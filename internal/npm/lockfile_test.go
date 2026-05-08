package npm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLockfileV3Packages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "root", "version": "1.0.0"},
    "node_modules/a": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/a/-/a-1.0.0.tgz",
      "integrity": "sha512-demo"
    },
    "node_modules/@scope/b": {
      "version": "2.0.0",
      "resolved": "https://registry.npmjs.org/@scope/b/-/b-2.0.0.tgz"
    }
  }
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := LoadLockfile(path)
	if err != nil {
		t.Fatal(err)
	}
	pkgs := graph.Packages()
	if len(pkgs) != 2 {
		t.Fatalf("got %d packages, want 2", len(pkgs))
	}
	if pkgs[0].Name != "@scope/b" || pkgs[1].Name != "a" {
		t.Fatalf("unexpected packages: %#v", pkgs)
	}
}

func TestLoadLockfileV1NestedDependencies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "lockfileVersion": 1,
  "dependencies": {
    "a": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/a/-/a-1.0.0.tgz",
      "integrity": "sha512-a",
      "dependencies": {
        "b": {
          "version": "2.0.0",
          "resolved": "https://registry.npmjs.org/b/-/b-2.0.0.tgz",
          "integrity": "sha512-b"
        }
      }
    }
  }
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := LoadLockfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("a@1.0.0") || !graph.Has("b@2.0.0") {
		t.Fatalf("nested v1 dependencies not imported: %#v", graph.Packages())
	}
}

func TestLoadLockfileAncientDependenciesOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "dependencies": {
    "ancient": {
      "version": "0.1.0",
      "resolved": "https://registry.npmjs.org/ancient/-/ancient-0.1.0.tgz",
      "requires": {
        "child": "1.0.0"
      },
      "dependencies": {
        "child": {
          "version": "1.0.0",
          "resolved": "https://registry.npmjs.org/child/-/child-1.0.0.tgz"
        }
      }
    }
  }
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := LoadLockfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("ancient@0.1.0") || !graph.Has("child@1.0.0") {
		t.Fatalf("ancient dependency-only lockfile not imported: %#v", graph.Packages())
	}
}

func TestLoadInputDirectoryPrioritizesShrinkwrap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(`{
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "root", "version": "1.0.0"},
    "node_modules/from-lock": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/from-lock/-/from-lock-1.0.0.tgz"
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "npm-shrinkwrap.json"), []byte(`{
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "root", "version": "1.0.0"},
    "node_modules/from-shrinkwrap": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/from-shrinkwrap/-/from-shrinkwrap-1.0.0.tgz"
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := LoadInput(context.Background(), NewClient("https://example.test"), dir, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("from-shrinkwrap@1.0.0") || graph.Has("from-lock@1.0.0") {
		t.Fatalf("directory input should prioritize shrinkwrap: %#v", graph.Packages())
	}
}

func TestLoadInputDirectoryIgnoresHiddenLockfile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"root","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	nodeModules := filepath.Join(dir, "node_modules")
	if err := os.MkdirAll(nodeModules, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodeModules, ".package-lock.json"), []byte(`{
  "lockfileVersion": 3,
  "packages": {
    "node_modules/hidden": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/hidden/-/hidden-1.0.0.tgz"
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := LoadInput(context.Background(), NewClient("https://example.test"), dir, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("hidden@1.0.0") || len(graph.Packages()) != 0 {
		t.Fatalf("hidden installed-tree lockfile should not affect input resolution: %#v", graph.Packages())
	}
}

func TestLoadLockfileDerivesMissingResolvedForRegistryPackages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "root", "version": "1.0.0"},
    "node_modules/a": {
      "version": "1.0.0",
      "integrity": "sha512-a"
    },
    "node_modules/@scope/b": {
      "version": "2.0.0",
      "integrity": "sha512-b"
    }
  }
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := LoadLockfile(path)
	if err != nil {
		t.Fatal(err)
	}
	pkgs := graph.Packages()
	if len(pkgs) != 2 {
		t.Fatalf("got %d packages, want 2: %#v", len(pkgs), pkgs)
	}
	got := map[string]Package{}
	for _, pkg := range pkgs {
		got[pkg.Key()] = pkg
	}
	if got["a@1.0.0"].Tarball != "https://registry.npmjs.org/a/-/a-1.0.0.tgz" {
		t.Fatalf("a tarball = %s", got["a@1.0.0"].Tarball)
	}
	if got["@scope/b@2.0.0"].Tarball != "https://registry.npmjs.org/@scope/b/-/b-2.0.0.tgz" {
		t.Fatalf("scoped tarball = %s", got["@scope/b@2.0.0"].Tarball)
	}
	if got["@scope/b@2.0.0"].Integrity != "sha512-b" {
		t.Fatalf("integrity not preserved: %#v", got["@scope/b@2.0.0"])
	}
}

func TestLoadLockfileDerivesMissingResolvedForDependencies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "dependencies": {
    "left-pad": {
      "version": "1.3.0",
      "integrity": "sha512-leftpad"
    },
    "@scope/pkg": {
      "version": "2.0.0"
    }
  }
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := LoadLockfile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Package{}
	for _, pkg := range graph.Packages() {
		got[pkg.Key()] = pkg
	}
	if got["left-pad@1.3.0"].Tarball != "https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz" {
		t.Fatalf("left-pad tarball = %s", got["left-pad@1.3.0"].Tarball)
	}
	if got["left-pad@1.3.0"].Integrity != "sha512-leftpad" {
		t.Fatalf("left-pad integrity = %s", got["left-pad@1.3.0"].Integrity)
	}
	if got["@scope/pkg@2.0.0"].Tarball != "https://registry.npmjs.org/@scope/pkg/-/pkg-2.0.0.tgz" {
		t.Fatalf("scoped dependency tarball = %s", got["@scope/pkg@2.0.0"].Tarball)
	}
}

func TestLoadLockfileMergesPackagesAndDependenciesMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "lockfileVersion": 2,
  "packages": {
    "": {"name": "root", "version": "1.0.0"},
    "node_modules/from-packages": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/from-packages/-/from-packages-1.0.0.tgz"
    }
  },
  "dependencies": {
    "from-dependencies": {
      "version": "2.0.0",
      "resolved": "https://registry.npmjs.org/from-dependencies/-/from-dependencies-2.0.0.tgz",
      "integrity": "sha512-deps"
    },
    "from-packages": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/from-packages/-/from-packages-1.0.0.tgz"
    }
  }
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := LoadLockfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("from-packages@1.0.0") || !graph.Has("from-dependencies@2.0.0") {
		t.Fatalf("packages and dependencies metadata should be merged: %#v", graph.Packages())
	}
	if len(graph.Packages()) != 2 {
		t.Fatalf("duplicate lock entries should be de-duplicated: %#v", graph.Packages())
	}
	got := map[string]Package{}
	for _, pkg := range graph.Packages() {
		got[pkg.Key()] = pkg
	}
	if got["from-dependencies@2.0.0"].Integrity != "sha512-deps" {
		t.Fatalf("dependency metadata integrity not preserved: %#v", got["from-dependencies@2.0.0"])
	}
}

func TestLoadLockfileImportsAliasPackageByManifestName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "root", "version": "1.0.0"},
    "node_modules/alias": {
      "name": "real-package",
      "version": "1.2.3",
      "integrity": "sha512-real"
    }
  }
}`)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := LoadLockfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Has("alias@1.2.3") || !graph.Has("real-package@1.2.3") {
		t.Fatalf("alias package should use manifest name for tarball acquisition: %#v", graph.Packages())
	}
	pkgs := graph.Packages()
	if len(pkgs) != 1 {
		t.Fatalf("got packages: %#v", pkgs)
	}
	if pkgs[0].Tarball != "https://registry.npmjs.org/real-package/-/real-package-1.2.3.tgz" {
		t.Fatalf("alias tarball = %s", pkgs[0].Tarball)
	}
}

func TestLoadLockfileFailsMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	if err := os.WriteFile(path, []byte(`{"lockfileVersion": 3,`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadLockfile(path)
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) {
		t.Fatalf("got %v, want json.SyntaxError", err)
	}
}
