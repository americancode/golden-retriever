package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	deps = appendDeps(deps, root.Dependencies, EdgeProd)
	if opts.IncludeDev {
		deps = appendDeps(deps, root.DevDependencies, EdgeDev)
	}
	if opts.IncludeOptional {
		deps = appendDeps(deps, root.OptionalDependencies, EdgeOptional)
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
	return specs
}

func mergeDeps(dst, src map[string]string) {
	for name, spec := range src {
		if isRegistrySpec(spec) {
			dst[name] = spec
		}
	}
}

func appendDeps(dst []DependencyRequest, src map[string]string, edgeType EdgeType) []DependencyRequest {
	for name, spec := range src {
		if isRegistrySpec(spec) {
			dst = append(dst, DependencyRequest{Name: name, Spec: spec, Type: edgeType})
		}
	}
	return dst
}

func isRegistrySpec(spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return false
	}
	if strings.HasPrefix(spec, "npm:") {
		return true
	}
	blockedPrefixes := []string{"file:", "link:", "git:", "git+", "github:", "http:", "https:"}
	for _, prefix := range blockedPrefixes {
		if strings.HasPrefix(spec, prefix) {
			return false
		}
	}
	return true
}
