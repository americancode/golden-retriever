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

func TestResolverErrorsOnPeerConflict(t *testing.T) {
	srv := peerRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
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

func TestResolverSkipsIncompatibleOptionalDependency(t *testing.T) {
	srv := platformRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("incompatible@1.0.0") {
		t.Fatalf("incompatible optional dependency should be skipped: %#v", graph.Packages())
	}
	if !graph.Has("compatible@1.0.0") {
		t.Fatalf("compatible optional dependency should be resolved: %#v", graph.Packages())
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

func TestResolverSkipsIncompatibleOptionalLibcDependency(t *testing.T) {
	srv := platformRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true, Libc: "glibc"}}
	graph, err := resolver.Resolve(context.Background(), map[string]string{"libc-parent": "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("libc-incompatible@1.0.0") {
		t.Fatalf("incompatible optional libc dependency should be skipped: %#v", graph.Packages())
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
}

func TestResolvePackageJSONErrorsOnUnsupportedRootSpec(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"dependencies":{"local":"file:../local"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ResolvePackageJSON(context.Background(), NewClient("https://example.test"), input, ResolveOptions{IncludeOptional: true})
	var specErr *UnsupportedSpecError
	if !errors.As(err, &specErr) {
		t.Fatalf("got %v, want UnsupportedSpecError", err)
	}
	if specErr.Name != "local" || specErr.Spec != "file:../local" || specErr.Type != "prod" {
		t.Fatalf("unexpected spec error: %#v", specErr)
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
		"git":       "git+https://github.com/acme/pkg.git",
		"hosted":    "github:acme/pkg",
		"gist":      "gist:acme/1234",
		"ssh-url":   "ssh://git@github.com/acme/pkg.git",
		"tarball":   "https://registry.npmjs.org/pkg/-/pkg-1.0.0.tgz",
		"directory": "../local",
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

func dedupeRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
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
