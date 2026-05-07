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
	IncludeDev          bool
	IncludeOptional     bool
	ResolveConcurrency int
}

type packageJSON struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
	Overrides            any               `json:"overrides"`
	Workspaces           any               `json:"workspaces"`
}

func LoadInput(ctx context.Context, client *Client, input string, opts ResolveOptions) (*Graph, error) {
	base := filepath.Base(input)
	if base == "package-lock.json" || base == "npm-shrinkwrap.json" {
		return LoadLockfile(input)
	}
	return ResolvePackageJSON(ctx, client, input, opts)
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
	if root.Overrides != nil {
		return nil, fmt.Errorf("package overrides are not implemented yet")
	}
	if root.Workspaces != nil {
		return nil, fmt.Errorf("workspaces are not implemented yet")
	}

	deps := map[string]string{}
	mergeDeps(deps, root.Dependencies)
	if opts.IncludeDev {
		mergeDeps(deps, root.DevDependencies)
	}
	if opts.IncludeOptional {
		mergeDeps(deps, root.OptionalDependencies)
	}

	r := &Resolver{Client: client, Options: opts}
	return r.Resolve(ctx, deps)
}

func mergeDeps(dst, src map[string]string) {
	for name, spec := range src {
		if isRegistrySpec(spec) {
			dst[name] = spec
		}
	}
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
