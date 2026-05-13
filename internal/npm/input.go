package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	NPMPlatforms       []NPMPlatform
	Progress           func(format string, args ...any)
}

type NPMPlatform struct {
	OS   string
	CPU  string
	Libc string
}

func (p NPMPlatform) Label() string {
	parts := []string{strings.TrimSpace(p.OS), strings.TrimSpace(p.CPU)}
	if strings.TrimSpace(p.Libc) != "" {
		parts = append(parts, strings.TrimSpace(p.Libc))
	}
	out := []string{}
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return "current"
	}
	return strings.Join(out, "/")
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
	if isLockfilePath(input) {
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
	if len(opts.NPMPlatforms) == 0 {
		if opts.Progress != nil {
			opts.Progress("resolve:npm-lock:start input=%s platform=current", path)
		}
		graph, err := resolveViaNPMLockfile(ctx, path, NPMPlatform{})
		if err == nil && opts.Progress != nil {
			opts.Progress("resolve:npm-lock:done input=%s platform=current packages=%d", path, len(graph.Packages()))
		}
		return graph, err
	}
	merged := NewGraph()
	for _, platform := range opts.NPMPlatforms {
		if opts.Progress != nil {
			opts.Progress("resolve:npm-lock:start input=%s platform=%s", path, platform.Label())
		}
		graph, err := resolveViaNPMLockfile(ctx, path, platform)
		if err != nil {
			return nil, fmt.Errorf("platform %s: %w", platform.Label(), err)
		}
		for _, pkg := range graph.Packages() {
			merged.Add(pkg)
		}
		if opts.Progress != nil {
			opts.Progress("resolve:npm-lock:done input=%s platform=%s packages=%d unique=%d", path, platform.Label(), len(graph.Packages()), len(merged.Packages()))
		}
	}
	return merged, nil
}

func sortedDependencyNames(deps map[string]string) []string {
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func resolveViaNPMLockfile(ctx context.Context, packageJSONPath string, platform NPMPlatform) (*Graph, error) {
	if _, err := exec.LookPath("npm"); err != nil {
		return nil, fmt.Errorf("npm is required to resolve package.json via lockfile generation: %w", err)
	}
	srcData, err := os.ReadFile(packageJSONPath)
	if err != nil {
		return nil, err
	}
	projectDir := filepath.Dir(packageJSONPath)
	resolvedPackageJSON := packageJSONPath
	lockPath := filepath.Join(projectDir, "package-lock.json")
	if filepath.Base(packageJSONPath) != "package.json" {
		tempProjectDir, err := cloneProjectDirForResolution(projectDir)
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tempProjectDir)
		tempPackageJSON := filepath.Join(tempProjectDir, "package.json")
		if err := os.WriteFile(tempPackageJSON, srcData, 0o644); err != nil {
			return nil, err
		}
		resolvedPackageJSON = tempPackageJSON
		lockPath = filepath.Join(tempProjectDir, "package-lock.json")
	}

	origLockData, lockReadErr := os.ReadFile(lockPath)
	origLockExists := lockReadErr == nil
	if lockReadErr != nil && !os.IsNotExist(lockReadErr) {
		return nil, lockReadErr
	}
	cmd := exec.CommandContext(ctx, "npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--progress=false")
	cmd.Dir = filepath.Dir(resolvedPackageJSON)
	cmd.Env = npmPlatformEnv(os.Environ(), platform)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("npm lockfile resolution failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	g, loadErr := LoadLockfile(lockPath)
	if filepath.Base(packageJSONPath) == "package.json" && origLockExists {
		_ = os.WriteFile(lockPath, origLockData, 0o644)
	} else if filepath.Base(packageJSONPath) == "package.json" {
		_ = os.Remove(lockPath)
	}
	return g, loadErr
}

func npmPlatformEnv(env []string, platform NPMPlatform) []string {
	if strings.TrimSpace(platform.OS) != "" {
		env = append(env, "npm_config_os="+strings.TrimSpace(platform.OS))
	}
	if strings.TrimSpace(platform.CPU) != "" {
		env = append(env, "npm_config_cpu="+strings.TrimSpace(platform.CPU))
	}
	if strings.TrimSpace(platform.Libc) != "" {
		env = append(env, "npm_config_libc="+strings.TrimSpace(platform.Libc))
	}
	return env
}

func cloneProjectDirForResolution(srcDir string) (string, error) {
	dstDir, err := os.MkdirTemp("", "golden-retriever-project-*")
	if err != nil {
		return "", err
	}
	err = filepath.WalkDir(srcDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() && (d.Name() == "node_modules" || d.Name() == ".git") {
			return filepath.SkipDir
		}
		dstPath := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0o755)
		}
		return copyFile(path, dstPath)
	})
	if err != nil {
		_ = os.RemoveAll(dstDir)
		return "", err
	}
	return dstDir, nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func isLockfilePath(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var probe struct {
		LockfileVersion *int `json:"lockfileVersion"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false
	}
	return probe.LockfileVersion != nil
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
