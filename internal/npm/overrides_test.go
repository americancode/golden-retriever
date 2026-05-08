package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResolverAppliesTopLevelTransitiveOverride(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"app": "1.0.0"},
  "overrides": {"dep": "2.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	dep := findNode(t, graph, "dep")
	if dep.Version != "2.0.0" {
		t.Fatalf("dep version = %s, want 2.0.0", dep.Version)
	}
	app := findNode(t, graph, "app")
	edge := app.Dependencies["dep"]
	if edge == nil || edge.RawSpec != "^1.0.0" || edge.Spec != "2.0.0" {
		t.Fatalf("unexpected override edge: %#v", edge)
	}
}

func TestResolverAppliesNestedOverrideOnlyUnderParent(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"app": "1.0.0", "other": "1.0.0"},
  "overrides": {"app": {"dep": "2.0.0"}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	app := findNode(t, graph, "app")
	other := findNode(t, graph, "other")
	if app.Dependencies["dep"].To.Version != "2.0.0" {
		t.Fatalf("app dep version = %s", app.Dependencies["dep"].To.Version)
	}
	if other.Dependencies["dep"].To.Version != "1.0.0" {
		t.Fatalf("other dep version = %s", other.Dependencies["dep"].To.Version)
	}
}

func TestResolverAppliesNestedOverrideOnlyWhenParentVersionMatches(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"app": "1.0.0", "app-two": "2.0.0"},
  "overrides": {"app@^1.0.0": {"dep": "2.0.0"}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	app := findNode(t, graph, "app")
	appTwo := findNode(t, graph, "app-two")
	if app.Dependencies["dep"].To.Version != "2.0.0" {
		t.Fatalf("app dep version = %s", app.Dependencies["dep"].To.Version)
	}
	if appTwo.Dependencies["dep"].To.Version != "1.0.0" {
		t.Fatalf("app-two dep version = %s", appTwo.Dependencies["dep"].To.Version)
	}
}

func TestResolverPrefersVersionQualifiedChildOverride(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"app": "1.0.0"},
  "overrides": {
    "dep": "2.0.0",
    "dep@^1.0.0": "3.0.0"
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	app := findNode(t, graph, "app")
	edge := app.Dependencies["dep"]
	if edge == nil || edge.To.Version != "3.0.0" || edge.Spec != "3.0.0" {
		t.Fatalf("unexpected specific override edge: %#v", edge)
	}
}

func TestOverridesResolveWithRangeIntersectingSelector(t *testing.T) {
	overrides, err := ParseOverrides(json.RawMessage(`{
  "dep": "2.0.0",
  "dep@^1.0.0": "3.0.0"
}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	spec, rule := overrides.ResolveWithRule(nil, "dep", "^1.0.0")
	if spec != "3.0.0" || rule == nil || rule.Target.Spec != "^1.0.0" {
		t.Fatalf("got spec=%q rule=%#v, want version-qualified override", spec, rule)
	}
	parent := &Node{Name: "app", Version: "1.0.0"}
	spec, rule = overrides.ResolveWithRule(parent, "dep", "^1.0.0")
	if spec != "3.0.0" || rule == nil || rule.Target.Spec != "^1.0.0" {
		t.Fatalf("with parent got spec=%q rule=%#v, want version-qualified override", spec, rule)
	}
	rangeOverrides, err := ParseOverrides(json.RawMessage(`{
  "range-dep@^1.0.0": "3.0.0"
}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	spec, rule = rangeOverrides.ResolveWithRule(parent, "range-dep", "^1.2.0")
	if spec != "3.0.0" || rule == nil || rule.Target.Spec != "^1.0.0" {
		t.Fatalf("intersecting range got spec=%q rule=%#v, want version-qualified override", spec, rule)
	}
}

func TestResolverMatchesVersionQualifiedOverrideByRangeIntersection(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"range-app": "1.0.0"},
  "overrides": {
    "range-dep@^1.0.0": "3.0.0"
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	app := findNode(t, graph, "range-app")
	edge := app.Dependencies["range-dep"]
	if edge == nil || edge.RawSpec != "^1.2.0" || edge.Spec != "3.0.0" || edge.To.Version != "3.0.0" {
		t.Fatalf("intersecting override selector should apply: %#v", edge)
	}
}

func TestResolverDoesNotMatchVersionQualifiedOverrideWhenRangesAreDisjoint(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"range-app": "1.0.0"},
  "overrides": {
    "range-dep@^2.0.0": "3.0.0"
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	app := findNode(t, graph, "range-app")
	edge := app.Dependencies["range-dep"]
	if edge == nil || edge.RawSpec != "^1.2.0" || edge.Spec != "^1.2.0" || edge.To.Version != "1.2.3" {
		t.Fatalf("disjoint override selector should not apply: %#v", edge)
	}
}

func TestResolverPrefersVersionQualifiedParentOverride(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"app": "1.0.0"},
  "overrides": {
    "app": {"dep": "2.0.0"},
    "app@^1.0.0": {"dep": "3.0.0"}
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	app := findNode(t, graph, "app")
	edge := app.Dependencies["dep"]
	if edge == nil || edge.To.Version != "3.0.0" || edge.Spec != "3.0.0" {
		t.Fatalf("unexpected parent-specific override edge: %#v", edge)
	}
}

func TestResolverAppliesOverrideSelfRule(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"wrapper": "1.0.0"},
  "overrides": {"app": {".": "2.0.0"}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	app := findNode(t, graph, "app")
	if app.Version != "2.0.0" {
		t.Fatalf("app version = %s, want 2.0.0", app.Version)
	}
	wrapper := findNode(t, graph, "wrapper")
	edge := wrapper.Dependencies["app"]
	if edge == nil || edge.RawSpec != "^1.0.0" || edge.Spec != "2.0.0" {
		t.Fatalf("unexpected wrapper edge: %#v", edge)
	}
}

func TestResolverAppliesOverrideReference(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"app": "1.0.0", "dep": "2.0.0"},
  "overrides": {"app": {"dep": "$dep"}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	app := findNode(t, graph, "app")
	edge := app.Dependencies["dep"]
	if edge == nil || edge.Spec != "2.0.0" || edge.RawSpec != "^1.0.0" {
		t.Fatalf("unexpected referenced override edge: %#v", edge)
	}
}

func TestResolverAppliesTopLevelOverrideReferenceToNestedDependency(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"app": "1.0.0", "dep": "2.0.0"},
  "overrides": {"dep": "$dep"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	app := findNode(t, graph, "app")
	nested := app.Dependencies["dep"]
	if nested == nil || nested.To.Version != "2.0.0" || nested.Spec != "2.0.0" || nested.RawSpec != "^1.0.0" {
		t.Fatalf("unexpected nested referenced override edge: %#v", nested)
	}
	root := graph.Root.Dependencies["dep"]
	if root == nil || root.To != nested.To {
		t.Fatalf("expected nested dep to share root direct dependency node, root=%#v nested=%#v", root, nested)
	}
}

func TestResolverRejectsDirectDependencyOverrideConflict(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"app": "^1.0.0"},
  "overrides": {"app": "2.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	var conflict *OverrideConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("got %v, want OverrideConflictError", err)
	}
	if conflict.Name != "app" || conflict.RawSpec != "^1.0.0" || conflict.Spec != "2.0.0" {
		t.Fatalf("unexpected conflict: %#v", conflict)
	}
}

func TestResolverRejectsPeerDependencyOverrideConflict(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "peerDependencies": {"app": "^1.0.0"},
  "overrides": {"app": "2.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	var conflict *OverrideConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("got %v, want OverrideConflictError", err)
	}
	if conflict.Name != "app" || conflict.RawSpec != "^1.0.0" || conflict.Spec != "2.0.0" {
		t.Fatalf("unexpected conflict: %#v", conflict)
	}
}

func TestResolverAllowsDirectDependencyOverrideReference(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"app": "^1.0.0"},
  "overrides": {"app": "$app"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	edge := graph.Root.Dependencies["app"]
	if edge == nil || edge.Spec != "^1.0.0" || edge.RawSpec != "^1.0.0" {
		t.Fatalf("unexpected direct ref edge: %#v", edge)
	}
}

func TestResolverCoercesEmptyOverrideSelfRuleToWildcard(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"wrapper": "1.0.0"},
  "overrides": {"app": {".": ""}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	app := findNode(t, graph, "app")
	if app.Version != "2.0.0" {
		t.Fatalf("app version = %s, want latest wildcard 2.0.0", app.Version)
	}
	wrapper := findNode(t, graph, "wrapper")
	edge := wrapper.Dependencies["app"]
	if edge == nil || edge.Spec != "*" {
		t.Fatalf("unexpected wrapper edge: %#v", edge)
	}
}

func TestResolverCoercesEmptyOverrideObjectToWildcard(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"wrapper": "1.0.0"},
  "overrides": {"app": {}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	app := findNode(t, graph, "app")
	if app.Version != "2.0.0" {
		t.Fatalf("app version = %s, want latest wildcard 2.0.0", app.Version)
	}
}

func TestResolverRejectsInvalidOverrideSelector(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"app": "1.0.0"},
  "overrides": {"github:npm/cli": "1.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := ResolvePackageJSON(context.Background(), NewClient("https://example.test"), input, ResolveOptions{IncludeOptional: true})
	var nameErr *InvalidPackageNameError
	if !errors.As(err, &nameErr) {
		t.Fatalf("got %v, want InvalidPackageNameError", err)
	}
}

func TestResolverRejectsUnsupportedOverrideSpecs(t *testing.T) {
	tests := map[string]string{
		"selector-file": `{"app@file:../app":{"dep":"1.0.0"}}`,
		"value-file":    `{"dep":"file:../dep"}`,
		"value-git":     `{"dep":"github:org/dep"}`,
		"self-file":     `{"app":{".":"file:../app"}}`,
	}
	for name, overrides := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "package.json")
			if err := os.WriteFile(input, []byte(fmt.Sprintf(`{
  "dependencies": {"app": "1.0.0"},
  "overrides": %s
}`, overrides)), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := ResolvePackageJSON(context.Background(), NewClient("https://example.test"), input, ResolveOptions{IncludeOptional: true})
			var specErr *UnsupportedSpecError
			if !errors.As(err, &specErr) {
				t.Fatalf("got %v, want UnsupportedSpecError", err)
			}
			if specErr.Type != "override" {
				t.Fatalf("unexpected spec error: %#v", specErr)
			}
		})
	}
}

func TestResolverAppliesOverrideInsideCyclicDependencyChain(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"cycle-foo": "1.0.1"},
  "overrides": {"cycle-foo": {"cycle-foo": "2.0.0"}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("cycle-foo@1.0.1") || !graph.Has("cycle-bar@1.0.1") || !graph.Has("cycle-baz@1.0.1") || !graph.Has("cycle-foo@2.0.0") {
		t.Fatalf("cyclic override tree not resolved: %#v", graph.Packages())
	}
	rootFoo := graph.Root.Dependencies["cycle-foo"].To
	barEdge := rootFoo.Dependencies["cycle-bar"]
	if barEdge == nil || barEdge.To.Version != "1.0.1" {
		t.Fatalf("cycle-foo should depend on cycle-bar@1.0.1: %#v", barEdge)
	}
	bazEdge := barEdge.To.Dependencies["cycle-baz"]
	if bazEdge == nil || bazEdge.To.Version != "1.0.1" {
		t.Fatalf("cycle-bar should depend on cycle-baz@1.0.1: %#v", bazEdge)
	}
	fooEdge := bazEdge.To.Dependencies["cycle-foo"]
	if fooEdge == nil || fooEdge.To.Version != "2.0.0" || fooEdge.Spec != "2.0.0" {
		t.Fatalf("cycle-baz should be overridden to cycle-foo@2.0.0: %#v", fooEdge)
	}
}

func TestResolverOverrideFixesPeerConflict(t *testing.T) {
	srv := overrideRegistry(t)
	defer srv.Close()

	resolver := &Resolver{Client: NewClient(srv.URL), Options: ResolveOptions{IncludeOptional: true}}
	_, err := resolver.ResolveRoot(context.Background(), []DependencyRequest{
		{Name: "peer-a", Spec: "1.0.0", Type: EdgeProd},
		{Name: "peer-d", Spec: "2.0.0", Type: EdgeProd},
	})
	var conflict *PeerConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("got %v, want PeerConflictError before override", err)
	}

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "dependencies": {"peer-a": "1.0.0", "peer-d": "2.0.0"},
  "overrides": {"peer-a": {"peer-b": "2.0.0"}}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("peer-a@1.0.0") || !graph.Has("peer-b@2.0.0") || !graph.Has("peer-c@2.0.0") || !graph.Has("peer-d@2.0.0") {
		t.Fatalf("override should resolve peer conflict to the b@2/c@2 set: %#v", graph.Packages())
	}
	peerA := findNode(t, graph, "peer-a")
	edge := peerA.Peers["peer-b"]
	if edge == nil || !edge.Satisfied || edge.To == nil || edge.To.Version != "2.0.0" || edge.Spec != "2.0.0" {
		t.Fatalf("peer-a peer should be overridden to peer-b@2.0.0: %#v", edge)
	}
}

func overrideRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wrapper":
			fmt.Fprintf(w, `{
  "name": "wrapper",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "wrapper",
      "version": "1.0.0",
      "dependencies": {"app": "^1.0.0"},
      "dist": {"tarball": "%s/wrapper/-/wrapper-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/app":
			fmt.Fprintf(w, `{
  "name": "app",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "1.0.0": {
      "name": "app",
      "version": "1.0.0",
      "dependencies": {"dep": "^1.0.0"},
      "dist": {"tarball": "%s/app/-/app-1.0.0.tgz"}
    },
    "2.0.0": {
      "name": "app",
      "version": "2.0.0",
      "dist": {"tarball": "%s/app/-/app-2.0.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r))
		case "/app-two":
			fmt.Fprintf(w, `{
  "name": "app-two",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "2.0.0": {
      "name": "app-two",
      "version": "2.0.0",
      "dependencies": {"dep": "^1.0.0"},
      "dist": {"tarball": "%s/app-two/-/app-two-2.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/range-app":
			fmt.Fprintf(w, `{
  "name": "range-app",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "range-app",
      "version": "1.0.0",
      "dependencies": {"range-dep": "^1.2.0"},
      "dist": {"tarball": "%s/range-app/-/range-app-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/range-dep":
			fmt.Fprintf(w, `{
  "name": "range-dep",
  "dist-tags": {"latest": "3.0.0"},
  "versions": {
    "1.2.3": {
      "name": "range-dep",
      "version": "1.2.3",
      "dist": {"tarball": "%s/range-dep/-/range-dep-1.2.3.tgz"}
    },
    "3.0.0": {
      "name": "range-dep",
      "version": "3.0.0",
      "dist": {"tarball": "%s/range-dep/-/range-dep-3.0.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r))
		case "/other":
			fmt.Fprintf(w, `{
  "name": "other",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "other",
      "version": "1.0.0",
      "dependencies": {"dep": "^1.0.0"},
      "dist": {"tarball": "%s/other/-/other-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/dep":
			fmt.Fprintf(w, `{
  "name": "dep",
  "dist-tags": {"latest": "3.0.0"},
  "versions": {
    "1.0.0": {
      "name": "dep",
      "version": "1.0.0",
      "dist": {"tarball": "%s/dep/-/dep-1.0.0.tgz"}
    },
    "2.0.0": {
      "name": "dep",
      "version": "2.0.0",
      "dist": {"tarball": "%s/dep/-/dep-2.0.0.tgz"}
    },
    "3.0.0": {
      "name": "dep",
      "version": "3.0.0",
      "dist": {"tarball": "%s/dep/-/dep-3.0.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r), serverURL(r))
		case "/cycle-foo":
			fmt.Fprintf(w, `{
  "name": "cycle-foo",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "1.0.1": {
      "name": "cycle-foo",
      "version": "1.0.1",
      "dependencies": {"cycle-bar": "1.0.1"},
      "dist": {"tarball": "%s/cycle-foo/-/cycle-foo-1.0.1.tgz"}
    },
    "2.0.0": {
      "name": "cycle-foo",
      "version": "2.0.0",
      "dist": {"tarball": "%s/cycle-foo/-/cycle-foo-2.0.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r))
		case "/cycle-bar":
			fmt.Fprintf(w, `{
  "name": "cycle-bar",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "1.0.1": {
      "name": "cycle-bar",
      "version": "1.0.1",
      "dependencies": {"cycle-baz": "1.0.1"},
      "dist": {"tarball": "%s/cycle-bar/-/cycle-bar-1.0.1.tgz"}
    },
    "2.0.0": {
      "name": "cycle-bar",
      "version": "2.0.0",
      "dist": {"tarball": "%s/cycle-bar/-/cycle-bar-2.0.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r))
		case "/cycle-baz":
			fmt.Fprintf(w, `{
  "name": "cycle-baz",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "1.0.1": {
      "name": "cycle-baz",
      "version": "1.0.1",
      "dependencies": {"cycle-foo": "1.0.1"},
      "dist": {"tarball": "%s/cycle-baz/-/cycle-baz-1.0.1.tgz"}
    },
    "2.0.0": {
      "name": "cycle-baz",
      "version": "2.0.0",
      "dist": {"tarball": "%s/cycle-baz/-/cycle-baz-2.0.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r))
		case "/peer-a":
			fmt.Fprintf(w, `{
  "name": "peer-a",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "peer-a",
      "version": "1.0.0",
      "peerDependencies": {"peer-b": "1.0.0"},
      "dist": {"tarball": "%s/peer-a/-/peer-a-1.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/peer-b":
			fmt.Fprintf(w, `{
  "name": "peer-b",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "1.0.0": {
      "name": "peer-b",
      "version": "1.0.0",
      "peerDependencies": {"peer-c": "2.0.0"},
      "dist": {"tarball": "%s/peer-b/-/peer-b-1.0.0.tgz"}
    },
    "2.0.0": {
      "name": "peer-b",
      "version": "2.0.0",
      "peerDependencies": {"peer-c": "2.0.0"},
      "dist": {"tarball": "%s/peer-b/-/peer-b-2.0.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r))
		case "/peer-c":
			fmt.Fprintf(w, `{
  "name": "peer-c",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "2.0.0": {
      "name": "peer-c",
      "version": "2.0.0",
      "dist": {"tarball": "%s/peer-c/-/peer-c-2.0.0.tgz"}
    }
  }
}`, serverURL(r))
		case "/peer-d":
			fmt.Fprintf(w, `{
  "name": "peer-d",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "2.0.0": {
      "name": "peer-d",
      "version": "2.0.0",
      "peerDependencies": {"peer-b": "2.0.0"},
      "dist": {"tarball": "%s/peer-d/-/peer-d-2.0.0.tgz"}
    }
  }
}`, serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
}
