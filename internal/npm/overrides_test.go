package npm

import (
	"context"
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
  "dist-tags": {"latest": "2.0.0"},
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
    }
  }
}`, serverURL(r), serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
}
