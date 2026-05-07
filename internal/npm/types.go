package npm

type Package struct {
	Name      string
	Version   string
	Tarball   string
	Integrity string
	Shasum    string
}

func (p Package) Key() string {
	return p.Name + "@" + p.Version
}

type Graph struct {
	packages map[string]Package
}

func NewGraph() *Graph {
	return &Graph{packages: map[string]Package{}}
}

func (g *Graph) Add(pkg Package) {
	if pkg.Name == "" || pkg.Version == "" {
		return
	}
	g.packages[pkg.Key()] = pkg
}

func (g *Graph) Has(key string) bool {
	_, ok := g.packages[key]
	return ok
}

func (g *Graph) Packages() []Package {
	out := make([]Package, 0, len(g.packages))
	for _, pkg := range g.packages {
		out = append(out, pkg)
	}
	sortPackages(out)
	return out
}
