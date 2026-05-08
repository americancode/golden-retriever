package npm

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type PeerConflictError struct {
	Package      string
	PeerName     string
	PeerSpec     string
	FoundVersion string
}

func (e *PeerConflictError) Error() string {
	return fmt.Sprintf("%s requires peer %s@%s, but found %s@%s", e.Package, e.PeerName, e.PeerSpec, e.PeerName, e.FoundVersion)
}

type Resolver struct {
	Client    *Client
	Options   ResolveOptions
	Overrides *Overrides

	mu       sync.Mutex
	graph    *Graph
	resolved map[string]*Node
	inflight map[string]*resolveCall
	fetchSem chan struct{}
}

type DependencyRequest struct {
	Name string
	Spec string
	Type EdgeType
}

type resolveCall struct {
	done chan struct{}
	node *Node
	err  error
}

func (r *Resolver) Resolve(ctx context.Context, deps map[string]string) (*Graph, error) {
	requests := make([]DependencyRequest, 0, len(deps))
	for _, name := range sortedDependencyNames(deps) {
		spec := deps[name]
		requests = append(requests, DependencyRequest{Name: name, Spec: spec, Type: EdgeProd})
	}
	return r.ResolveRoot(ctx, requests)
}

func (r *Resolver) ResolveRoot(ctx context.Context, deps []DependencyRequest) (*Graph, error) {
	r.graph = NewGraph()
	r.resolved = map[string]*Node{}
	r.inflight = map[string]*resolveCall{}
	if r.Options.ResolveConcurrency <= 0 {
		r.Options.ResolveConcurrency = 32
	}
	r.fetchSem = make(chan struct{}, r.Options.ResolveConcurrency)
	if err := r.resolveRequests(ctx, r.graph.Root, deps); err != nil {
		return nil, err
	}
	r.reconcileOptionalPeers()
	return r.graph, nil
}

func (r *Resolver) resolveDeps(ctx context.Context, parent *Node, deps map[string]string, edgeType EdgeType) error {
	requests := make([]DependencyRequest, 0, len(deps))
	for _, name := range sortedDependencyNames(deps) {
		spec := deps[name]
		requests = append(requests, DependencyRequest{Name: name, Spec: spec, Type: edgeType})
	}
	if edgeType == EdgeOptional {
		return r.resolveOptionalRequests(ctx, parent, requests)
	}
	return r.resolveRequests(ctx, parent, requests)
}

func (r *Resolver) resolveRequests(ctx context.Context, parent *Node, deps []DependencyRequest) error {
	for _, dep := range deps {
		if _, err := r.resolveDep(ctx, parent, dep.Name, dep.Spec, dep.Type); err != nil {
			return err
		}
	}
	return nil
}

func (r *Resolver) resolveOptionalRequests(ctx context.Context, parent *Node, deps []DependencyRequest) error {
	for _, dep := range deps {
		snapshot := r.snapshot()
		if _, err := r.resolveDep(ctx, parent, dep.Name, dep.Spec, dep.Type); err != nil {
			r.restore(snapshot)
		}
	}
	return nil
}

func (r *Resolver) resolveDep(ctx context.Context, parent *Node, name, spec string, edgeType EdgeType) (*Node, error) {
	rawSpec := spec
	overrideRule := (*OverrideRule)(nil)
	spec, overrideRule = r.overrideSpec(parent, name, spec)
	if parent != nil && parent.ID == "root" && overrideRule != nil && spec != rawSpec && !isOverrideReference(overrideRule.Spec) {
		return nil, &OverrideConflictError{Name: name, RawSpec: rawSpec, Spec: spec}
	}
	actualName, wanted, err := parsePackageSpec(name, spec)
	if err != nil {
		return nil, err
	}
	if canReuseExistingForSpec(wanted) {
		if existing := r.findExistingSatisfyingNode(actualName, wanted); existing != nil {
			r.mu.Lock()
			r.graph.AddDependency(parent, existing, name, rawSpec, spec, edgeType)
			r.mu.Unlock()
			return existing, nil
		}
	}
	if err := r.acquireFetchSlot(ctx); err != nil {
		return nil, err
	}
	pack, err := r.Client.Packument(ctx, actualName)
	r.releaseFetchSlot()
	if err != nil {
		return nil, err
	}
	version, err := pickVersionWithOptions(pack, wanted, r.Options)
	if err != nil {
		return nil, err
	}
	key := actualName + "@" + version

	r.mu.Lock()
	if node, ok := r.resolved[key]; ok {
		r.graph.AddDependency(parent, node, name, rawSpec, spec, edgeType)
		r.mu.Unlock()
		return node, nil
	}
	if call, ok := r.inflight[key]; ok {
		r.mu.Unlock()
		<-call.done
		if call.node != nil {
			r.mu.Lock()
			r.graph.AddDependency(parent, call.node, name, rawSpec, spec, edgeType)
			r.mu.Unlock()
		}
		return call.node, call.err
	}
	call := &resolveCall{done: make(chan struct{})}
	r.inflight[key] = call
	r.mu.Unlock()

	node, err := r.resolveManifest(ctx, parent, name, rawSpec, spec, edgeType, actualName, version, pack)
	r.finishResolve(key, call, node, err)
	return node, err
}

type resolverSnapshot struct {
	graph          graphSnapshot
	resolved       map[string]*Node
	parentByID     map[string]*Node
	depsByID       map[string]map[string]*Edge
	peersByID      map[string]map[string]*Edge
	peerConflicts  []PeerConflict
	engineWarnings []*PackageEngineError
	deprecations   []PackageDeprecationWarning
}

type graphSnapshot struct {
	packages map[string]Package
	nodes    map[string]*Node
}

func (r *Resolver) snapshot() resolverSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := resolverSnapshot{
		graph: graphSnapshot{
			packages: clonePackages(r.graph.packages),
			nodes:    cloneNodes(r.graph.nodes),
		},
		resolved:       cloneResolved(r.resolved),
		parentByID:     map[string]*Node{},
		depsByID:       map[string]map[string]*Edge{},
		peersByID:      map[string]map[string]*Edge{},
		peerConflicts:  append([]PeerConflict(nil), r.graph.PeerConflicts...),
		engineWarnings: append([]*PackageEngineError(nil), r.graph.EngineWarnings...),
		deprecations:   append([]PackageDeprecationWarning(nil), r.graph.DeprecationWarnings...),
	}
	for id, node := range r.graph.nodes {
		s.parentByID[id] = node.Parent
		s.depsByID[id] = cloneEdges(node.Dependencies)
		s.peersByID[id] = cloneEdges(node.Peers)
	}
	return s
}

func (r *Resolver) restore(s resolverSnapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.graph.packages = s.graph.packages
	r.graph.nodes = s.graph.nodes
	r.graph.PeerConflicts = s.peerConflicts
	r.graph.EngineWarnings = s.engineWarnings
	r.graph.DeprecationWarnings = s.deprecations
	r.resolved = s.resolved
	for id, node := range r.graph.nodes {
		node.Parent = s.parentByID[id]
		node.Dependencies = s.depsByID[id]
		node.Peers = s.peersByID[id]
	}
}

func (r *Resolver) resolveManifest(ctx context.Context, parent *Node, depName, rawSpec, depSpec string, edgeType EdgeType, actualName, version string, pack *Packument) (*Node, error) {
	manifest, ok := pack.Versions[version]
	if !ok {
		return nil, fmt.Errorf("%s@%s missing from packument", actualName, version)
	}
	pkgName := manifest.Name
	if pkgName == "" {
		pkgName = actualName
	}
	pkgVersion := manifest.Version
	if pkgVersion == "" {
		pkgVersion = version
	}
	if compatible, platformErr := platformCompatible(manifest, r.Options); !compatible {
		if edgeType == EdgeOptional {
			return nil, nil
		}
		return nil, platformErr
	}
	if compatible, engineErr := engineCompatible(manifest, r.Options); !compatible {
		if edgeType == EdgeOptional {
			return nil, nil
		}
		if r.Options.EngineStrict {
			return nil, engineErr
		}
		r.mu.Lock()
		r.graph.AddEngineWarning(engineErr)
		r.mu.Unlock()
	}

	pkg := Package{
		Name:      pkgName,
		Version:   pkgVersion,
		Tarball:   manifest.Dist.Tarball,
		Integrity: manifest.Dist.Integrity,
		Shasum:    manifest.Dist.Shasum,
	}
	r.mu.Lock()
	node := r.graph.AddNode(pkg)
	r.resolved[pkg.Key()] = node
	r.graph.AddDependency(parent, node, depName, rawSpec, depSpec, edgeType)
	r.graph.AddDeprecationWarning(pkg, manifest.Deprecated)
	r.mu.Unlock()

	childDeps := map[string]string{}
	if err := mergeDeps(childDeps, manifest.Dependencies); err != nil {
		return nil, err
	}
	bundled := bundledDependencyNames(manifest)
	childDeps = filterBundledDependencies(childDeps, bundled)
	if r.Options.IncludeOptional {
		optionalDeps := filterBundledDependencies(manifest.OptionalDependencies, bundled)
		if err := r.resolveDeps(ctx, node, optionalDeps, EdgeOptional); err != nil {
			return nil, fmt.Errorf("%s@%s optional dependency: %w", pkgName, pkgVersion, err)
		}
		for name := range optionalDeps {
			delete(childDeps, name)
		}
	}
	if err := r.resolveDeps(ctx, node, childDeps, EdgeProd); err != nil {
		return nil, fmt.Errorf("%s@%s dependency: %w", pkgName, pkgVersion, err)
	}
	if err := r.resolvePeers(ctx, node, manifest); err != nil {
		return nil, fmt.Errorf("%s@%s peer dependency: %w", pkgName, pkgVersion, err)
	}
	return node, nil
}

func canReuseExistingForSpec(spec string) bool {
	spec = strings.TrimSpace(spec)
	return spec != "" && spec != "latest" && !registryTagLike(spec)
}

func (r *Resolver) overrideSpec(parent *Node, name, spec string) (string, *OverrideRule) {
	if r.Overrides == nil {
		return spec, nil
	}
	if override, rule := r.Overrides.ResolveWithRule(parent, name, spec); override != "" {
		return override, rule
	}
	return spec, nil
}

func (r *Resolver) resolvePeers(ctx context.Context, node *Node, manifest VersionManifest) error {
	if r.Options.LegacyPeerDeps || r.Options.OmitPeer {
		return nil
	}
	for name, spec := range manifest.PeerDependencies {
		spec, _ = r.overrideSpec(node, name, spec)
		optional := manifest.PeerDependenciesMeta[name].Optional
		target, conflict := r.findPeerTarget(node, name, spec)
		if target != nil {
			r.mu.Lock()
			r.graph.AddPeer(node, target, name, spec, optional, true)
			r.mu.Unlock()
			continue
		}
		if optional {
			if existing := r.findExistingSatisfyingNode(name, spec); existing != nil {
				r.mu.Lock()
				r.graph.AddPeer(node, existing, name, spec, optional, true)
				r.mu.Unlock()
				continue
			}
		}
		if conflict != nil {
			if target, ok, err := r.tryResolveCombinedPeerSet(ctx, node, name, spec, conflict); err != nil {
				return err
			} else if ok {
				r.mu.Lock()
				r.graph.AddPeer(node, target, name, spec, optional, true)
				r.mu.Unlock()
				continue
			}
			r.mu.Lock()
			r.graph.AddPeer(node, conflict, name, spec, optional, false)
			if !optional || r.Options.StrictPeerDeps {
				r.graph.AddPeerConflict(node, conflict, name, spec)
			}
			r.mu.Unlock()
			if !optional || r.Options.StrictPeerDeps {
				return &PeerConflictError{
					Package:      node.ID,
					PeerName:     name,
					PeerSpec:     spec,
					FoundVersion: conflict.Version,
				}
			}
			continue
		}
		if optional {
			r.mu.Lock()
			r.graph.AddPeer(node, nil, name, spec, optional, false)
			r.mu.Unlock()
			continue
		}
		placement := node.Parent
		if placement == nil {
			placement = r.graph.Root
		}
		target, err := r.resolveDep(ctx, placement, name, spec, EdgePeer)
		if err != nil {
			return err
		}
		r.mu.Lock()
		r.graph.AddPeer(node, target, name, spec, optional, target != nil)
		r.mu.Unlock()
	}
	return nil
}

func (r *Resolver) findPeerTarget(node *Node, name, spec string) (*Node, *Node) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var conflict *Node
	for cursor := node; cursor != nil; cursor = cursor.Parent {
		if cursor.Name == name {
			if satisfies(cursor.Version, spec) {
				return cursor, nil
			}
			if conflict == nil {
				conflict = cursor
			}
		}
		if edge := cursor.Dependencies[name]; edge != nil && edge.To != nil {
			if satisfies(edge.To.Version, spec) {
				return edge.To, nil
			}
			if conflict == nil {
				conflict = edge.To
			}
		}
	}
	return nil, conflict
}

func (r *Resolver) findExistingSatisfyingNode(name, spec string) *Node {
	r.mu.Lock()
	defer r.mu.Unlock()
	var best *Node
	for _, node := range r.graph.nodes {
		if node == nil || node.ID == "root" || node.Name != name || !satisfies(node.Version, spec) {
			continue
		}
		if best == nil || compareVersion(node.Version, best.Version) > 0 {
			best = node
		}
	}
	return best
}

func (r *Resolver) reconcileOptionalPeers() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, node := range r.graph.nodes {
		if node == nil {
			continue
		}
		for _, peer := range node.Peers {
			if peer == nil || !peer.PeerOptional || peer.Satisfied {
				continue
			}
			target := r.findExistingSatisfyingNodeLocked(peer.Name, peer.Spec)
			if target == nil {
				continue
			}
			peer.To = target
			peer.Satisfied = true
		}
	}
}

func (r *Resolver) findExistingSatisfyingNodeLocked(name, spec string) *Node {
	var best *Node
	for _, node := range r.graph.nodes {
		if node == nil || node.ID == "root" || node.Name != name || !satisfies(node.Version, spec) {
			continue
		}
		if best == nil || compareVersion(node.Version, best.Version) > 0 {
			best = node
		}
	}
	return best
}

func (r *Resolver) tryResolveCombinedPeerSet(ctx context.Context, node *Node, name, spec string, conflict *Node) (*Node, bool, error) {
	placement := node.Parent
	if placement == nil {
		placement = r.graph.Root
	}
	r.mu.Lock()
	placementEdge := placement.Dependencies[name]
	canReplace := placementEdge != nil && placementEdge.Type == EdgePeer && placementEdge.To == conflict
	combined := []string{spec}
	for _, candidate := range r.graph.nodes {
		if candidate == nil || candidate == node {
			continue
		}
		if peer := candidate.Peers[name]; peer != nil && peer.Type == EdgePeer {
			combined = append(combined, peer.Spec)
		}
	}
	r.mu.Unlock()
	if !canReplace || len(combined) < 2 {
		return nil, false, nil
	}

	actualName, _, err := parsePackageSpec(name, spec)
	if err != nil {
		return nil, false, err
	}
	if err := r.acquireFetchSlot(ctx); err != nil {
		return nil, false, err
	}
	pack, err := r.Client.Packument(ctx, actualName)
	r.releaseFetchSlot()
	if err != nil {
		return nil, false, err
	}
	version, ok := pickVersionSatisfyingAll(pack, combined, r.Options)
	if !ok || version == conflict.Version {
		return nil, false, nil
	}

	target, err := r.resolveDep(ctx, placement, name, version, EdgePeer)
	if err != nil {
		return nil, false, err
	}
	if target == nil || target == conflict {
		return target, target != nil, nil
	}
	r.mu.Lock()
	for _, candidate := range r.graph.nodes {
		if peer := candidate.Peers[name]; peer != nil && peer.To == conflict && satisfies(target.Version, peer.Spec) {
			peer.To = target
			peer.Satisfied = true
		}
	}
	r.removeUnreferencedNodeLocked(conflict)
	r.mu.Unlock()
	return target, true, nil
}

func (r *Resolver) removeUnreferencedNodeLocked(node *Node) {
	if node == nil || node.ID == "root" {
		return
	}
	for _, candidate := range r.graph.nodes {
		for _, edge := range candidate.Dependencies {
			if edge != nil && edge.To == node {
				return
			}
		}
		for _, edge := range candidate.Peers {
			if edge != nil && edge.To == node {
				return
			}
		}
	}
	delete(r.graph.nodes, node.ID)
	delete(r.graph.packages, node.Package.Key())
	delete(r.resolved, node.ID)
}

func (r *Resolver) finishResolve(key string, call *resolveCall, node *Node, err error) {
	r.mu.Lock()
	if node != nil && err == nil {
		r.resolved[key] = node
	}
	call.node = node
	call.err = err
	delete(r.inflight, key)
	close(call.done)
	r.mu.Unlock()
}

func (r *Resolver) acquireFetchSlot(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r.fetchSem <- struct{}{}:
		return nil
	}
}

func (r *Resolver) releaseFetchSlot() {
	<-r.fetchSem
}
