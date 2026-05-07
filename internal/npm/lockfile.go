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
	Name         string                 `json:"name"`
	Version      string                 `json:"version"`
	Resolved     string                 `json:"resolved"`
	Integrity    string                 `json:"integrity"`
	Dependencies map[string]string      `json:"dependencies"`
	Dev          bool                   `json:"dev"`
	Optional     bool                   `json:"optional"`
	Extra        map[string]interface{} `json:"-"`
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
			if loc == "" || pkg.Version == "" || pkg.Resolved == "" {
				continue
			}
			name := pkg.Name
			if name == "" {
				name = nameFromNodeModulesPath(loc)
			}
			g.Add(Package{Name: name, Version: pkg.Version, Tarball: pkg.Resolved, Integrity: pkg.Integrity})
		}
		return g, nil
	}

	for name, pkg := range lock.Dependencies {
		walkLockDependency(g, name, pkg)
	}
	return g, nil
}

func walkLockDependency(g *Graph, name string, pkg lockPackage) {
	if pkg.Version != "" && pkg.Resolved != "" {
		g.Add(Package{Name: name, Version: pkg.Version, Tarball: pkg.Resolved, Integrity: pkg.Integrity})
	}
}

func nameFromNodeModulesPath(path string) string {
	parts := strings.Split(path, "node_modules/")
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}
