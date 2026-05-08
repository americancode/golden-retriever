package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type ResolveOptions struct {
	IncludeDev         bool
	IncludeOptional    bool
	LegacyPeerDeps     bool
	StrictPeerDeps     bool
	OmitPeer           bool
	EngineStrict       bool
	NodeVersion        string
	Libc               string
	ResolveConcurrency int
}

type packageJSON struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	Overrides            json.RawMessage   `json:"overrides"`
	Workspaces           any               `json:"workspaces"`
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
		if fileExists(filepath.Join(input, "npm-shrinkwrap.json")) {
			return LoadLockfile(filepath.Join(input, "npm-shrinkwrap.json"))
		}
		if fileExists(filepath.Join(input, "package-lock.json")) {
			return LoadLockfile(filepath.Join(input, "package-lock.json"))
		}
		return ResolvePackageJSON(ctx, client, filepath.Join(input, "package.json"), opts)
	}
	base := filepath.Base(input)
	if base == "package-lock.json" || base == "npm-shrinkwrap.json" {
		return LoadLockfile(input)
	}
	return ResolvePackageJSON(ctx, client, input, opts)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func ResolvePackageJSON(ctx context.Context, client *Client, path string, opts ResolveOptions) (*Graph, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root packageJSON
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	if root.Workspaces != nil {
		return nil, fmt.Errorf("workspaces are not implemented yet")
	}
	rootSpecs := rootDependencySpecs(root)
	overrides, err := ParseOverrides(root.Overrides, rootSpecs)
	if err != nil {
		return nil, err
	}

	var deps []DependencyRequest
	deps, err = appendDeps(deps, root.Dependencies, EdgeProd)
	if err != nil {
		return nil, err
	}
	if opts.IncludeDev {
		deps, err = appendDeps(deps, root.DevDependencies, EdgeDev)
		if err != nil {
			return nil, err
		}
	}
	if opts.IncludeOptional {
		deps, err = appendDeps(deps, root.OptionalDependencies, EdgeOptional)
		if err != nil {
			return nil, err
		}
	}

	r := &Resolver{Client: client, Options: opts, Overrides: overrides}
	return r.ResolveRoot(ctx, deps)
}

func rootDependencySpecs(root packageJSON) map[string]string {
	specs := map[string]string{}
	for name, spec := range root.Dependencies {
		specs[name] = spec
	}
	for name, spec := range root.DevDependencies {
		if specs[name] == "" {
			specs[name] = spec
		}
	}
	for name, spec := range root.OptionalDependencies {
		if specs[name] == "" {
			specs[name] = spec
		}
	}
	for name, spec := range root.PeerDependencies {
		if specs[name] == "" {
			specs[name] = spec
		}
	}
	return specs
}

func mergeDeps(dst, src map[string]string) error {
	for name, spec := range src {
		if err := validateDependencySpec(name, spec, EdgeProd); err != nil {
			return err
		}
		dst[name] = spec
	}
	return nil
}

func appendDeps(dst []DependencyRequest, src map[string]string, edgeType EdgeType) ([]DependencyRequest, error) {
	for name, spec := range src {
		if err := validateDependencySpec(name, spec, edgeType); err != nil {
			return nil, err
		}
		dst = append(dst, DependencyRequest{Name: name, Spec: spec, Type: edgeType})
	}
	return dst, nil
}

var gitSSHSpecRe = regexp.MustCompile(`^[^@]+@[^:.]+\.[^:]+:.+$`)

func validateDependencySpec(name, spec string, edgeType EdgeType) error {
	spec = strings.TrimSpace(spec)
	if !validPackageName(name) {
		return &InvalidPackageNameError{Name: name, Spec: spec}
	}
	if spec == "" {
		return &UnsupportedSpecError{Name: name, Spec: spec, Type: string(edgeType)}
	}
	if strings.HasPrefix(strings.ToLower(spec), "npm:") {
		actual, wanted, err := parsePackageSpec(name, spec)
		if err != nil {
			return err
		}
		if !validPackageName(actual) {
			return &InvalidPackageNameError{Name: actual, Spec: spec}
		}
		return validateRegistryWanted(actual, wanted, edgeType)
	}
	return validateRegistryWanted(name, spec, edgeType)
}

func validateRegistryWanted(name, spec string, edgeType EdgeType) error {
	spec = strings.TrimSpace(spec)
	if unsupportedSpecClass(spec) {
		return &UnsupportedSpecError{Name: name, Spec: spec, Type: string(edgeType)}
	}
	if registryTagLike(spec) && !validTagName(spec) {
		return &InvalidTagNameError{Name: name, Spec: spec}
	}
	return nil
}

func isRegistrySpec(spec string) bool {
	return validateDependencySpec("pkg", spec, EdgeProd) == nil
}

func unsupportedSpecClass(spec string) bool {
	lower := strings.ToLower(strings.TrimSpace(spec))
	blockedPrefixes := []string{"file:", "link:", "git:", "git+", "github:", "gitlab:", "bitbucket:", "http:", "https:", "workspace:"}
	for _, prefix := range blockedPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") || strings.HasPrefix(spec, "~") {
		return true
	}
	if strings.Contains(spec, "/") || strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tar") {
		return true
	}
	return gitSSHSpecRe.MatchString(spec)
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

func isUnsupportedSpec(err error) bool {
	var specErr *UnsupportedSpecError
	return errors.As(err, &specErr)
}
