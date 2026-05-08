package npm

import "fmt"

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
	Root                *Node
	PeerConflicts       []PeerConflict
	EngineWarnings      []*PackageEngineError
	DeprecationWarnings []PackageDeprecationWarning
	packages            map[string]Package
	nodes               map[string]*Node
}

func NewGraph() *Graph {
	root := &Node{
		ID:           "root",
		Name:         "",
		Version:      "",
		Dependencies: map[string]*Edge{},
		Peers:        map[string]*Edge{},
	}
	return &Graph{
		Root:     root,
		packages: map[string]Package{},
		nodes:    map[string]*Node{"root": root},
	}
}

func (g *Graph) Add(pkg Package) {
	g.AddNode(pkg)
}

func (g *Graph) AddNode(pkg Package) *Node {
	if pkg.Name == "" || pkg.Version == "" {
		return nil
	}
	g.packages[pkg.Key()] = pkg
	if node, ok := g.nodes[pkg.Key()]; ok {
		return node
	}
	node := &Node{
		ID:           pkg.Key(),
		Name:         pkg.Name,
		Version:      pkg.Version,
		Package:      pkg,
		Dependencies: map[string]*Edge{},
		Peers:        map[string]*Edge{},
	}
	g.nodes[node.ID] = node
	return node
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

func (g *Graph) Nodes() []*Node {
	out := make([]*Node, 0, len(g.nodes))
	for _, node := range g.nodes {
		if node.ID != "root" {
			out = append(out, node)
		}
	}
	sortNodes(out)
	return out
}

func (g *Graph) AddDependency(parent, child *Node, name, rawSpec, spec string, edgeType EdgeType) {
	if parent == nil || child == nil || edgeType == "" {
		return
	}
	if parent.Dependencies == nil {
		parent.Dependencies = map[string]*Edge{}
	}
	parent.Dependencies[name] = &Edge{
		From:      parent,
		To:        child,
		Name:      name,
		Spec:      spec,
		RawSpec:   rawSpec,
		Type:      edgeType,
		Satisfied: true,
	}
	if child.Parent == nil {
		child.Parent = parent
	}
}

func (g *Graph) AddPeer(parent, target *Node, name, spec string, optional bool, satisfied bool) {
	if parent == nil {
		return
	}
	if parent.Peers == nil {
		parent.Peers = map[string]*Edge{}
	}
	parent.Peers[name] = &Edge{
		From:         parent,
		To:           target,
		Name:         name,
		Spec:         spec,
		Type:         EdgePeer,
		PeerOptional: optional,
		Satisfied:    satisfied,
	}
}

func (g *Graph) AddPeerConflict(from, found *Node, name, spec string) {
	if from == nil || found == nil {
		return
	}
	g.PeerConflicts = append(g.PeerConflicts, PeerConflict{
		From:         from,
		Found:        found,
		Name:         name,
		Spec:         spec,
		FoundVersion: found.Version,
	})
}

func (g *Graph) AddEngineWarning(warning *PackageEngineError) {
	if warning == nil {
		return
	}
	g.EngineWarnings = append(g.EngineWarnings, warning)
}

type PackageDeprecationWarning struct {
	Package string `json:"package"`
	Message string `json:"message"`
}

func (g *Graph) AddDeprecationWarning(pkg Package, deprecated any) {
	if deprecated == nil {
		return
	}
	g.DeprecationWarnings = append(g.DeprecationWarnings, PackageDeprecationWarning{
		Package: pkg.Key(),
		Message: fmt.Sprint(deprecated),
	})
}

func clonePackages(in map[string]Package) map[string]Package {
	out := map[string]Package{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneNodes(in map[string]*Node) map[string]*Node {
	out := map[string]*Node{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneResolved(in map[string]*Node) map[string]*Node {
	out := map[string]*Node{}
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneEdges(in map[string]*Edge) map[string]*Edge {
	if in == nil {
		return nil
	}
	out := map[string]*Edge{}
	for key, value := range in {
		if value == nil {
			out[key] = nil
			continue
		}
		edge := *value
		out[key] = &edge
	}
	return out
}

type Node struct {
	ID           string
	Name         string
	Version      string
	Package      Package
	Parent       *Node
	Dependencies map[string]*Edge
	Peers        map[string]*Edge
}

type EdgeType string

const (
	EdgeProd     EdgeType = "prod"
	EdgeDev      EdgeType = "dev"
	EdgeOptional EdgeType = "optional"
	EdgePeer     EdgeType = "peer"
)

type Edge struct {
	From         *Node
	To           *Node
	Name         string
	Spec         string
	RawSpec      string
	Type         EdgeType
	PeerOptional bool
	Satisfied    bool
}

type PeerConflict struct {
	From         *Node
	Found        *Node
	Name         string
	Spec         string
	FoundVersion string
}
