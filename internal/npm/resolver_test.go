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

func TestResolverOptionalPeerConflictRecordsByDefault(t *testing.T) {
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
	if len(graph.PeerConflicts) != 1 {
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
