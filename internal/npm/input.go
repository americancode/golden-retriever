package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type ResolveOptions struct {
	IncludeDev         bool
	IncludeOptional    bool
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
	Overrides            json.RawMessage   `json:"overrides"`
	Workspaces           any               `json:"workspaces"`
}

type workspacePackage struct {
	Name string
	Root packageJSON
	Path string
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

type UnsupportedWorkspacesError struct{}

func (e *UnsupportedWorkspacesError) Error() string {
	return "workspaces are not implemented yet"
}

type DuplicateWorkspaceError struct {
	Name  string
	First string
	Other string
}

func (e *DuplicateWorkspaceError) Error() string {
	return fmt.Sprintf("duplicate workspace package %q at %s and %s", e.Name, e.First, e.Other)
}

type WorkspaceDependencyError struct {
	Name    string
	Spec    string
	Version string
}

func (e *WorkspaceDependencyError) Error() string {
	return fmt.Sprintf("workspace dependency %s@%s does not satisfy local workspace version %s", e.Name, e.Spec, e.Version)
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
	workspaces, err := loadWorkspaces(filepath.Dir(path), root.Workspaces)
	if err != nil {
		return nil, err
	}
	workspaceVersions := map[string]string{}
	for _, workspace := range workspaces {
		if workspace.Name != "" {
			workspaceVersions[workspace.Name] = workspace.Root.Version
		}
	}
	rootSpecs := rootDependencySpecs(root)
	overrides, err := ParseOverrides(root.Overrides, rootSpecs)
	if err != nil {
		return nil, err
	}

	var deps []DependencyRequest
	deps, err = appendDeps(deps, root.Dependencies, EdgeProd, workspaceVersions)
	if err != nil {
		return nil, err
	}
	if opts.IncludeDev {
		deps, err = appendDeps(deps, root.DevDependencies, EdgeDev, workspaceVersions)
		if err != nil {
			return nil, err
		}
	}
	if opts.IncludeOptional {
		deps, err = appendDeps(deps, root.OptionalDependencies, EdgeOptional, workspaceVersions)
		if err != nil {
			return nil, err
		}
	}
	for _, workspace := range workspaces {
		deps, err = appendDeps(deps, workspace.Root.Dependencies, EdgeProd, workspaceVersions)
		if err != nil {
			return nil, err
		}
		if opts.IncludeDev {
			deps, err = appendDeps(deps, workspace.Root.DevDependencies, EdgeDev, workspaceVersions)
			if err != nil {
				return nil, err
			}
		}
		if opts.IncludeOptional {
			deps, err = appendDeps(deps, workspace.Root.OptionalDependencies, EdgeOptional, workspaceVersions)
			if err != nil {
				return nil, err
			}
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

func appendDeps(dst []DependencyRequest, src map[string]string, edgeType EdgeType, workspaceVersions map[string]string) ([]DependencyRequest, error) {
	for _, name := range sortedDependencyNames(src) {
		spec := src[name]
		satisfied, err := workspaceDependencySatisfied(workspaceVersions, name, spec)
		if err != nil {
			return nil, err
		}
		if satisfied {
			continue
		}
		if err := validateDependencySpec(name, spec, edgeType); err != nil {
			return nil, err
		}
		dst = append(dst, DependencyRequest{Name: name, Spec: spec, Type: edgeType})
	}
	return dst, nil
}

func loadWorkspaces(rootDir string, raw any) ([]workspacePackage, error) {
	patterns, err := workspacePatterns(raw)
	if err != nil {
		return nil, err
	}
	var out []workspacePackage
	seen := map[string]bool{}
	seenNames := map[string]string{}
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") {
			continue
		}
		matches, err := filepath.Glob(filepath.Join(rootDir, filepath.FromSlash(pattern)))
		if err != nil {
			return nil, fmt.Errorf("invalid workspace pattern %q: %w", pattern, err)
		}
		sort.Strings(matches)
		for _, match := range matches {
			info, err := os.Stat(match)
			if err != nil || !info.IsDir() {
				continue
			}
			pkgPath := filepath.Join(match, "package.json")
			data, err := os.ReadFile(pkgPath)
			if err != nil {
				continue
			}
			var pkg packageJSON
			if err := json.Unmarshal(data, &pkg); err != nil {
				return nil, err
			}
			if pkg.Name == "" || seen[pkgPath] {
				continue
			}
			if first := seenNames[pkg.Name]; first != "" {
				return nil, &DuplicateWorkspaceError{Name: pkg.Name, First: first, Other: pkgPath}
			}
			seen[pkgPath] = true
			seenNames[pkg.Name] = pkgPath
			out = append(out, workspacePackage{Name: pkg.Name, Root: pkg, Path: pkgPath})
		}
	}
	return out, nil
}

func workspacePatterns(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	var patterns []string
	switch value := raw.(type) {
	case []any:
		for _, item := range value {
			pattern, ok := item.(string)
			if !ok {
				return nil, &UnsupportedWorkspacesError{}
			}
			patterns = append(patterns, pattern)
		}
	case map[string]any:
		pkgs, ok := value["packages"]
		if !ok {
			return nil, nil
		}
		items, ok := pkgs.([]any)
		if !ok {
			return nil, &UnsupportedWorkspacesError{}
		}
		for _, item := range items {
			pattern, ok := item.(string)
			if !ok {
				return nil, &UnsupportedWorkspacesError{}
			}
			patterns = append(patterns, pattern)
		}
	default:
		return nil, &UnsupportedWorkspacesError{}
	}
	return patterns, nil
}

func isWorkspaceReference(spec string) bool {
	spec = strings.TrimSpace(strings.ToLower(spec))
	return spec == "" || strings.HasPrefix(spec, "workspace:") || strings.HasPrefix(spec, "file:") || strings.HasPrefix(spec, "link:")
}

func workspaceDependencySatisfied(workspaceVersions map[string]string, name, spec string) (bool, error) {
	version, ok := workspaceVersions[name]
	if !ok {
		return false, nil
	}
	if isWorkspaceReference(spec) && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(spec)), "workspace:") {
		return true, nil
	}
	if wanted, ok := workspaceWanted(spec); ok {
		if workspaceWantedSatisfied(version, wanted) {
			return true, nil
		}
		return false, &WorkspaceDependencyError{Name: name, Spec: spec, Version: version}
	}
	return version != "" && satisfies(version, spec), nil
}

func workspaceWanted(spec string) (string, bool) {
	spec = strings.TrimSpace(spec)
	if !strings.HasPrefix(strings.ToLower(spec), "workspace:") {
		return "", false
	}
	return strings.TrimSpace(spec[len("workspace:"):]), true
}

func workspaceWantedSatisfied(version, wanted string) bool {
	if version == "" {
		return false
	}
	switch strings.TrimSpace(wanted) {
	case "", "*", "^", "~":
		return true
	default:
		return satisfies(version, wanted)
	}
}

func sortedDependencyNames(deps map[string]string) []string {
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
