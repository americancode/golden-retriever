package npm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
)

func TestResolverRecordsSatisfiedPeerDependency(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"dependencies":{"host":"^1.0.0","plugin":"1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	plugin := findNode(t, graph, "plugin")
	peer := plugin.Peers["host"]
	if peer == nil {
		t.Fatalf("plugin peer host not recorded")
	}
	if !peer.Satisfied || peer.To == nil || peer.To.Name != "host" {
		t.Fatalf("unexpected peer edge: %#v", peer)
	}
	if graph.Root.Dependencies["host"] == nil {
		t.Fatalf("host should be placed at root")
	}
}

func TestResolverAutoPlacesMissingPeerDependency(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"dependencies":{"plugin":"1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Packages()) != 2 {
		t.Fatalf("packages = %#v", graph.Packages())
	}
	plugin := findNode(t, graph, "plugin")
	peer := plugin.Peers["host"]
	if peer == nil || !peer.Satisfied || peer.To == nil || peer.To.Name != "host" {
		t.Fatalf("unexpected peer edge: %#v", peer)
	}
	rootPeer := graph.Root.Dependencies["host"]
	if rootPeer == nil || rootPeer.Type != EdgePeer {
		t.Fatalf("host peer should be placed at root with peer edge, got %#v", rootPeer)
	}
}

func TestResolverUsesRootDependencyToSatisfyPeerResolvedEarlier(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "peer-first-plugin", Spec: "1.0.0", Type: EdgeDev},
		{Name: "vite-peer", Spec: "^5.0.0", Type: EdgeDev},
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin := findNode(t, graph, "peer-first-plugin")
	peer := plugin.Peers["vite-peer"]
	if peer == nil || !peer.Satisfied || peer.To == nil || peer.To.Version != "5.4.0" {
		t.Fatalf("peer should use explicit root dependency version, got %#v", peer)
	}
	if graph.Has("vite-peer@7.0.0") {
		t.Fatalf("resolver should not auto-install newer peer when root request satisfies it: %#v", graph.Packages())
	}
	rootEdge := graph.Root.Dependencies["vite-peer"]
	if rootEdge == nil || rootEdge.Type != EdgeDev || rootEdge.To == nil || rootEdge.To.Version != "5.4.0" {
		t.Fatalf("root vite-peer edge should preserve original dependency type: %#v", rootEdge)
	}
}

func TestResolverUsesAncestorDependencyToSatisfyPeerResolvedEarlier(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "peer-parent", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin := findNode(t, graph, "peer-first-plugin")
	peer := plugin.Peers["vite-peer"]
	if peer == nil || !peer.Satisfied || peer.To == nil || peer.To.Version != "5.4.0" {
		t.Fatalf("peer should use parent dependency version, got %#v", peer)
	}
	if graph.Has("vite-peer@7.0.0") {
		t.Fatalf("resolver should not auto-install newer peer when parent request satisfies it: %#v", graph.Packages())
	}
	parent := findNode(t, graph, "peer-parent")
	parentEdge := parent.Dependencies["vite-peer"]
	if parentEdge == nil || parentEdge.Type != EdgeProd || parentEdge.To == nil || parentEdge.To.Version != "5.4.0" {
		t.Fatalf("parent vite-peer edge should preserve dependency type: %#v", parentEdge)
	}
}

func TestResolverDoesNotReuseSiblingDependencyForNormalPlacement(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{
		"novelty-a": "1.0.0",
		"novelty-c": "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("shared@1.2.0") || !graph.Has("shared@1.3.0") {
		t.Fatalf("npm-compatible placement should keep both sibling-selected versions: %#v", graph.Packages())
	}
	c := findNode(t, graph, "novelty-c")
	edge := c.Dependencies["shared"]
	if edge == nil || edge.To == nil || edge.To.Version != "1.3.0" {
		t.Fatalf("novelty-c should not reuse novelty-a's sibling dependency: %#v", edge)
	}
}

func TestResolverPreferDedupeReusesSiblingDependency(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, PreferDedupe: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{
		"novelty-a": "1.0.0",
		"novelty-c": "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("shared@1.3.0") {
		t.Fatalf("prefer-dedupe should reuse the existing satisfying sibling version: %#v", graph.Packages())
	}
	c := findNode(t, graph, "novelty-c")
	edge := c.Dependencies["shared"]
	if edge == nil || edge.To == nil || edge.To.Version != "1.2.0" {
		t.Fatalf("novelty-c should reuse novelty-a's shared version with prefer-dedupe: %#v", edge)
	}
}

func TestResolverInstallStrategyHoistedReusesSiblingDependency(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, InstallStrategy: "hoisted"}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{
		"novelty-a": "1.0.0",
		"novelty-c": "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("shared@1.3.0") {
		t.Fatalf("hoisted strategy should reuse existing satisfying version: %#v", graph.Packages())
	}
	c := findNode(t, graph, "novelty-c")
	edge := c.Dependencies["shared"]
	if edge == nil || edge.To == nil || edge.To.Version != "1.2.0" {
		t.Fatalf("novelty-c should reuse novelty-a shared version under hoisted strategy: %#v", edge)
	}
}

func TestResolverInstallStrategyShallowUsesRootDependency(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, InstallStrategy: "shallow"}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "shared", Spec: "1.2.0", Type: EdgeProd},
		{Name: "novelty-c", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := findNode(t, graph, "novelty-c")
	edge := c.Dependencies["shared"]
	if edge == nil || edge.To == nil || edge.To.Version != "1.2.0" {
		t.Fatalf("shallow strategy should place transitive dep from root request when available: %#v", edge)
	}
}

func TestResolverInstallStrategyHoistedMatchesDefaultPlacement(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	defaultResolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	defaultGraph, err := defaultResolver.Resolve(context.Background(), map[string]string{
		"novelty-a": "1.0.0",
		"novelty-c": "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	hoistedResolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, InstallStrategy: "hoisted"}}
	hoistedGraph, err := hoistedResolver.Resolve(context.Background(), map[string]string{
		"novelty-a": "1.0.0",
		"novelty-c": "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !equalPackageKeys(defaultGraph.Packages(), hoistedGraph.Packages()) {
		t.Fatalf("hoisted strategy should match default placement package set default=%#v hoisted=%#v", defaultGraph.Packages(), hoistedGraph.Packages())
	}
	c := findNode(t, hoistedGraph, "novelty-c")
	edge := c.Dependencies["shared"]
	if edge == nil || edge.To == nil || edge.To.Version != "1.2.0" {
		t.Fatalf("hoisted strategy should reuse novelty-a transitive placement at shared@1.2.0: %#v", edge)
	}
}

func TestResolverPrefersPlannedRootDependencyVersionForTransitiveRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/parent":
			fmt.Fprintf(w, `{
  "name":"parent",
  "dist-tags":{"latest":"1.0.0"},
  "versions":{
    "1.0.0":{
      "name":"parent",
      "version":"1.0.0",
      "dependencies":{"dep":"^1.0.0"},
      "dist":{"tarball":"%s/parent/-/parent-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/dep":
			fmt.Fprintf(w, `{
  "name":"dep",
  "dist-tags":{"latest":"1.3.0"},
  "versions":{
    "1.2.0":{"name":"dep","version":"1.2.0","dist":{"tarball":"%s/dep/-/dep-1.2.0.tgz"}},
    "1.3.0":{"name":"dep","version":"1.3.0","dist":{"tarball":"%s/dep/-/dep-1.3.0.tgz"}}
  }
}`, serverURL(r), serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "parent", Spec: "1.0.0", Type: EdgeProd},
		{Name: "dep", Spec: "1.2.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("dep@1.3.0") {
		t.Fatalf("transitive range should not outrun planned root dependency: %#v", graph.Packages())
	}
	if !graph.Has("dep@1.2.0") {
		t.Fatalf("expected planned root dependency version to be used: %#v", graph.Packages())
	}
	parent := findNode(t, graph, "parent")
	edge := parent.Dependencies["dep"]
	if edge == nil || edge.To == nil || edge.To.Version != "1.2.0" {
		t.Fatalf("parent dep edge should point to planned root version: %#v", edge)
	}
}

func TestResolverFallsBackWhenPlannedRootDependencyCannotSatisfyTransitiveRange(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/parent":
			fmt.Fprintf(w, `{
  "name":"parent",
  "dist-tags":{"latest":"1.0.0"},
  "versions":{
    "1.0.0":{
      "name":"parent",
      "version":"1.0.0",
      "dependencies":{"dep":"^2.0.0"},
      "dist":{"tarball":"%s/parent/-/parent-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/dep":
			fmt.Fprintf(w, `{
  "name":"dep",
  "dist-tags":{"latest":"2.1.0"},
  "versions":{
    "1.2.0":{"name":"dep","version":"1.2.0","dist":{"tarball":"%s/dep/-/dep-1.2.0.tgz"}},
    "2.1.0":{"name":"dep","version":"2.1.0","dist":{"tarball":"%s/dep/-/dep-2.1.0.tgz"}}
  }
}`, serverURL(r), serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "parent", Spec: "1.0.0", Type: EdgeProd},
		{Name: "dep", Spec: "1.2.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("dep@1.2.0") || !graph.Has("dep@2.1.0") {
		t.Fatalf("resolver should keep explicit root dep and add separate incompatible transitive version: %#v", graph.Packages())
	}
	parent := findNode(t, graph, "parent")
	edge := parent.Dependencies["dep"]
	if edge == nil || edge.To == nil || edge.To.Version != "2.1.0" {
		t.Fatalf("parent dep edge should resolve to satisfying transitive version: %#v", edge)
	}
}

func TestResolverRecordsUnsatisfiedOptionalPeerDependency(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"dependencies":{"optional-plugin":"1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	plugin := findNode(t, graph, "optional-plugin")
	peer := plugin.Peers["host"]
	if peer == nil || peer.Satisfied || !peer.PeerOptional || peer.To != nil {
		t.Fatalf("unexpected optional peer edge: %#v", peer)
	}
	if len(graph.Packages()) != 1 {
		t.Fatalf("optional peer should not be auto-installed: %#v", graph.Packages())
	}
}

func TestResolverRecordsPeerConflictWarningByDefault(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "host", Spec: "2.0.0", Type: EdgeProd},
		{Name: "plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatalf("got %v, want nil in non-strict mode", err)
	}
	if len(graph.PeerConflicts) != 1 {
		t.Fatalf("expected one peer conflict warning, got %#v", graph.PeerConflicts)
	}
	conflict := graph.PeerConflicts[0]
	if conflict.Name != "host" || conflict.Spec != "^1.0.0" || conflict.FoundVersion != "2.0.0" {
		t.Fatalf("unexpected conflict warning: %#v", conflict)
	}
	plugin := findNode(t, graph, "plugin")
	peer := plugin.Peers["host"]
	if peer == nil || peer.Satisfied || peer.To == nil || peer.To.Version != "2.0.0" {
		t.Fatalf("unexpected peer edge: %#v", peer)
	}
}

func TestResolverStrictPeerDepsErrorsOnPeerConflict(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, StrictPeerDeps: true}}
	_, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "host", Spec: "2.0.0", Type: EdgeProd},
		{Name: "plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	var conflict *PeerConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("got %v, want PeerConflictError", err)
	}
	if conflict.PeerName != "host" || conflict.PeerSpec != "^1.0.0" || conflict.FoundVersion != "2.0.0" {
		t.Fatalf("unexpected conflict: %#v", conflict)
	}
}

func TestResolverLegacyPeerDepsIgnoresPeers(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, LegacyPeerDeps: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin := findNode(t, graph, "plugin")
	if len(plugin.Peers) != 0 {
		t.Fatalf("legacy peer deps should not record peers: %#v", plugin.Peers)
	}
	if len(graph.Packages()) != 1 {
		t.Fatalf("legacy peer deps should not auto-install peers: %#v", graph.Packages())
	}
}

func TestResolverOmitPeerSkipsPeerResolution(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, OmitPeer: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin := findNode(t, graph, "plugin")
	if len(plugin.Peers) != 0 {
		t.Fatalf("omit peer should not record peer edges: %#v", plugin.Peers)
	}
	if graph.Has("host@1.2.0") || graph.Has("host@2.0.0") {
		t.Fatalf("omit peer should not auto-resolve peer tarballs: %#v", graph.Packages())
	}
}

func TestResolverOptionalPeerConflictIsNotProblemByDefault(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "host", Spec: "2.0.0", Type: EdgeProd},
		{Name: "optional-plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.PeerConflicts) != 0 {
		t.Fatalf("peer conflicts = %#v", graph.PeerConflicts)
	}
	plugin := findNode(t, graph, "optional-plugin")
	peer := plugin.Peers["host"]
	if peer == nil || peer.Satisfied || !peer.PeerOptional || peer.To == nil || peer.To.Version != "2.0.0" {
		t.Fatalf("unexpected optional peer conflict edge: %#v", peer)
	}
}

func TestResolverStrictPeerDepsErrorsOnOptionalPeerConflict(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, StrictPeerDeps: true}}
	_, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "host", Spec: "2.0.0", Type: EdgeProd},
		{Name: "optional-plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	var conflict *PeerConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("got %v, want PeerConflictError", err)
	}
}

func TestResolverPeerDependenciesMetaOptionalFalseKeepsPeerRequired(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "host", Spec: "2.0.0", Type: EdgeProd},
		{Name: "meta-false-plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatalf("got %v, want nil in non-strict mode", err)
	}
	if len(graph.PeerConflicts) != 1 {
		t.Fatalf("expected one peer conflict warning, got %#v", graph.PeerConflicts)
	}
	plugin := findNode(t, graph, "meta-false-plugin")
	peer := plugin.Peers["host"]
	if peer == nil || peer.PeerOptional || peer.Satisfied || peer.To == nil || peer.To.Version != "2.0.0" {
		t.Fatalf("optional=false should keep required conflicting peer edge, got %#v", peer)
	}
}

func TestResolverPeerDependenciesMetaForUnrelatedPeerDoesNotMakePeerOptional(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "meta-unrelated-plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin := findNode(t, graph, "meta-unrelated-plugin")
	peer := plugin.Peers["host"]
	if peer == nil || peer.PeerOptional || !peer.Satisfied || peer.To == nil || peer.To.Version != "1.2.0" {
		t.Fatalf("unrelated peerDependenciesMeta should not mark required peer optional: %#v", peer)
	}
	if !graph.Has("host@1.2.0") {
		t.Fatalf("required peer should still be installed: %#v", graph.Packages())
	}
}

func TestResolverReconcilesOverlappingPeerRanges(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "wide-peer-plugin", Spec: "1.0.0", Type: EdgeProd},
		{Name: "exact-peer-plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("shared-peer@1.3.0") {
		t.Fatalf("initial broad peer choice should be replaced: %#v", graph.Packages())
	}
	if !graph.Has("shared-peer@1.2.0") {
		t.Fatalf("expected combined peer set to select shared-peer@1.2.0: %#v", graph.Packages())
	}
	wide := findNode(t, graph, "wide-peer-plugin")
	exact := findNode(t, graph, "exact-peer-plugin")
	if wide.Peers["shared-peer"].To.Version != "1.2.0" || exact.Peers["shared-peer"].To.Version != "1.2.0" {
		t.Fatalf("peer edges not reconciled: wide=%#v exact=%#v", wide.Peers["shared-peer"], exact.Peers["shared-peer"])
	}
}

func TestResolverReconcilesThreeWayPeerEntrySet(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "wide-peer-plugin", Spec: "1.0.0", Type: EdgeProd},
		{Name: "mid-peer-plugin", Spec: "1.0.0", Type: EdgeProd},
		{Name: "exact-peer-plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("shared-peer@1.3.0") {
		t.Fatalf("three-way peer set should replace the first broad peer choice: %#v", graph.Packages())
	}
	if !graph.Has("shared-peer@1.2.0") {
		t.Fatalf("expected combined peer set to select shared-peer@1.2.0: %#v", graph.Packages())
	}
	for _, name := range []string{"wide-peer-plugin", "mid-peer-plugin", "exact-peer-plugin"} {
		plugin := findNode(t, graph, name)
		peer := plugin.Peers["shared-peer"]
		if peer == nil || !peer.Satisfied || peer.To == nil || peer.To.Version != "1.2.0" {
			t.Fatalf("%s peer not reconciled: %#v", name, peer)
		}
	}
}

func TestResolverDoesNotReconcileDisjointPeerRanges(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "wide-peer-plugin", Spec: "1.0.0", Type: EdgeProd},
		{Name: "disjoint-peer-plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatalf("got %v, want nil in non-strict mode", err)
	}
	if len(graph.PeerConflicts) != 1 {
		t.Fatalf("expected one peer conflict warning, got %#v", graph.PeerConflicts)
	}
	conflict := graph.PeerConflicts[0]
	if conflict.Name != "shared-peer" || conflict.Spec != "^2.0.0" || conflict.FoundVersion != "1.3.0" {
		t.Fatalf("unexpected conflict warning: %#v", conflict)
	}
}

func TestResolverStrictPeerDepsErrorsOnDisjointPeerRanges(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, StrictPeerDeps: true}}
	_, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "wide-peer-plugin", Spec: "1.0.0", Type: EdgeProd},
		{Name: "disjoint-peer-plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	var conflict *PeerConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("got %v, want PeerConflictError", err)
	}
	if conflict.PeerName != "shared-peer" || conflict.PeerSpec != "^2.0.0" || conflict.FoundVersion != "1.3.0" {
		t.Fatalf("unexpected conflict: %#v", conflict)
	}
}

func TestResolverResolvesNestedPeerFromAncestorDependency(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "nested-peer-parent", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("nested-peer-parent@1.0.0") || !graph.Has("nested-peer-child@1.0.0") || !graph.Has("host@1.2.0") {
		t.Fatalf("nested peer tree should resolve parent, child, and host: %#v", graph.Packages())
	}
	child := findNode(t, graph, "nested-peer-child")
	peer := child.Peers["host"]
	if peer == nil || !peer.Satisfied || peer.To == nil || peer.To.Version != "1.2.0" {
		t.Fatalf("nested peer should be satisfied by ancestor dependency host@1.2.0: %#v", peer)
	}
}

func TestResolverErrorsWhenRequiredPeerCannotBeResolved(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	_, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "missing-peer-plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	if err == nil {
		t.Fatalf("expected error when required peer package is unresolvable")
	}
}

func TestResolverRecordsMultiplePeerSetConflictWarnings(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "host", Spec: "2.0.0", Type: EdgeProd},
		{Name: "plugin", Spec: "1.0.0", Type: EdgeProd},
		{Name: "host-three-plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatalf("got %v, want nil in non-strict mode", err)
	}
	if len(graph.PeerConflicts) != 2 {
		t.Fatalf("expected two peer conflict warnings, got %#v", graph.PeerConflicts)
	}
}

func TestResolverHandlesCyclicPeerDependencies(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "cyclic-a", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("cyclic-a@1.0.0") || !graph.Has("cyclic-b@1.0.0") {
		t.Fatalf("cyclic peer set should resolve both packages: %#v", graph.Packages())
	}
	cyclicA := findNode(t, graph, "cyclic-a")
	cyclicB := findNode(t, graph, "cyclic-b")
	if peer := cyclicA.Peers["cyclic-b"]; peer == nil || !peer.Satisfied || peer.To != cyclicB {
		t.Fatalf("cyclic-a peer not satisfied by cyclic-b: %#v", peer)
	}
	if peer := cyclicB.Peers["cyclic-a"]; peer == nil || !peer.Satisfied || peer.To != cyclicA {
		t.Fatalf("cyclic-b peer not satisfied by cyclic-a: %#v", peer)
	}
}

func TestResolverOptionalPeerPrefersExistingSatisfyingNode(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "shared-peer", Spec: "1.3.0", Type: EdgeProd},
		{Name: "peer-provider", Spec: "1.0.0", Type: EdgeProd},
		{Name: "optional-existing-peer-plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin := findNode(t, graph, "optional-existing-peer-plugin")
	peer := plugin.Peers["shared-peer"]
	if peer == nil || !peer.Satisfied || !peer.PeerOptional || peer.To == nil || peer.To.Version != "1.2.0" {
		t.Fatalf("optional peer should use existing satisfying node, got %#v", peer)
	}
	if len(graph.PeerConflicts) != 0 {
		t.Fatalf("optional peer existing-node preference should not record conflicts: %#v", graph.PeerConflicts)
	}
}

func TestResolverOptionalPeerPrefersHighestExistingSatisfyingNodeWithoutNewInstall(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "shared-peer", Spec: "1.2.0", Type: EdgeProd},
		{Name: "optional-existing-range-peer-plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin := findNode(t, graph, "optional-existing-range-peer-plugin")
	peer := plugin.Peers["shared-peer"]
	if peer == nil || !peer.Satisfied || !peer.PeerOptional || peer.To == nil || peer.To.Version != "1.2.0" {
		t.Fatalf("optional peer should bind to existing satisfying shared-peer@1.2.0, got %#v", peer)
	}
	if graph.Has("shared-peer@1.3.0") {
		t.Fatalf("optional existing-node preference should not introduce newer shared-peer@1.3.0: %#v", graph.Packages())
	}
}

func TestResolverOptionalPeerUpperBoundPrefersCompatibleExistingNode(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "shared-peer", Spec: "1.3.0", Type: EdgeProd},
		{Name: "peer-provider", Spec: "1.0.0", Type: EdgeProd},
		{Name: "optional-existing-upper-bound-peer-plugin", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin := findNode(t, graph, "optional-existing-upper-bound-peer-plugin")
	peer := plugin.Peers["shared-peer"]
	if peer == nil || !peer.Satisfied || !peer.PeerOptional || peer.To == nil || peer.To.Version != "1.2.0" {
		t.Fatalf("optional peer with upper bound should bind to existing compatible shared-peer@1.2.0, got %#v", peer)
	}
	if len(graph.PeerConflicts) != 0 {
		t.Fatalf("optional existing-node preference should not record conflicts: %#v", graph.PeerConflicts)
	}
}

func TestResolverReconcilesPreviouslyMissingOptionalPeer(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "missing-then-satisfied-optional-peer", Spec: "1.0.0", Type: EdgeProd},
		{Name: "peer-provider", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin := findNode(t, graph, "missing-then-satisfied-optional-peer")
	peer := plugin.Peers["shared-peer"]
	if peer == nil || !peer.Satisfied || !peer.PeerOptional || peer.To == nil || peer.To.Version != "1.2.0" {
		t.Fatalf("optional peer should be reconciled after satisfying node appears, got %#v", peer)
	}
}

func TestResolverReconcilesOptionalPeerAfterInitialConflictWhenSatisfyingNodeAppears(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "host", Spec: "2.0.0", Type: EdgeProd},
		{Name: "optional-conflict-then-satisfied-peer", Spec: "1.0.0", Type: EdgeProd},
		{Name: "host-provider", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	plugin := findNode(t, graph, "optional-conflict-then-satisfied-peer")
	peer := plugin.Peers["host"]
	if peer == nil || !peer.PeerOptional || !peer.Satisfied || peer.To == nil || peer.To.Version != "1.2.0" {
		t.Fatalf("optional peer should be re-resolved to satisfying host@1.2.0 after initial conflict, got %#v", peer)
	}
	if len(graph.PeerConflicts) != 0 {
		t.Fatalf("optional peer re-resolution should not retain peer conflicts: %#v", graph.PeerConflicts)
	}
	if !graph.Has("host@2.0.0") || !graph.Has("host@1.2.0") {
		t.Fatalf("fixture should keep both host versions to validate re-resolution, got %#v", graph.Packages())
	}
}

func TestResolverSkipsBundledDependencies(t *testing.T) {
	srv := bundleRegistry(t, `"bundleDependencies": ["bundled"]`)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("bundled@1.0.0") {
		t.Fatalf("bundled dependency should not be resolved as a separate tarball: %#v", graph.Packages())
	}
	if !graph.Has("loose@1.0.0") {
		t.Fatalf("non-bundled dependency should still be resolved: %#v", graph.Packages())
	}
}

func TestResolverSkipsBundledDependenciesAliasField(t *testing.T) {
	srv := bundleRegistry(t, `"bundledDependencies": ["bundled"]`)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("bundled@1.0.0") {
		t.Fatalf("bundled dependency should not be resolved as a separate tarball: %#v", graph.Packages())
	}
	if !graph.Has("loose@1.0.0") {
		t.Fatalf("legacy bundledDependencies fixture should keep non-bundled dependency tarballs: %#v", graph.Packages())
	}
}

func TestResolverSkipsAllDependenciesWhenBundleDependenciesIsTrue(t *testing.T) {
	srv := bundleRegistry(t, `"bundleDependencies": true`)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("bundled@1.0.0") || graph.Has("loose@1.0.0") {
		t.Fatalf("bundleDependencies true should skip child dependency tarballs: %#v", graph.Packages())
	}
}

func TestResolverBundleMetadataPrefersBundleDependenciesOverBundledDependencies(t *testing.T) {
	srv := bundleRegistryFullMetadata(t, `"bundleDependencies": ["bundled"], "bundledDependencies": ["alt-bundled"]`)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("bundled@1.0.0") {
		t.Fatalf("bundleDependencies should take precedence and skip bundled: %#v", graph.Packages())
	}
	if !graph.Has("alt-bundled@1.0.0") || !graph.Has("loose@1.0.0") {
		t.Fatalf("non-bundled deps should still resolve when only bundleDependencies names are skipped: %#v", graph.Packages())
	}
}

func TestResolverBundleMetadataUsesBundledDependenciesWhenBundleDependenciesFalse(t *testing.T) {
	srv := bundleRegistryFullMetadata(t, `"bundleDependencies": false, "bundledDependencies": ["alt-bundled"]`)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("alt-bundled@1.0.0") {
		t.Fatalf("bundledDependencies should be applied when bundleDependencies is false: %#v", graph.Packages())
	}
	if !graph.Has("bundled@1.0.0") || !graph.Has("loose@1.0.0") {
		t.Fatalf("other dependencies should still resolve: %#v", graph.Packages())
	}
}

func TestResolverLegacyBundledDependenciesTrueSkipsAllDependencies(t *testing.T) {
	srv := bundleRegistryFullMetadata(t, `"bundledDependencies": true`)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("bundled@1.0.0") || graph.Has("alt-bundled@1.0.0") || graph.Has("loose@1.0.0") {
		t.Fatalf("legacy bundledDependencies=true should skip all child tarballs: %#v", graph.Packages())
	}
}

func TestResolverLegacyBundledDependenciesFallbackWhenBundleDependenciesEmpty(t *testing.T) {
	srv := bundleRegistryFullMetadata(t, `"bundleDependencies": [], "bundledDependencies": ["alt-bundled"]`)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("alt-bundled@1.0.0") {
		t.Fatalf("bundledDependencies should apply when bundleDependencies is empty: %#v", graph.Packages())
	}
	if !graph.Has("bundled@1.0.0") || !graph.Has("loose@1.0.0") {
		t.Fatalf("non-bundled dependencies should still resolve: %#v", graph.Packages())
	}
}

func TestResolverLegacyBundledDependenciesFallbackWhenBundleDependenciesNull(t *testing.T) {
	srv := bundleRegistryFullMetadata(t, `"bundleDependencies": null, "bundledDependencies": ["alt-bundled"]`)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("alt-bundled@1.0.0") {
		t.Fatalf("bundledDependencies should apply when bundleDependencies is null: %#v", graph.Packages())
	}
	if !graph.Has("bundled@1.0.0") || !graph.Has("loose@1.0.0") {
		t.Fatalf("non-bundled dependencies should still resolve: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONRootBundleDependenciesDoesNotSuppressRootDependencyResolution(t *testing.T) {
	srv := bundleRegistry(t, `"bundleDependencies": ["bundled"]`)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "name": "root-bundler",
  "version": "1.0.0",
  "dependencies": {"bundled": "1.0.0", "loose": "1.0.0"},
  "bundleDependencies": ["bundled"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("bundled@1.0.0") || !graph.Has("loose@1.0.0") {
		t.Fatalf("root bundleDependencies metadata should not suppress dependency resolution: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONRootBundleDependenciesTrueDoesNotSuppressRootDependencyResolution(t *testing.T) {
	srv := bundleRegistry(t, `"bundleDependencies": true`)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "name": "root-bundler",
  "version": "1.0.0",
  "dependencies": {"bundled": "1.0.0", "loose": "1.0.0"},
  "bundleDependencies": true
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("bundled@1.0.0") || !graph.Has("loose@1.0.0") {
		t.Fatalf("root bundleDependencies=true should not suppress dependency resolution: %#v", graph.Packages())
	}
}

func TestResolverOptionalDependenciesOverrideDependencies(t *testing.T) {
	srv := optionalOverrideRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("shared@1.0.0") {
		t.Fatalf("dependency entry should be overridden by optionalDependencies: %#v", graph.Packages())
	}
	if !graph.Has("shared@2.0.0") {
		t.Fatalf("optional dependency override should be resolved: %#v", graph.Packages())
	}
	parent := findNode(t, graph, "parent")
	edge := parent.Dependencies["shared"]
	if edge == nil || edge.Type != EdgeOptional || edge.To == nil || edge.To.Version != "2.0.0" {
		t.Fatalf("unexpected shared edge: %#v", edge)
	}
}

func TestResolverIncludesIncompatibleOptionalPlatformDependencyForLockParity(t *testing.T) {
	srv := platformRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("incompatible@1.0.0") {
		t.Fatalf("incompatible optional platform dependency should be mirrored for lock parity: %#v", graph.Packages())
	}
	if !graph.Has("compatible@1.0.0") {
		t.Fatalf("compatible optional dependency should be resolved: %#v", graph.Packages())
	}
	parent := findNode(t, graph, "parent")
	edge := parent.Dependencies["incompatible"]
	if edge == nil || edge.Type != EdgeOptional || edge.To == nil {
		t.Fatalf("incompatible optional platform dependency should keep an optional edge: %#v", edge)
	}
}

func TestResolverAcceptsAnyPlatformRule(t *testing.T) {
	srv := platformRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, Libc: "glibc"}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"any-platform": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("any-platform@1.0.0") {
		t.Fatalf("any-platform should resolve: %#v", graph.Packages())
	}
}

func TestResolverErrorsOnIncompatibleProdDependency(t *testing.T) {
	srv := platformRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	_, err := resolver.Resolve(context.Background(), map[string]string{"incompatible": "1.0.0"})
	var platformErr *PackagePlatformError
	if !errors.As(err, &platformErr) {
		t.Fatalf("got %v, want PackagePlatformError", err)
	}
}

func TestResolverAppliesLibcFilter(t *testing.T) {
	srv := platformRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, Libc: "glibc"}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"libc-compatible": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("libc-compatible@1.0.0") {
		t.Fatalf("libc-compatible should resolve: %#v", graph.Packages())
	}
}

func TestResolverErrorsOnIncompatibleLibcProdDependency(t *testing.T) {
	srv := platformRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, Libc: "glibc"}}
	_, err := resolver.Resolve(context.Background(), map[string]string{"libc-incompatible": "1.0.0"})
	var platformErr *PackagePlatformError
	if !errors.As(err, &platformErr) {
		t.Fatalf("got %v, want PackagePlatformError", err)
	}
	if platformErr.Field != "libc" {
		t.Fatalf("field = %s want libc", platformErr.Field)
	}
}

func TestResolverIncludesIncompatibleOptionalLibcDependencyForLockParity(t *testing.T) {
	srv := platformRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, Libc: "glibc"}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"libc-parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("libc-incompatible@1.0.0") {
		t.Fatalf("incompatible optional libc dependency should be mirrored for lock parity: %#v", graph.Packages())
	}
	if !graph.Has("libc-compatible@1.0.0") {
		t.Fatalf("compatible optional libc dependency should resolve: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONSkipsOmittedDevPlatformMismatch(t *testing.T) {
	srv := platformRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"devDependencies":{"incompatible":"1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("incompatible@1.0.0") {
		t.Fatalf("omitted dev dependency should not resolve: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONSkipsOmittedOptionalPlatformMismatch(t *testing.T) {
	srv := platformRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"optionalDependencies":{"incompatible":"1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("incompatible@1.0.0") {
		t.Fatalf("omitted optional dependency should not resolve: %#v", graph.Packages())
	}
}

func TestResolverSkipsOmittedPeerPlatformMismatch(t *testing.T) {
	srv := platformRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, OmitPeer: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"platform-peer-plugin": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("incompatible@1.0.0") {
		t.Fatalf("omitted peer dependency should not resolve: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONCombinedOmitDevOptionalPeer(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"plugin": "1.0.0"},
  "devDependencies": {"host": "1.2.0"},
  "optionalDependencies": {"optional-plugin": "1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{
		IncludeDev:      false,
		IncludeOptional: false,
		OmitPeer:        true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("plugin@1.0.0") {
		t.Fatalf("prod dependency should resolve: %#v", graph.Packages())
	}
	if graph.Has("host@1.2.0") || graph.Has("optional-plugin@1.0.0") {
		t.Fatalf("dev and optional dependencies should be omitted: %#v", graph.Packages())
	}
	plugin := findNode(t, graph, "plugin")
	if len(plugin.Peers) != 0 {
		t.Fatalf("peer dependencies should be omitted: %#v", plugin.Peers)
	}
}

func TestClassifyLibcOutput(t *testing.T) {
	tests := map[string]string{
		"musl libc (x86_64)":                          "musl",
		"ldd (Ubuntu GLIBC 2.39-0ubuntu8.4) 2.39":     "glibc",
		"Copyright (C) 2024 Free Software Foundation": "glibc",
		"not a libc marker":                           "",
	}
	for input, want := range tests {
		if got := classifyLibcOutput(input); got != want {
			t.Fatalf("classifyLibcOutput(%q) = %q want %q", input, got, want)
		}
	}
}

func TestEffectiveLibcHonorsExplicitValue(t *testing.T) {
	if got := effectiveLibc("linux", "musl"); got != "musl" {
		t.Fatalf("explicit libc = %q want musl", got)
	}
	if got := effectiveLibc("darwin", ""); got != "" {
		t.Fatalf("non-linux auto libc = %q want empty", got)
	}
}

func TestResolverErrorsOnEngineMismatchWhenStrict(t *testing.T) {
	srv := engineRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{
		EngineStrict: true,
		NodeVersion:  "12.18.4",
	}}
	_, err := resolver.Resolve(context.Background(), map[string]string{"engine-package": "1.0.0"})
	var engineErr *PackageEngineError
	if !errors.As(err, &engineErr) {
		t.Fatalf("got %v, want PackageEngineError", err)
	}
	if engineErr.Wanted != ">=20" || engineErr.Current != "12.18.4" {
		t.Fatalf("unexpected engine error: %#v", engineErr)
	}
}

func TestResolverAllowsEngineMismatchWhenNotStrict(t *testing.T) {
	srv := engineRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{
		EngineStrict: false,
		NodeVersion:  "12.18.4",
	}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"engine-package": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("engine-package@1.0.0") {
		t.Fatalf("non-strict engine mismatch should still resolve: %#v", graph.Packages())
	}
	if len(graph.EngineWarnings) != 1 {
		t.Fatalf("expected one non-strict engine warning, got %#v", graph.EngineWarnings)
	}
	warning := graph.EngineWarnings[0]
	if warning.Package != "engine-package@1.0.0" || warning.Engine != "node" || warning.Wanted != ">=20" || warning.Current != "12.18.4" {
		t.Fatalf("unexpected engine warning: %#v", warning)
	}
}

func TestResolverRollsBackOptionalEngineWarnings(t *testing.T) {
	srv := engineRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{
		IncludeOptional: true,
		NodeVersion:     "12.18.4",
	}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"engine-optional-failing-parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("engine-package@1.0.0") || graph.Has("missing-engine-meta@1.0.0") {
		t.Fatalf("failed optional subtree should be rolled back: %#v", graph.Packages())
	}
	if len(graph.EngineWarnings) != 0 {
		t.Fatalf("engine warnings from rolled-back optional subtree should not remain: %#v", graph.EngineWarnings)
	}
}

func TestResolverPrefersEngineCompatibleRangeVersion(t *testing.T) {
	srv := engineRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{NodeVersion: "12.18.4"}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"engine-range-package": "^1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("engine-range-package@1.1.0") {
		t.Fatalf("expected engine-compatible version, got %#v", graph.Packages())
	}
	if graph.Has("engine-range-package@1.2.0") {
		t.Fatalf("engine-incompatible latest should not be selected: %#v", graph.Packages())
	}
}

func TestResolverRecordsDeprecationWarningForSelectedPackage(t *testing.T) {
	srv := deprecationRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"deprecated-package": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("deprecated-package@1.0.0") {
		t.Fatalf("deprecated package should still resolve when explicitly selected: %#v", graph.Packages())
	}
	if len(graph.DeprecationWarnings) != 1 {
		t.Fatalf("expected one deprecation warning, got %#v", graph.DeprecationWarnings)
	}
	warning := graph.DeprecationWarnings[0]
	if warning.Package != "deprecated-package@1.0.0" || warning.Message != "use maintained-package instead" {
		t.Fatalf("unexpected deprecation warning: %#v", warning)
	}
}

func TestResolverAvoidsDeprecatedRangeVersionWhenPossible(t *testing.T) {
	srv := deprecationRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"deprecated-range-package": "^1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("deprecated-range-package@1.1.0") {
		t.Fatalf("deprecated range candidate should be avoided when a non-deprecated version satisfies: %#v", graph.Packages())
	}
	if !graph.Has("deprecated-range-package@1.0.0") {
		t.Fatalf("expected non-deprecated fallback: %#v", graph.Packages())
	}
	if len(graph.DeprecationWarnings) != 0 {
		t.Fatalf("non-deprecated fallback should not warn: %#v", graph.DeprecationWarnings)
	}
}

func TestResolverSkipsOptionalEngineMismatch(t *testing.T) {
	srv := engineRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{
		IncludeOptional: true,
		EngineStrict:    true,
		NodeVersion:     "12.18.4",
	}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"engine-parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("engine-package@1.0.0") {
		t.Fatalf("optional engine mismatch should be skipped: %#v", graph.Packages())
	}
	if !graph.Has("engine-parent@1.0.0") {
		t.Fatalf("parent should still resolve: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONSkipsOmittedDevEngineMismatch(t *testing.T) {
	srv := engineRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"devDependencies":{"engine-package":"1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{
		EngineStrict: true,
		NodeVersion:  "12.18.4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("engine-package@1.0.0") {
		t.Fatalf("omitted dev dependency should not resolve: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONFailsIncludedDevEngineMismatch(t *testing.T) {
	srv := engineRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"devDependencies":{"engine-package":"1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{
		IncludeDev:   true,
		EngineStrict: true,
		NodeVersion:  "12.18.4",
	})
	var engineErr *PackageEngineError
	if !errors.As(err, &engineErr) {
		t.Fatalf("got %v, want PackageEngineError", err)
	}
}

func TestResolvePackageJSONSkipsOmittedOptionalEngineMismatch(t *testing.T) {
	srv := engineRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"optionalDependencies":{"engine-package":"1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{
		EngineStrict: true,
		NodeVersion:  "12.18.4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("engine-package@1.0.0") {
		t.Fatalf("omitted optional dependency should not resolve: %#v", graph.Packages())
	}
}

func TestResolverSkipsOmittedPeerEngineMismatch(t *testing.T) {
	srv := engineRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{
		IncludeOptional: true,
		OmitPeer:        true,
		EngineStrict:    true,
		NodeVersion:     "12.18.4",
	}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"engine-peer-plugin": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("engine-package@1.0.0") {
		t.Fatalf("omitted peer dependency should not resolve: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONEngineOmitIncludeInteractions(t *testing.T) {
	srv := engineRegistry(t)
	defer srv.Close()

	t.Run("omit dev optional and peer avoids engine failure", func(t *testing.T) {
		dir := t.TempDir()
		input := filepath.Join(dir, "package.json")
		if err := os.WriteFile(input, []byte(`{
  "dependencies":{"engine-peer-plugin":"1.0.0"},
  "devDependencies":{"engine-package":"1.0.0"},
  "optionalDependencies":{"engine-package":"1.0.0"}
}`), 0o644); err != nil {
			t.Fatal(err)
		}
		graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{
			IncludeDev:      false,
			IncludeOptional: false,
			OmitPeer:        true,
			EngineStrict:    true,
			NodeVersion:     "12.18.4",
		})
		if err != nil {
			t.Fatal(err)
		}
		if !graph.Has("engine-peer-plugin@1.0.0") {
			t.Fatalf("prod dependency should resolve: %#v", graph.Packages())
		}
		if graph.Has("engine-package@1.0.0") {
			t.Fatalf("omitted dependency classes should not resolve engine-package: %#v", graph.Packages())
		}
	})

	t.Run("include dev triggers strict engine failure", func(t *testing.T) {
		dir := t.TempDir()
		input := filepath.Join(dir, "package.json")
		if err := os.WriteFile(input, []byte(`{"devDependencies":{"engine-package":"1.0.0"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{
			IncludeDev:   true,
			EngineStrict: true,
			NodeVersion:  "12.18.4",
		})
		var engineErr *PackageEngineError
		if !errors.As(err, &engineErr) {
			t.Fatalf("got %v, want PackageEngineError", err)
		}
	})

	t.Run("include optional keeps strict mode but skips optional engine mismatch", func(t *testing.T) {
		dir := t.TempDir()
		input := filepath.Join(dir, "package.json")
		if err := os.WriteFile(input, []byte(`{"optionalDependencies":{"engine-package":"1.0.0"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{
			IncludeOptional: true,
			EngineStrict:    true,
			NodeVersion:     "12.18.4",
		})
		if err != nil {
			t.Fatal(err)
		}
		if graph.Has("engine-package@1.0.0") {
			t.Fatalf("optional engine mismatch should be skipped: %#v", graph.Packages())
		}
	})

	t.Run("include peer triggers strict engine failure", func(t *testing.T) {
		dir := t.TempDir()
		input := filepath.Join(dir, "package.json")
		if err := os.WriteFile(input, []byte(`{"dependencies":{"engine-peer-plugin":"1.0.0"}}`), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{
			IncludeOptional: true,
			OmitPeer:        false,
			EngineStrict:    true,
			NodeVersion:     "12.18.4",
		})
		var engineErr *PackageEngineError
		if !errors.As(err, &engineErr) {
			t.Fatalf("got %v, want PackageEngineError", err)
		}
	})
}

func TestResolverSkipsMissingOptionalDependency(t *testing.T) {
	srv := optionalFailureRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"optional-root": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("missing-optional@1.0.0") {
		t.Fatalf("missing optional dependency should be skipped: %#v", graph.Packages())
	}
	if !graph.Has("optional-root@1.0.0") {
		t.Fatalf("root package should still resolve: %#v", graph.Packages())
	}
}

func TestResolverErrorsOnMissingProdDependency(t *testing.T) {
	srv := optionalFailureRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	_, err := resolver.Resolve(context.Background(), map[string]string{"prod-root": "1.0.0"})
	if err == nil {
		t.Fatalf("missing prod dependency should fail")
	}
}

func TestResolverRollsBackOptionalMetadependencyFailure(t *testing.T) {
	srv := optionalFailureRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"optional-meta-root": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("optional-wrapper@1.0.0") || graph.Has("missing-meta@1.0.0") {
		t.Fatalf("failed optional subtree should be rolled back: %#v", graph.Packages())
	}
	if !graph.Has("optional-meta-root@1.0.0") {
		t.Fatalf("root package should still resolve: %#v", graph.Packages())
	}
}

func TestResolverOptionalFailurePreservesSharedRequiredDependency(t *testing.T) {
	srv := optionalFailureRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "shared-required", Spec: "1.0.0", Type: EdgeProd},
		{Name: "optional-shared-root", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("shared-required@1.0.0") || !graph.Has("optional-shared-root@1.0.0") {
		t.Fatalf("required packages should remain after optional subtree failure: %#v", graph.Packages())
	}
	if graph.Has("optional-shared-wrapper@1.0.0") || graph.Has("missing-meta@1.0.0") {
		t.Fatalf("failed optional subtree should be removed: %#v", graph.Packages())
	}
	root := findNode(t, graph, "optional-shared-root")
	if edge := root.Dependencies["optional-shared-wrapper"]; edge != nil {
		t.Fatalf("failed optional edge should not remain attached to root: %#v", edge)
	}
}

func TestResolverOptionalFailurePrunesUnreferencedSharedOptionalSubtree(t *testing.T) {
	srv := optionalFailureRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"optional-shared-unreferenced-root": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("optional-shared-unreferenced-root@1.0.0") {
		t.Fatalf("root package should resolve: %#v", graph.Packages())
	}
	if graph.Has("optional-shared-wrapper-unreferenced@1.0.0") || graph.Has("shared-only-optional@1.0.0") || graph.Has("missing-meta@1.0.0") {
		t.Fatalf("failed optional set should be fully pruned when unreferenced elsewhere: %#v", graph.Packages())
	}
}

func TestResolverOptionalFailureKeepsSharedNodeReferencedBySuccessfulOptionalSet(t *testing.T) {
	srv := optionalFailureRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"optional-shared-dual-root": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("optional-shared-dual-root@1.0.0") || !graph.Has("optional-good-wrapper@1.0.0") || !graph.Has("shared-only-optional@1.0.0") {
		t.Fatalf("successful optional set should remain resolved: %#v", graph.Packages())
	}
	if graph.Has("optional-bad-wrapper@1.0.0") || graph.Has("missing-meta@1.0.0") {
		t.Fatalf("failed optional set should be pruned: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONSupportsRootFileSpecLocalDir(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	localDir := filepath.Join(dir, "local")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "package.json"), []byte(`{
  "name":"local",
  "version":"1.0.0",
  "dependencies":{"consumer":"1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"dependencies":{"local":"file:./local"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("local@1.0.0") {
		t.Fatalf("file dependency should stay local and not fetch local package tarball: %#v", graph.Packages())
	}
	if !graph.Has("consumer@1.0.0") {
		t.Fatalf("local file dependency transitive registry deps should resolve: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONSupportsRootLinkSpecLocalDir(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	localDir := filepath.Join(dir, "local")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "package.json"), []byte(`{
  "name":"local",
  "version":"1.0.0",
  "dependencies":{"consumer":"1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"dependencies":{"local":"link:./local"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("local@1.0.0") {
		t.Fatalf("link dependency should stay local and not fetch local package tarball: %#v", graph.Packages())
	}
	if !graph.Has("consumer@1.0.0") {
		t.Fatalf("local link dependency transitive registry deps should resolve: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONErrorsOnInvalidPackageName(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"dependencies":{"bad space":"^1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ResolvePackageJSON(context.Background(), NewClient("https://example.test"), input, ResolveOptions{IncludeOptional: true})
	var nameErr *InvalidPackageNameError
	if !errors.As(err, &nameErr) {
		t.Fatalf("got %v, want InvalidPackageNameError", err)
	}
	if nameErr.Name != "bad space" || nameErr.Spec != "^1.0.0" {
		t.Fatalf("unexpected name error: %#v", nameErr)
	}
}

func TestResolvePackageJSONErrorsOnInvalidPackageNameForms(t *testing.T) {
	tests := []string{
		"node_modules",
		"favicon.ico",
		"@scope",
		"@scope/",
		"@scope/.hidden",
		"_private",
		"-dash",
		".dot",
	}
	for _, depName := range tests {
		t.Run(depName, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "package.json")
			if err := os.WriteFile(input, []byte(fmt.Sprintf(`{"dependencies":{%q:"^1.0.0"}}`, depName)), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := ResolvePackageJSON(context.Background(), NewClient("https://example.test"), input, ResolveOptions{IncludeOptional: true})
			var nameErr *InvalidPackageNameError
			if !errors.As(err, &nameErr) {
				t.Fatalf("got %v, want InvalidPackageNameError", err)
			}
			if nameErr.Name != depName || nameErr.Spec != "^1.0.0" {
				t.Fatalf("unexpected name error: %#v", nameErr)
			}
		})
	}
}

func TestResolvePackageJSONErrorsOnInvalidTagName(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"dependencies":{"left-pad":"bad tag"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ResolvePackageJSON(context.Background(), NewClient("https://example.test"), input, ResolveOptions{IncludeOptional: true})
	var tagErr *InvalidTagNameError
	if !errors.As(err, &tagErr) {
		t.Fatalf("got %v, want InvalidTagNameError", err)
	}
	if tagErr.Name != "left-pad" || tagErr.Spec != "bad tag" {
		t.Fatalf("unexpected tag error: %#v", tagErr)
	}
}

func TestValidateDependencySpecMatchesNPAURLAndTagBoundary(t *testing.T) {
	unsupported := []string{"foo:bar", "git+foo:bar", "ftp://example.test/pkg.tgz"}
	for _, spec := range unsupported {
		t.Run("unsupported/"+spec, func(t *testing.T) {
			err := validateDependencySpec("pkg", spec, EdgeProd)
			var specErr *UnsupportedSpecError
			if !errors.As(err, &specErr) {
				t.Fatalf("got %v, want UnsupportedSpecError", err)
			}
		})
	}

	invalidTags := []string{"foo1:bar", "foo.bar:baz", "foo-bar:baz", "foo+baz:bar"}
	for _, spec := range invalidTags {
		t.Run("tag/"+spec, func(t *testing.T) {
			err := validateDependencySpec("pkg", spec, EdgeProd)
			var tagErr *InvalidTagNameError
			if !errors.As(err, &tagErr) {
				t.Fatalf("got %v, want InvalidTagNameError", err)
			}
		})
	}
}

func TestResolvePackageJSONErrorsOnInvalidAliasTargetName(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"dependencies":{"alias":"npm:bad space@^1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ResolvePackageJSON(context.Background(), NewClient("https://example.test"), input, ResolveOptions{IncludeOptional: true})
	var nameErr *InvalidPackageNameError
	if !errors.As(err, &nameErr) {
		t.Fatalf("got %v, want InvalidPackageNameError", err)
	}
	if nameErr.Name != "bad space" || nameErr.Spec != "npm:bad space@^1.0.0" {
		t.Fatalf("unexpected name error: %#v", nameErr)
	}
}

func TestResolvePackageJSONErrorsOnUnsupportedAliasTargetSpec(t *testing.T) {
	tests := map[string]string{
		"file":   "npm:file:../local",
		"remote": "npm:https://registry.npmjs.org/pkg/-/pkg-1.0.0.tgz",
		"nested": "npm:npm:real@1.0.0",
		"slash":  "npm:foo/bar",
		"scheme": "npm:foo:bar",
	}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "package.json")
			if err := os.WriteFile(input, []byte(fmt.Sprintf(`{"dependencies":{"alias":%q}}`, spec)), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := ResolvePackageJSON(context.Background(), NewClient("https://example.test"), input, ResolveOptions{IncludeOptional: true})
			var specErr *UnsupportedSpecError
			if !errors.As(err, &specErr) {
				t.Fatalf("got %v, want UnsupportedSpecError", err)
			}
			if specErr.Name != "alias" || specErr.Spec != spec || specErr.Type != "prod" {
				t.Fatalf("unexpected spec error: %#v", specErr)
			}
		})
	}
}

func TestResolvePackageJSONErrorsOnUnsupportedRootSpecClasses(t *testing.T) {
	tests := map[string]string{
		"workspace": "workspace:*",
		"link":      "link:../local",
		"scheme":    "foo:bar",
		"directory": "../local",
		"home-dir":  "~/local",
		"windows":   "C:\\local\\pkg",
	}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "package.json")
			if err := os.WriteFile(input, []byte(fmt.Sprintf(`{"dependencies":{"pkg":%q}}`, spec)), 0o644); err != nil {
				t.Fatal(err)
			}
			_, err := ResolvePackageJSON(context.Background(), NewClient("https://example.test"), input, ResolveOptions{IncludeOptional: true})
			var specErr *UnsupportedSpecError
			if !errors.As(err, &specErr) {
				t.Fatalf("got %v, want UnsupportedSpecError", err)
			}
			if specErr.Name != "pkg" || specErr.Spec != spec || specErr.Type != "prod" {
				t.Fatalf("unexpected spec error: %#v", specErr)
			}
		})
	}
}

func TestIsGitLikeSpec(t *testing.T) {
	yes := []string{
		"git+https://github.com/acme/pkg.git",
		"github:acme/pkg",
		"gitlab:acme/pkg",
		"bitbucket:acme/pkg",
		"gist:acme/1234",
		"ssh://git@github.com/acme/pkg.git",
		"svn://svn.example.test/repo",
		"git@github.com:acme/pkg.git",
	}
	for _, spec := range yes {
		if !isGitLikeSpec(spec) {
			t.Fatalf("expected git-like spec: %q", spec)
		}
	}
	no := []string{"^1.0.0", "latest", "file:./local", "https://registry.npmjs.org/pkg/-/pkg-1.0.0.tgz"}
	for _, spec := range no {
		if isGitLikeSpec(spec) {
			t.Fatalf("did not expect git-like spec: %q", spec)
		}
	}
}

func TestResolvePackageJSONSupportsRemoteTarballSpec(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	spec := srv.URL + "/consumer/-/consumer-1.0.0.tgz"
	if err := os.WriteFile(input, []byte(fmt.Sprintf(`{"dependencies":{"consumer":%q}}`, spec)), 0o644); err != nil {
		t.Fatal(err)
	}
	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("consumer@1.0.0") || !graph.Has("shared@1.3.0") {
		t.Fatalf("remote tarball dependency should resolve package and transitive deps: %#v", graph.Packages())
	}
	consumer := findNode(t, graph, "consumer")
	if consumer.Package.Tarball != spec {
		t.Fatalf("consumer tarball=%q want %q", consumer.Package.Tarball, spec)
	}
}

func TestValidateDependencySpecAllowsTildeSemverRanges(t *testing.T) {
	for _, spec := range []string{"~0.4.0", "~1.1.4", "~"} {
		if err := validateDependencySpec("pkg", spec, EdgeProd); err != nil {
			t.Fatalf("validateDependencySpec(%q) = %v, want nil", spec, err)
		}
	}
}

func TestResolvePackageJSONIncludesWorkspaceExternalDependencies(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	workspaceDir := filepath.Join(dir, "packages", "app")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "workspaces":["packages/*"],
  "dependencies":{"app":"workspace:*"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "package.json"), []byte(`{
  "name":"app",
  "version":"1.0.0",
  "dependencies":{"consumer":"1.0.0"},
  "devDependencies":{"consumer-two":"1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeDev: true, IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("app@1.0.0") {
		t.Fatalf("workspace package itself should not be fetched as a registry tarball: %#v", graph.Packages())
	}
	if !graph.Has("consumer@1.0.0") || !graph.Has("consumer-two@1.0.0") {
		t.Fatalf("workspace external dependencies not resolved: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONUsesWorkspaceWhenVersionSatisfied(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	workspaceDir := filepath.Join(dir, "packages", "app")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "workspaces":{"packages":["packages/*"]},
  "dependencies":{"app":"^1.0.0","consumer":"1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "package.json"), []byte(`{
  "name":"app",
  "version":"1.2.0",
  "dependencies":{"consumer-two":"1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeDev: true, IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("app@1.2.0") {
		t.Fatalf("satisfied workspace dependency should not fetch registry app tarball: %#v", graph.Packages())
	}
	if !graph.Has("consumer@1.0.0") || !graph.Has("consumer-two@1.0.0") {
		t.Fatalf("root and workspace external dependencies should resolve: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONUsesVersionedWorkspaceWhenSatisfied(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	workspaceDir := filepath.Join(dir, "packages", "app")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "workspaces":["packages/*"],
  "dependencies":{"app":"workspace:^1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "package.json"), []byte(`{
  "name":"app",
  "version":"1.2.0",
  "dependencies":{"consumer":"1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeDev: true, IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("app@1.2.0") {
		t.Fatalf("satisfied versioned workspace dependency should not fetch registry app tarball: %#v", graph.Packages())
	}
	if !graph.Has("consumer@1.0.0") {
		t.Fatalf("workspace external dependency should resolve: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONUsesWorkspaceProtocolEvenWhenVersionTextUnsatisfied(t *testing.T) {
	dir := t.TempDir()
	workspaceDir := filepath.Join(dir, "packages", "app")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "workspaces":["packages/*"],
  "dependencies":{"app":"workspace:^2.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "package.json"), []byte(`{
  "name":"app",
  "version":"1.2.0"
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient("https://example.test"), input, ResolveOptions{IncludeDev: true, IncludeOptional: true})
	if err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	if graph.Has("app@1.2.0") {
		t.Fatalf("workspace protocol should not fetch workspace package from registry: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONFetchesRegistryWhenWorkspaceVersionUnsatisfied(t *testing.T) {
	srv := workspaceRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	workspaceDir := filepath.Join(dir, "packages", "app")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "workspaces":["packages/*"],
  "dependencies":{"app":"^2.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "package.json"), []byte(`{
  "name":"app",
  "version":"1.0.0"
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeDev: true, IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("app@2.0.0") {
		t.Fatalf("unsatisfied workspace dependency should fetch registry app@2.0.0: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONSupportsRootFileLinkToWorkspace(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	workspaceDir := filepath.Join(dir, "packages", "app")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "workspaces":["packages/*"],
  "dependencies":{"app":"file:packages/app"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "package.json"), []byte(`{
  "name":"app",
  "version":"1.0.0",
  "dependencies":{"consumer":"1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeDev: true, IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("app@1.0.0") {
		t.Fatalf("workspace app should be linked locally, not fetched from registry: %#v", graph.Packages())
	}
	if !graph.Has("consumer@1.0.0") {
		t.Fatalf("workspace external deps should still resolve: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONAppliesOverridesToWorkspaceDependencies(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	workspaceDir := filepath.Join(dir, "packages", "app")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "workspaces":["packages/*"],
  "dependencies":{"app":"workspace:*"},
  "overrides":{"shared":"2.1.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "package.json"), []byte(`{
  "name":"app",
  "version":"1.0.0",
  "dependencies":{"consumer":"1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeDev: true, IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("shared@1.3.0") || !graph.Has("shared@2.1.0") {
		t.Fatalf("workspace dependency override should force shared@2.1.0: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONHandlesConflictingWorkspaceDevDependencies(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	workspaceADir := filepath.Join(dir, "packages", "a")
	workspaceBDir := filepath.Join(dir, "packages", "b")
	if err := os.MkdirAll(workspaceADir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspaceBDir, 0o755); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "workspaces":["packages/*"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceADir, "package.json"), []byte(`{
  "name":"a",
  "version":"1.0.0",
  "devDependencies":{"consumer":"1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceBDir, "package.json"), []byte(`{
  "name":"b",
  "version":"1.0.0",
  "devDependencies":{"consumer-two":"1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeDev: true, IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("shared@1.3.0") || !graph.Has("shared@2.1.0") {
		t.Fatalf("conflicting workspace dev deps should retain both shared versions: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONSupportsWorkspaceSpecificPeerSets(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	workspaceADir := filepath.Join(dir, "packages", "a")
	workspaceBDir := filepath.Join(dir, "packages", "b")
	if err := os.MkdirAll(workspaceADir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(workspaceBDir, 0o755); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "workspaces":["packages/*"]
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceADir, "package.json"), []byte(`{
  "name":"a",
  "version":"1.0.0",
  "peerDependencies":{"host":"^1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceBDir, "package.json"), []byte(`{
  "name":"b",
  "version":"1.0.0",
  "peerDependencies":{"host":"^2.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeDev: true, IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("host@1.2.0") || !graph.Has("host@2.0.0") {
		t.Fatalf("workspace-specific peer sets should resolve both host versions: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONRejectsDuplicateWorkspaceNames(t *testing.T) {
	dir := t.TempDir()
	for _, workspace := range []string{"a", "b"} {
		workspaceDir := filepath.Join(dir, "packages", workspace)
		if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceDir, "package.json"), []byte(`{"name":"dup","version":"1.0.0"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"workspaces":["packages/*"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ResolvePackageJSON(context.Background(), NewClient("https://example.test"), input, ResolveOptions{IncludeOptional: true})
	var dupErr *DuplicateWorkspaceError
	if !errors.As(err, &dupErr) {
		t.Fatalf("got %v, want DuplicateWorkspaceError", err)
	}
	if dupErr.Name != "dup" || dupErr.First == "" || dupErr.Other == "" {
		t.Fatalf("unexpected duplicate workspace error: %#v", dupErr)
	}
}

func TestResolvePackageJSONResolvesWorkspacePeerDependencies(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	workspaceDir := filepath.Join(dir, "packages", "app")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "workspaces":["packages/*"],
  "dependencies":{"app":"workspace:*"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "package.json"), []byte(`{
  "name":"app",
  "version":"1.0.0",
  "peerDependencies":{"host":"^1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeDev: true, IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("host@1.2.0") {
		t.Fatalf("workspace peer dependency should resolve host@1.2.0: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONSkipsOptionalWorkspacePeerDependencies(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	workspaceDir := filepath.Join(dir, "packages", "app")
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "workspaces":["packages/*"],
  "dependencies":{"app":"workspace:*"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspaceDir, "package.json"), []byte(`{
  "name":"app",
  "version":"1.0.0",
  "peerDependencies":{"host":"^1.0.0"},
  "peerDependenciesMeta":{"host":{"optional":true}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeDev: true, IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("host@1.2.0") || graph.Has("host@2.0.0") {
		t.Fatalf("optional workspace peer dependency should be skipped: %#v", graph.Packages())
	}
}

func TestResolvePackageJSONRespectsWorkspaceNegatedGlobPatterns(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	includeDir := filepath.Join(dir, "packages", "include")
	excludeDir := filepath.Join(dir, "packages", "exclude")
	if err := os.MkdirAll(includeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(excludeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "workspaces":["packages/*","!packages/exclude"],
  "dependencies":{"include":"workspace:*"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(includeDir, "package.json"), []byte(`{
  "name":"include",
  "version":"1.0.0",
  "dependencies":{"consumer":"1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(excludeDir, "package.json"), []byte(`{
  "name":"exclude",
  "version":"1.0.0",
  "dependencies":{"consumer-two":"1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeDev: true, IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("consumer@1.0.0") {
		t.Fatalf("included workspace dependency should resolve: %#v", graph.Packages())
	}
	if graph.Has("consumer-two@1.0.0") {
		t.Fatalf("excluded workspace dependency should not resolve: %#v", graph.Packages())
	}
}

func TestResolverErrorsOnUnsupportedProdTransitiveSpec(t *testing.T) {
	srv := unsupportedSpecRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	_, err := resolver.Resolve(context.Background(), map[string]string{"prod-unsupported": "1.0.0"})
	var specErr *UnsupportedSpecError
	if !errors.As(err, &specErr) {
		t.Fatalf("got %v, want UnsupportedSpecError", err)
	}
	if specErr.Name != "local-child" {
		t.Fatalf("unexpected spec error: %#v", specErr)
	}
}

func TestResolverSkipsUnsupportedOptionalTransitiveSpec(t *testing.T) {
	srv := unsupportedSpecRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"optional-unsupported": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("optional-unsupported@1.0.0") {
		t.Fatalf("root should resolve: %#v", graph.Packages())
	}
	if graph.Has("optional-wrapper@1.0.0") {
		t.Fatalf("unsupported optional subtree should be rolled back: %#v", graph.Packages())
	}
}

func TestResolverReusesExistingSatisfyingDependency(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "shared", Spec: "1.2.0", Type: EdgeProd},
		{Name: "consumer", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("shared@1.3.0") {
		t.Fatalf("existing satisfying dependency should be reused instead of fetching newer range match: %#v", graph.Packages())
	}
	if !graph.Has("shared@1.2.0") || !graph.Has("consumer@1.0.0") {
		t.Fatalf("expected shared@1.2.0 and consumer@1.0.0: %#v", graph.Packages())
	}
	consumer := findNode(t, graph, "consumer")
	edge := consumer.Dependencies["shared"]
	if edge == nil || edge.To == nil || edge.To.Version != "1.2.0" {
		t.Fatalf("consumer should point at existing shared@1.2.0 edge: %#v", edge)
	}
}

func TestResolverKeepsIncompatibleVersionsAtMultipleDepths(t *testing.T) {
	srv := dedupeRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "consumer", Spec: "1.0.0", Type: EdgeProd},
		{Name: "consumer-two", Spec: "1.0.0", Type: EdgeProd},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("shared@1.3.0") || !graph.Has("shared@2.1.0") {
		t.Fatalf("incompatible transitive ranges should keep both shared versions: %#v", graph.Packages())
	}
	consumer := findNode(t, graph, "consumer")
	consumerTwo := findNode(t, graph, "consumer-two")
	if edge := consumer.Dependencies["shared"]; edge == nil || edge.To == nil || edge.To.Version != "1.3.0" {
		t.Fatalf("consumer should use shared@1.3.0: %#v", edge)
	}
	if edge := consumerTwo.Dependencies["shared"]; edge == nil || edge.To == nil || edge.To.Version != "2.1.0" {
		t.Fatalf("consumer-two should use shared@2.1.0: %#v", edge)
	}
}

func TestSortedDependencyNamesDeterministic(t *testing.T) {
	got := sortedDependencyNames(map[string]string{
		"zeta":  "1.0.0",
		"alpha": "1.0.0",
		"mid":   "1.0.0",
	})
	want := []string{"alpha", "mid", "zeta"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func peerRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/host":
			fmt.Fprintf(w, `{
  "name": "host",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "1.2.0": {
      "name": "host",
      "version": "1.2.0",
      "dist": {"tarball": "%s/host/-/host-1.2.0.tgz"}
    },
    "2.0.0": {
      "name": "host",
      "version": "2.0.0",
      "dist": {"tarball": "%s/host/-/host-2.0.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r))
		case "/plugin":
			fmt.Fprintf(w, `{
  "name": "plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "plugin",
      "version": "1.0.0",
      "peerDependencies": {"host": "^1.0.0"},
      "dist": {"tarball": "%s/plugin/-/plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-plugin":
			fmt.Fprintf(w, `{
  "name": "optional-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-plugin",
      "version": "1.0.0",
      "peerDependencies": {"host": "^1.0.0"},
      "peerDependenciesMeta": {"host": {"optional": true}},
      "dist": {"tarball": "%s/optional-plugin/-/optional-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/meta-false-plugin":
			fmt.Fprintf(w, `{
  "name": "meta-false-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "meta-false-plugin",
      "version": "1.0.0",
      "peerDependencies": {"host": "^1.0.0"},
      "peerDependenciesMeta": {"host": {"optional": false}},
      "dist": {"tarball": "%s/meta-false-plugin/-/meta-false-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/meta-unrelated-plugin":
			fmt.Fprintf(w, `{
  "name": "meta-unrelated-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "meta-unrelated-plugin",
      "version": "1.0.0",
      "peerDependencies": {"host": "^1.0.0"},
      "peerDependenciesMeta": {"other": {"optional": true}},
      "dist": {"tarball": "%s/meta-unrelated-plugin/-/meta-unrelated-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/nested-peer-parent":
			fmt.Fprintf(w, `{
  "name": "nested-peer-parent",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "nested-peer-parent",
      "version": "1.0.0",
      "dependencies": {"nested-peer-child": "1.0.0", "host": "1.2.0"},
      "dist": {"tarball": "%s/nested-peer-parent/-/nested-peer-parent-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/nested-peer-child":
			fmt.Fprintf(w, `{
  "name": "nested-peer-child",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "nested-peer-child",
      "version": "1.0.0",
      "peerDependencies": {"host": "^1.0.0"},
      "dist": {"tarball": "%s/nested-peer-child/-/nested-peer-child-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/missing-peer-plugin":
			fmt.Fprintf(w, `{
  "name": "missing-peer-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "missing-peer-plugin",
      "version": "1.0.0",
      "peerDependencies": {"missing-host": "^1.0.0"},
      "dist": {"tarball": "%s/missing-peer-plugin/-/missing-peer-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/host-three-plugin":
			fmt.Fprintf(w, `{
  "name": "host-three-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "host-three-plugin",
      "version": "1.0.0",
      "peerDependencies": {"host": "^3.0.0"},
      "dist": {"tarball": "%s/host-three-plugin/-/host-three-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/peer-first-plugin":
			fmt.Fprintf(w, `{
  "name": "peer-first-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "peer-first-plugin",
      "version": "1.0.0",
      "peerDependencies": {"vite-peer": "^5.0.0 || ^6.0.0 || ^7.0.0"},
      "dist": {"tarball": "%s/peer-first-plugin/-/peer-first-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/peer-parent":
			fmt.Fprintf(w, `{
  "name": "peer-parent",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "peer-parent",
      "version": "1.0.0",
      "dependencies": {
        "peer-first-plugin": "1.0.0",
        "vite-peer": "5.4.0"
      },
      "dist": {"tarball": "%s/peer-parent/-/peer-parent-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/vite-peer":
			fmt.Fprintf(w, `{
  "name": "vite-peer",
  "dist-tags": {"latest": "7.0.0"},
  "versions": {
    "5.4.0": {
      "name": "vite-peer",
      "version": "5.4.0",
      "dist": {"tarball": "%s/vite-peer/-/vite-peer-5.4.0.tgz"}
    },
    "7.0.0": {
      "name": "vite-peer",
      "version": "7.0.0",
      "dist": {"tarball": "%s/vite-peer/-/vite-peer-7.0.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r))
		case "/wide-peer-plugin":
			fmt.Fprintf(w, `{
  "name": "wide-peer-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "wide-peer-plugin",
      "version": "1.0.0",
      "peerDependencies": {"shared-peer": "^1.0.0"},
      "dist": {"tarball": "%s/wide-peer-plugin/-/wide-peer-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/mid-peer-plugin":
			fmt.Fprintf(w, `{
  "name": "mid-peer-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "mid-peer-plugin",
      "version": "1.0.0",
      "peerDependencies": {"shared-peer": ">=1.1.0 <2.0.0"},
      "dist": {"tarball": "%s/mid-peer-plugin/-/mid-peer-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/exact-peer-plugin":
			fmt.Fprintf(w, `{
  "name": "exact-peer-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "exact-peer-plugin",
      "version": "1.0.0",
      "peerDependencies": {"shared-peer": "1.2.0"},
      "dist": {"tarball": "%s/exact-peer-plugin/-/exact-peer-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/disjoint-peer-plugin":
			fmt.Fprintf(w, `{
  "name": "disjoint-peer-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "disjoint-peer-plugin",
      "version": "1.0.0",
      "peerDependencies": {"shared-peer": "^2.0.0"},
      "dist": {"tarball": "%s/disjoint-peer-plugin/-/disjoint-peer-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/cyclic-a":
			fmt.Fprintf(w, `{
  "name": "cyclic-a",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "cyclic-a",
      "version": "1.0.0",
      "peerDependencies": {"cyclic-b": "1.0.0"},
      "dist": {"tarball": "%s/cyclic-a/-/cyclic-a-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/cyclic-b":
			fmt.Fprintf(w, `{
  "name": "cyclic-b",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "cyclic-b",
      "version": "1.0.0",
      "peerDependencies": {"cyclic-a": "1.0.0"},
      "dist": {"tarball": "%s/cyclic-b/-/cyclic-b-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/shared-peer":
			fmt.Fprintf(w, `{
  "name": "shared-peer",
  "dist-tags": {"latest": "1.3.0"},
  "versions": {
    "1.2.0": {
      "name": "shared-peer",
      "version": "1.2.0",
      "dist": {"tarball": "%s/shared-peer/-/shared-peer-1.2.0.tgz"}
    },
    "1.3.0": {
      "name": "shared-peer",
      "version": "1.3.0",
      "dist": {"tarball": "%s/shared-peer/-/shared-peer-1.3.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r))
		case "/peer-provider":
			fmt.Fprintf(w, `{
  "name": "peer-provider",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "peer-provider",
      "version": "1.0.0",
      "dependencies": {"shared-peer": "1.2.0"},
      "dist": {"tarball": "%s/peer-provider/-/peer-provider-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/host-provider":
			fmt.Fprintf(w, `{
  "name": "host-provider",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "host-provider",
      "version": "1.0.0",
      "dependencies": {"host": "1.2.0"},
      "dist": {"tarball": "%s/host-provider/-/host-provider-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-existing-peer-plugin":
			fmt.Fprintf(w, `{
  "name": "optional-existing-peer-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-existing-peer-plugin",
      "version": "1.0.0",
      "peerDependencies": {"shared-peer": "1.2.0"},
      "peerDependenciesMeta": {"shared-peer": {"optional": true}},
      "dist": {"tarball": "%s/optional-existing-peer-plugin/-/optional-existing-peer-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-existing-range-peer-plugin":
			fmt.Fprintf(w, `{
  "name": "optional-existing-range-peer-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-existing-range-peer-plugin",
      "version": "1.0.0",
      "peerDependencies": {"shared-peer": "^1.0.0"},
      "peerDependenciesMeta": {"shared-peer": {"optional": true}},
      "dist": {"tarball": "%s/optional-existing-range-peer-plugin/-/optional-existing-range-peer-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-existing-upper-bound-peer-plugin":
			fmt.Fprintf(w, `{
  "name": "optional-existing-upper-bound-peer-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-existing-upper-bound-peer-plugin",
      "version": "1.0.0",
      "peerDependencies": {"shared-peer": ">=1.0.0 <1.3.0"},
      "peerDependenciesMeta": {"shared-peer": {"optional": true}},
      "dist": {"tarball": "%s/optional-existing-upper-bound-peer-plugin/-/optional-existing-upper-bound-peer-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/missing-then-satisfied-optional-peer":
			fmt.Fprintf(w, `{
  "name": "missing-then-satisfied-optional-peer",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "missing-then-satisfied-optional-peer",
      "version": "1.0.0",
      "peerDependencies": {"shared-peer": "1.2.0"},
      "peerDependenciesMeta": {"shared-peer": {"optional": true}},
      "dist": {"tarball": "%s/missing-then-satisfied-optional-peer/-/missing-then-satisfied-optional-peer-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-conflict-then-satisfied-peer":
			fmt.Fprintf(w, `{
  "name": "optional-conflict-then-satisfied-peer",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-conflict-then-satisfied-peer",
      "version": "1.0.0",
      "peerDependencies": {"host": "^1.0.0"},
      "peerDependenciesMeta": {"host": {"optional": true}},
      "dist": {"tarball": "%s/optional-conflict-then-satisfied-peer/-/optional-conflict-then-satisfied-peer-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
}

func workspaceRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app":
			fmt.Fprintf(w, `{
  "name": "app",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "2.0.0": {
      "name": "app",
      "version": "2.0.0",
      "dist": {"tarball": "%s/app/-/app-2.0.0.tgz"}
    }
  }
}`, serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
}

func unsupportedSpecRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/prod-unsupported":
			fmt.Fprintf(w, `{
  "name": "prod-unsupported",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "prod-unsupported",
      "version": "1.0.0",
      "dependencies": {"local-child": "file:../local-child"},
      "dist": {"tarball": "%s/prod-unsupported/-/prod-unsupported-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-unsupported":
			fmt.Fprintf(w, `{
  "name": "optional-unsupported",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-unsupported",
      "version": "1.0.0",
      "optionalDependencies": {"optional-wrapper": "1.0.0"},
      "dist": {"tarball": "%s/optional-unsupported/-/optional-unsupported-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-wrapper":
			fmt.Fprintf(w, `{
  "name": "optional-wrapper",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-wrapper",
      "version": "1.0.0",
      "dependencies": {"local-child": "file:../local-child"},
      "dist": {"tarball": "%s/optional-wrapper/-/optional-wrapper-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
}

func optionalFailureRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/optional-root":
			fmt.Fprintf(w, `{
  "name": "optional-root",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-root",
      "version": "1.0.0",
      "optionalDependencies": {"missing-optional": "1.0.0"},
      "dist": {"tarball": "%s/optional-root/-/optional-root-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/prod-root":
			fmt.Fprintf(w, `{
  "name": "prod-root",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "prod-root",
      "version": "1.0.0",
      "dependencies": {"missing-prod": "1.0.0"},
      "dist": {"tarball": "%s/prod-root/-/prod-root-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-meta-root":
			fmt.Fprintf(w, `{
  "name": "optional-meta-root",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-meta-root",
      "version": "1.0.0",
      "optionalDependencies": {"optional-wrapper": "1.0.0"},
      "dist": {"tarball": "%s/optional-meta-root/-/optional-meta-root-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-wrapper":
			fmt.Fprintf(w, `{
  "name": "optional-wrapper",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-wrapper",
      "version": "1.0.0",
      "dependencies": {"missing-meta": "1.0.0"},
      "dist": {"tarball": "%s/optional-wrapper/-/optional-wrapper-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-shared-root":
			fmt.Fprintf(w, `{
  "name": "optional-shared-root",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-shared-root",
      "version": "1.0.0",
      "optionalDependencies": {"optional-shared-wrapper": "1.0.0"},
      "dist": {"tarball": "%s/optional-shared-root/-/optional-shared-root-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-shared-wrapper":
			fmt.Fprintf(w, `{
  "name": "optional-shared-wrapper",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-shared-wrapper",
      "version": "1.0.0",
      "dependencies": {"shared-required": "1.0.0", "missing-meta": "1.0.0"},
      "dist": {"tarball": "%s/optional-shared-wrapper/-/optional-shared-wrapper-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-shared-unreferenced-root":
			fmt.Fprintf(w, `{
  "name": "optional-shared-unreferenced-root",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-shared-unreferenced-root",
      "version": "1.0.0",
      "optionalDependencies": {"optional-shared-wrapper-unreferenced": "1.0.0"},
      "dist": {"tarball": "%s/optional-shared-unreferenced-root/-/optional-shared-unreferenced-root-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-shared-wrapper-unreferenced":
			fmt.Fprintf(w, `{
  "name": "optional-shared-wrapper-unreferenced",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-shared-wrapper-unreferenced",
      "version": "1.0.0",
      "dependencies": {"shared-only-optional": "1.0.0", "missing-meta": "1.0.0"},
      "dist": {"tarball": "%s/optional-shared-wrapper-unreferenced/-/optional-shared-wrapper-unreferenced-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-shared-dual-root":
			fmt.Fprintf(w, `{
  "name": "optional-shared-dual-root",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-shared-dual-root",
      "version": "1.0.0",
      "optionalDependencies": {"optional-good-wrapper": "1.0.0", "optional-bad-wrapper": "1.0.0"},
      "dist": {"tarball": "%s/optional-shared-dual-root/-/optional-shared-dual-root-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-good-wrapper":
			fmt.Fprintf(w, `{
  "name": "optional-good-wrapper",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-good-wrapper",
      "version": "1.0.0",
      "dependencies": {"shared-only-optional": "1.0.0"},
      "dist": {"tarball": "%s/optional-good-wrapper/-/optional-good-wrapper-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/optional-bad-wrapper":
			fmt.Fprintf(w, `{
  "name": "optional-bad-wrapper",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "optional-bad-wrapper",
      "version": "1.0.0",
      "dependencies": {"shared-only-optional": "1.0.0", "missing-meta": "1.0.0"},
      "dist": {"tarball": "%s/optional-bad-wrapper/-/optional-bad-wrapper-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/shared-required":
			fmt.Fprintf(w, `{
  "name": "shared-required",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "shared-required",
      "version": "1.0.0",
      "dist": {"tarball": "%s/shared-required/-/shared-required-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/shared-only-optional":
			fmt.Fprintf(w, `{
  "name": "shared-only-optional",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "shared-only-optional",
      "version": "1.0.0",
      "dist": {"tarball": "%s/shared-only-optional/-/shared-only-optional-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
}

func engineRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engine-parent":
			fmt.Fprintf(w, `{
  "name": "engine-parent",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "engine-parent",
      "version": "1.0.0",
      "optionalDependencies": {"engine-package": "1.0.0"},
      "dist": {"tarball": "%s/engine-parent/-/engine-parent-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/engine-optional-failing-parent":
			fmt.Fprintf(w, `{
  "name": "engine-optional-failing-parent",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "engine-optional-failing-parent",
      "version": "1.0.0",
      "optionalDependencies": {"engine-failing-wrapper": "1.0.0"},
      "dist": {"tarball": "%s/engine-optional-failing-parent/-/engine-optional-failing-parent-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/engine-failing-wrapper":
			fmt.Fprintf(w, `{
  "name": "engine-failing-wrapper",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "engine-failing-wrapper",
      "version": "1.0.0",
      "dependencies": {"engine-package": "1.0.0", "missing-engine-meta": "1.0.0"},
      "dist": {"tarball": "%s/engine-failing-wrapper/-/engine-failing-wrapper-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/engine-package":
			fmt.Fprintf(w, `{
  "name": "engine-package",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "engine-package",
      "version": "1.0.0",
      "engines": {"node": ">=20"},
      "dist": {"tarball": "%s/engine-package/-/engine-package-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/engine-range-package":
			fmt.Fprintf(w, `{
  "name": "engine-range-package",
  "dist-tags": {"latest": "1.2.0"},
  "versions": {
    "1.1.0": {
      "name": "engine-range-package",
      "version": "1.1.0",
      "engines": {"node": ">=1"},
      "dist": {"tarball": "%s/engine-range-package/-/engine-range-package-1.1.0.tgz"}
    },
    "1.2.0": {
      "name": "engine-range-package",
      "version": "1.2.0",
      "engines": {"node": ">=20"},
      "dist": {"tarball": "%s/engine-range-package/-/engine-range-package-1.2.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r))
		case "/engine-peer-plugin":
			fmt.Fprintf(w, `{
  "name": "engine-peer-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "engine-peer-plugin",
      "version": "1.0.0",
      "peerDependencies": {"engine-package": "1.0.0"},
      "dist": {"tarball": "%s/engine-peer-plugin/-/engine-peer-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
}

func deprecationRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/deprecated-package":
			fmt.Fprintf(w, `{
  "name": "deprecated-package",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "deprecated-package",
      "version": "1.0.0",
      "deprecated": "use maintained-package instead",
      "dist": {"tarball": "%s/deprecated-package/-/deprecated-package-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/deprecated-range-package":
			fmt.Fprintf(w, `{
  "name": "deprecated-range-package",
  "dist-tags": {"latest": "1.1.0"},
  "versions": {
    "1.0.0": {
      "name": "deprecated-range-package",
      "version": "1.0.0",
      "dist": {"tarball": "%s/deprecated-range-package/-/deprecated-range-package-1.0.0.tgz"}
    },
    "1.1.0": {
      "name": "deprecated-range-package",
      "version": "1.1.0",
      "deprecated": "bad version",
      "dist": {"tarball": "%s/deprecated-range-package/-/deprecated-range-package-1.1.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
}

func platformRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	currentOS := npmOS(runtime.GOOS)
	blockedOS := "!" + currentOS
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/parent":
			fmt.Fprintf(w, `{
  "name": "parent",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "parent",
      "version": "1.0.0",
      "optionalDependencies": {"incompatible": "1.0.0", "compatible": "1.0.0"},
      "dist": {"tarball": "%s/parent/-/parent-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/incompatible":
			fmt.Fprintf(w, `{
  "name": "incompatible",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "incompatible",
      "version": "1.0.0",
      "os": ["%s"],
      "dist": {"tarball": "%s/incompatible/-/incompatible-1.0.0.tgz"}
    }
  }
}`, blockedOS, serverURL(r))
		case "/compatible":
			fmt.Fprintf(w, `{
  "name": "compatible",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "compatible",
      "version": "1.0.0",
      "os": ["%s"],
      "dist": {"tarball": "%s/compatible/-/compatible-1.0.0.tgz"}
    }
  }
}`, currentOS, serverURL(r))
		case "/any-platform":
			fmt.Fprintf(w, `{
  "name": "any-platform",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "any-platform",
      "version": "1.0.0",
      "os": ["any"],
      "cpu": ["any"],
      "libc": ["any"],
      "dist": {"tarball": "%s/any-platform/-/any-platform-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/libc-parent":
			fmt.Fprintf(w, `{
  "name": "libc-parent",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "libc-parent",
      "version": "1.0.0",
      "optionalDependencies": {"libc-incompatible": "1.0.0", "libc-compatible": "1.0.0"},
      "dist": {"tarball": "%s/libc-parent/-/libc-parent-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/libc-incompatible":
			fmt.Fprintf(w, `{
  "name": "libc-incompatible",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "libc-incompatible",
      "version": "1.0.0",
      "libc": ["musl"],
      "dist": {"tarball": "%s/libc-incompatible/-/libc-incompatible-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/libc-compatible":
			fmt.Fprintf(w, `{
  "name": "libc-compatible",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "libc-compatible",
      "version": "1.0.0",
      "libc": ["glibc"],
      "dist": {"tarball": "%s/libc-compatible/-/libc-compatible-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/platform-peer-plugin":
			fmt.Fprintf(w, `{
  "name": "platform-peer-plugin",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "platform-peer-plugin",
      "version": "1.0.0",
      "peerDependencies": {"incompatible": "1.0.0"},
      "dist": {"tarball": "%s/platform-peer-plugin/-/platform-peer-plugin-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
}

func optionalOverrideRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/parent":
			fmt.Fprintf(w, `{
  "name": "parent",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "parent",
      "version": "1.0.0",
      "dependencies": {"shared": "1.0.0"},
      "optionalDependencies": {"shared": "2.0.0"},
      "dist": {"tarball": "%s/parent/-/parent-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/shared":
			fmt.Fprintf(w, `{
  "name": "shared",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "1.0.0": {
      "name": "shared",
      "version": "1.0.0",
      "dist": {"tarball": "%s/shared/-/shared-1.0.0.tgz"}
    },
    "2.0.0": {
      "name": "shared",
      "version": "2.0.0",
      "dist": {"tarball": "%s/shared/-/shared-2.0.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
}

func bundleRegistry(t *testing.T, bundleField string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/parent":
			fmt.Fprintf(w, `{
  "name": "parent",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "parent",
      "version": "1.0.0",
      "dependencies": {"bundled": "1.0.0", "loose": "1.0.0"},
      %s,
      "dist": {"tarball": "%s/parent/-/parent-1.0.0.tgz"}
    }
  }
}`, bundleField, serverURL(r))
		case "/bundled":
			fmt.Fprintf(w, `{
  "name": "bundled",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "bundled",
      "version": "1.0.0",
      "dist": {"tarball": "%s/bundled/-/bundled-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/loose":
			fmt.Fprintf(w, `{
  "name": "loose",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "loose",
      "version": "1.0.0",
      "dist": {"tarball": "%s/loose/-/loose-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
}

func bundleRegistryFullMetadata(t *testing.T, bundleFields string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/parent":
			fmt.Fprintf(w, `{
  "name": "parent",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "parent",
      "version": "1.0.0",
      "dependencies": {"bundled": "1.0.0", "alt-bundled": "1.0.0", "loose": "1.0.0"},
      %s,
      "dist": {"tarball": "%s/parent/-/parent-1.0.0.tgz"}
    }
  }
}`, bundleFields, serverURL(r))
		case "/bundled":
			fmt.Fprintf(w, `{
  "name": "bundled",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "bundled",
      "version": "1.0.0",
      "dist": {"tarball": "%s/bundled/-/bundled-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/alt-bundled":
			fmt.Fprintf(w, `{
  "name": "alt-bundled",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "alt-bundled",
      "version": "1.0.0",
      "dist": {"tarball": "%s/alt-bundled/-/alt-bundled-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/loose":
			fmt.Fprintf(w, `{
  "name": "loose",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "loose",
      "version": "1.0.0",
      "dist": {"tarball": "%s/loose/-/loose-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
}

func dedupeRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/novelty-a":
			fmt.Fprintf(w, `{
  "name": "novelty-a",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "novelty-a",
      "version": "1.0.0",
      "dependencies": {"shared": "1.2.0"},
      "dist": {"tarball": "%s/novelty-a/-/novelty-a-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/novelty-c":
			fmt.Fprintf(w, `{
  "name": "novelty-c",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "novelty-c",
      "version": "1.0.0",
      "dependencies": {"shared": "^1.0.0"},
      "dist": {"tarball": "%s/novelty-c/-/novelty-c-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/consumer":
			fmt.Fprintf(w, `{
  "name": "consumer",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "consumer",
      "version": "1.0.0",
      "dependencies": {"shared": "^1.0.0"},
      "dist": {"tarball": "%s/consumer/-/consumer-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/consumer-two":
			fmt.Fprintf(w, `{
  "name": "consumer-two",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "consumer-two",
      "version": "1.0.0",
      "dependencies": {"shared": "^2.0.0"},
      "dist": {"tarball": "%s/consumer-two/-/consumer-two-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/shared":
			fmt.Fprintf(w, `{
  "name": "shared",
  "dist-tags": {"latest": "2.1.0"},
  "versions": {
    "1.2.0": {
      "name": "shared",
      "version": "1.2.0",
      "dist": {"tarball": "%s/shared/-/shared-1.2.0.tgz"}
    },
    "1.3.0": {
      "name": "shared",
      "version": "1.3.0",
      "dist": {"tarball": "%s/shared/-/shared-1.3.0.tgz"}
    },
    "2.1.0": {
      "name": "shared",
      "version": "2.1.0",
      "dist": {"tarball": "%s/shared/-/shared-2.1.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r), serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
}

func findNode(t *testing.T, graph *Graph, name string) *Node {
	t.Helper()
	for _, node := range graph.Nodes() {
		if node.Name == name {
			return node
		}
	}
	t.Fatalf("node %s not found", name)
	return nil
}

func equalPackageKeys(a, b []Package) bool {
	if len(a) != len(b) {
		return false
	}
	keysA := make([]string, 0, len(a))
	keysB := make([]string, 0, len(b))
	for _, pkg := range a {
		keysA = append(keysA, pkg.Key())
	}
	for _, pkg := range b {
		keysB = append(keysB, pkg.Key())
	}
	sort.Strings(keysA)
	sort.Strings(keysB)
	for i := range keysA {
		if keysA[i] != keysB[i] {
			return false
		}
	}
	return true
}
