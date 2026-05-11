package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type ResolveOptions struct {
	IncludeDev         bool
	IncludeOptional    bool
	InstallStrategy    string
	LegacyPeerDeps     bool
	StrictPeerDeps     bool
	OmitPeer           bool
	PreferDedupe       bool
	EngineStrict       bool
	NodeVersion        string
	Libc               string
	Before             time.Time
	DefaultTag         string
	IncludeStaged      bool
	Avoid              string
	AvoidStrict        bool
	ResolveConcurrency int
}

type packageJSON struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	PeerDependenciesMeta map[string]struct {
		Optional bool `json:"optional"`
	} `json:"peerDependenciesMeta"`
	Overrides  json.RawMessage `json:"overrides"`
	Workspaces any             `json:"workspaces"`
}

type UnsupportedSpecError struct {
	Name string
	Spec string
	Type string
}

func (e *UnsupportedSpecError) Error() string {
	return fmt.Sprintf("%s dependency %s uses unsupported spec %q", e.Type, e.Name, e.Spec)
}

type InvalidPackageNameError struct {
	Name string
	Spec string
}

func (e *InvalidPackageNameError) Error() string {
	return fmt.Sprintf("invalid package name %q for dependency spec %q", e.Name, e.Spec)
}

type InvalidTagNameError struct {
	Name string
	Spec string
}

func (e *InvalidTagNameError) Error() string {
	return fmt.Sprintf("invalid tag name %q for package %s", e.Spec, e.Name)
}

func LoadInput(ctx context.Context, client *Client, input string, opts ResolveOptions) (*Graph, error) {
	info, err := os.Stat(input)
	if err == nil && info.IsDir() {
		yarnPath := filepath.Join(input, "yarn.lock")
		if fileExists(filepath.Join(input, "npm-shrinkwrap.json")) {
			return loadLockfile(filepath.Join(input, "npm-shrinkwrap.json"), yarnPath)
		}
		if fileExists(filepath.Join(input, "package-lock.json")) {
			return loadLockfile(filepath.Join(input, "package-lock.json"), yarnPath)
		}
		return ResolvePackageJSON(ctx, client, filepath.Join(input, "package.json"), opts)
	}
	base := filepath.Base(input)
	if base == "package-lock.json" || base == "npm-shrinkwrap.json" {
		return loadLockfile(input, filepath.Join(filepath.Dir(input), "yarn.lock"))
	}
	return ResolvePackageJSON(ctx, client, input, opts)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func ResolvePackageJSON(ctx context.Context, client *Client, path string, opts ResolveOptions) (*Graph, error) {
	_ = client
	_ = opts
	return resolveViaNPMLockfile(ctx, path)
}

func sortedDependencyNames(deps map[string]string) []string {
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func resolveViaNPMLockfile(ctx context.Context, packageJSONPath string) (*Graph, error) {
	if _, err := exec.LookPath("npm"); err != nil {
		return nil, fmt.Errorf("npm is required to resolve package.json via lockfile generation: %w", err)
	}
	tempDir, err := os.MkdirTemp("", "golden-retriever-npm-lock-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)

	srcData, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(tempDir, "package.json"), srcData, 0o644); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--progress=false")
	cmd.Dir = tempDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("npm lockfile resolution failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return LoadLockfile(filepath.Join(tempDir, "package-lock.json"))
}

var gitSSHSpecRe = regexp.MustCompile(`^[^@]+@[^:.]+\.[^:]+:.+$`)
var windowsDriveSpecRe = regexp.MustCompile(`^[a-zA-Z]:`)
var npaURLSpecRe = regexp.MustCompile(`^(?i:(?:git\+)?[a-z]+:)`)

func validateDependencySpec(name, spec string, edgeType EdgeType) error {
	rawSpec := spec
	spec = strings.TrimSpace(spec)
	if !validPackageName(name) {
		return &InvalidPackageNameError{Name: name, Spec: rawSpec}
	}
	if spec == "" {
		return &UnsupportedSpecError{Name: name, Spec: rawSpec, Type: string(edgeType)}
	}
	if strings.HasPrefix(strings.ToLower(rawSpec), "npm:") {
		aliasTarget := rawSpec[len("npm:"):]
		if strings.HasPrefix(strings.ToLower(aliasTarget), "npm:") {
			return &UnsupportedSpecError{Name: name, Spec: rawSpec, Type: string(edgeType)}
		}
		actual, wanted, err := parsePackageSpec(name, rawSpec)
		if err != nil {
			var aliasErr *nonRegistryAliasError
			if errors.As(err, &aliasErr) {
				return &UnsupportedSpecError{Name: name, Spec: rawSpec, Type: string(edgeType)}
			}
			return err
		}
		if !validPackageName(actual) {
			if unsupportedSpecClass(aliasTarget) {
				return &UnsupportedSpecError{Name: name, Spec: rawSpec, Type: string(edgeType)}
			}
			return &InvalidPackageNameError{Name: actual, Spec: rawSpec}
		}
		return validateRegistryWanted(actual, wanted, edgeType)
	}
	return validateRegistryWanted(name, rawSpec, edgeType)
}

func validateRegistryWanted(name, spec string, edgeType EdgeType) error {
	spec = strings.TrimSpace(spec)
	if isRemoteTarballSpec(spec) {
		return nil
	}
	if unsupportedSpecClass(spec) {
		return &UnsupportedSpecError{Name: name, Spec: spec, Type: string(edgeType)}
	}
	if registryTagLike(spec) && !validTagName(spec) {
		return &InvalidTagNameError{Name: name, Spec: spec}
	}
	return nil
}

func unsupportedSpecClass(spec string) bool {
	lower := strings.ToLower(strings.TrimSpace(spec))
	if isRemoteTarballSpec(spec) {
		return false
	}
	blockedPrefixes := []string{"file:", "link:", "github:", "gitlab:", "bitbucket:", "gist:", "ssh:", "svn:", "workspace:"}
	for _, prefix := range blockedPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") || strings.HasPrefix(spec, "~/") || windowsDriveSpecRe.MatchString(spec) {
		return true
	}
	if npaURLSpecRe.MatchString(spec) {
		return true
	}
	if strings.Contains(spec, "/") || strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tar") {
		return true
	}
	return gitSSHSpecRe.MatchString(spec)
}

func isRemoteTarballSpec(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return false
	}
	u, err := url.Parse(spec)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return false
	}
	path := strings.ToLower(u.Path)
	return strings.HasSuffix(path, ".tgz") || strings.HasSuffix(path, ".tar.gz") || strings.HasSuffix(path, ".tar")
}

func registryTagLike(spec string) bool {
	if spec == "" || spec == "*" || strings.Contains(spec, "||") || strings.ContainsAny(spec, "<>=~^xX*") {
		return false
	}
	if parseVersion(spec).ok || partialLooksLikeRange(spec) || strings.Contains(spec, " - ") {
		return false
	}
	return true
}

func validTagName(tag string) bool {
	return tag != "" && encodeURIComponentSafe(tag)
}

func validPackageName(name string) bool {
	if name == "" || strings.TrimSpace(name) != name {
		return false
	}
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "-") || strings.HasPrefix(name, "_") {
		return false
	}
	if strings.EqualFold(name, "node_modules") || strings.EqualFold(name, "favicon.ico") {
		return false
	}
	if strings.HasPrefix(name, "@") {
		scope, pkg, ok := strings.Cut(name, "/")
		return ok && validPackageNamePart(strings.TrimPrefix(scope, "@")) && validPackageNamePart(pkg) && !strings.HasPrefix(pkg, ".")
	}
	return validPackageNamePart(name)
}

func validPackageNamePart(part string) bool {
	if part == "" || strings.TrimSpace(part) != part {
		return false
	}
	return encodeURIComponentSafe(part)
}

func encodeURIComponentSafe(value string) bool {
	for _, r := range value {
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		switch r {
		case '-', '_', '.', '!', '~', '*', '\'', '(', ')':
			continue
		default:
			return false
		}
	}
	return true
}
