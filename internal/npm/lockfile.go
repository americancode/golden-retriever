package npm

import (
	"encoding/json"
	"os"
	"strings"
)

type lockfile struct {
	Name            string                 `json:"name"`
	LockfileVersion int                    `json:"lockfileVersion"`
	Packages        map[string]lockPackage `json:"packages"`
	Dependencies    map[string]lockPackage `json:"dependencies"`
}

type lockPackage struct {
	Name               string                 `json:"name"`
	Version            string                 `json:"version"`
	Resolved           string                 `json:"resolved"`
	Integrity          string                 `json:"integrity"`
	Dependencies       map[string]string      `json:"dependencies"`
	NestedDependencies map[string]lockPackage `json:"-"`
	Requires           map[string]string      `json:"requires"`
	Dev                bool                   `json:"dev"`
	Optional           bool                   `json:"optional"`
	InBundle           bool                   `json:"inBundle"`
	Link               bool                   `json:"link"`
	Extra              map[string]interface{} `json:"-"`
}

func (p *lockPackage) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name         string            `json:"name"`
		Version      string            `json:"version"`
		Resolved     string            `json:"resolved"`
		Integrity    string            `json:"integrity"`
		Dependencies json.RawMessage   `json:"dependencies"`
		Requires     map[string]string `json:"requires"`
		Dev          bool              `json:"dev"`
		Optional     bool              `json:"optional"`
		InBundle     bool              `json:"inBundle"`
		Link         bool              `json:"link"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.Name = raw.Name
	p.Version = raw.Version
	p.Resolved = raw.Resolved
	p.Integrity = raw.Integrity
	p.Requires = raw.Requires
	p.Dev = raw.Dev
	p.Optional = raw.Optional
	p.InBundle = raw.InBundle
	p.Link = raw.Link
	if len(raw.Dependencies) > 0 && string(raw.Dependencies) != "null" {
		var specs map[string]string
		if err := json.Unmarshal(raw.Dependencies, &specs); err == nil {
			p.Dependencies = specs
		} else {
			var nested map[string]lockPackage
			if nestedErr := json.Unmarshal(raw.Dependencies, &nested); nestedErr != nil {
				return err
			}
			p.NestedDependencies = nested
		}
	}
	return nil
}

func LoadLockfile(path string) (*Graph, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lock lockfile
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}

	g := NewGraph()
	if len(lock.Packages) > 0 {
		for loc, pkg := range lock.Packages {
			if loc == "" || skipLockPackage(pkg) {
				continue
			}
			name := pkg.Name
			if name == "" {
				name = nameFromNodeModulesPath(loc)
			}
			resolved := pkg.Resolved
			if resolved == "" {
				resolved = defaultTarballURL(name, pkg.Version)
			}
			g.Add(Package{Name: name, Version: pkg.Version, Tarball: resolved, Integrity: pkg.Integrity})
		}
	}

	for name, pkg := range lock.Dependencies {
		walkLockDependency(g, name, pkg)
	}
	return g, nil
}

func walkLockDependency(g *Graph, name string, pkg lockPackage) {
	if skipLockPackage(pkg) {
		return
	}
	if pkg.Version != "" {
		resolved := pkg.Resolved
		if resolved == "" {
			resolved = defaultTarballURL(name, pkg.Version)
		}
		g.Add(Package{Name: name, Version: pkg.Version, Tarball: resolved, Integrity: pkg.Integrity})
	}
	for childName, child := range pkg.NestedDependencies {
		walkLockDependency(g, childName, child)
	}
}

func skipLockPackage(pkg lockPackage) bool {
	if pkg.Version == "" || pkg.InBundle || pkg.Link {
		return true
	}
	if !parseVersion(pkg.Version).ok {
		return true
	}
	return localLockResolved(pkg.Resolved)
}

func localLockResolved(resolved string) bool {
	resolved = strings.TrimSpace(resolved)
	if resolved == "" {
		return false
	}
	lower := strings.ToLower(resolved)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return false
	}
	if strings.HasPrefix(lower, "file:") || strings.HasPrefix(lower, "link:") ||
		strings.HasPrefix(lower, "git:") || strings.HasPrefix(lower, "git+") ||
		strings.HasPrefix(lower, "github:") || strings.HasPrefix(lower, "gitlab:") ||
		strings.HasPrefix(lower, "bitbucket:") || strings.HasPrefix(lower, "gist:") ||
		strings.HasPrefix(lower, "ssh:") || strings.HasPrefix(lower, "svn:") {
		return true
	}
	return strings.HasPrefix(resolved, ".") || strings.HasPrefix(resolved, "/") ||
		strings.HasPrefix(resolved, "~") || windowsDriveSpecRe.MatchString(resolved)
}

func defaultTarballURL(name, version string) string {
	if name == "" || version == "" {
		return ""
	}
	baseName := name
	if strings.HasPrefix(name, "@") {
		if _, after, ok := strings.Cut(name, "/"); ok {
			baseName = after
		}
	}
	return DefaultRegistry + "/" + name + "/-/" + baseName + "-" + version + ".tgz"
}

func nameFromNodeModulesPath(path string) string {
	parts := strings.Split(path, "node_modules/")
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}
