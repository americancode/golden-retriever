package npm

import (
	"context"
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
