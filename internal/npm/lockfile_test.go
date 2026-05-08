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

func TestLoadLockfileAncientVersionZeroWithFromFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "lockfileVersion": 0,
  "dependencies": {
    "from-only": {
      "from": "from-only@1.2.3",
      "integrity": "sha512-from-only"
    },
    "legacy-url-from": {
      "from": "https://registry.npmjs.org/legacy-url-from/-/legacy-url-from-2.3.4.tgz"
    },
    "legacy-nested": {
      "version": "0.1.0",
      "dependencies": {
        "nested-from": {
          "from": "nested-from@3.4.5"
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
	if !graph.Has("from-only@1.2.3") || !graph.Has("legacy-url-from@2.3.4") || !graph.Has("legacy-nested@0.1.0") || !graph.Has("nested-from@3.4.5") {
		t.Fatalf("ancient v0 lockfile entries with from fallback not imported: %#v", graph.Packages())
	}
	got := map[string]Package{}
	for _, pkg := range graph.Packages() {
		got[pkg.Key()] = pkg
	}
	if got["from-only@1.2.3"].Tarball != "https://registry.npmjs.org/from-only/-/from-only-1.2.3.tgz" {
		t.Fatalf("from-only tarball = %s", got["from-only@1.2.3"].Tarball)
	}
	if got["legacy-url-from@2.3.4"].Tarball != "https://registry.npmjs.org/legacy-url-from/-/legacy-url-from-2.3.4.tgz" {
		t.Fatalf("legacy-url-from tarball = %s", got["legacy-url-from@2.3.4"].Tarball)
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

func TestLoadInputLegacyShrinkwrapPeerCase(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "npm-shrinkwrap.json"), []byte(`{
  "name": "root",
  "lockfileVersion": 1,
  "dependencies": {
    "legacy-peer-plugin": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/legacy-peer-plugin/-/legacy-peer-plugin-1.0.0.tgz",
      "peerDependencies": {"legacy-peer-host": "^1.0.0"},
      "dependencies": {
        "legacy-peer-host": {
          "version": "1.2.0",
          "resolved": "https://registry.npmjs.org/legacy-peer-host/-/legacy-peer-host-1.2.0.tgz"
        }
      }
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := LoadInput(context.Background(), NewClient("https://example.test"), dir, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("legacy-peer-plugin@1.0.0") || !graph.Has("legacy-peer-host@1.2.0") {
		t.Fatalf("legacy shrinkwrap peer tree should be imported: %#v", graph.Packages())
	}
}

func TestLoadInputLegacyShrinkwrapBundledEdgeCases(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "npm-shrinkwrap.json"), []byte(`{
  "name": "root",
  "lockfileVersion": 1,
  "dependencies": {
    "legacy-wrapper": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/legacy-wrapper/-/legacy-wrapper-1.0.0.tgz",
      "dependencies": {
        "legacy-bundled-child": {
          "version": "1.0.0",
          "bundled": true,
          "resolved": "https://registry.npmjs.org/legacy-bundled-child/-/legacy-bundled-child-1.0.0.tgz"
        },
        "legacy-shrinkwrapped-child": {
          "version": "2.0.0",
          "resolved": "https://registry.npmjs.org/legacy-shrinkwrapped-child/-/legacy-shrinkwrapped-child-2.0.0.tgz"
        }
      }
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := LoadInput(context.Background(), NewClient("https://example.test"), dir, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("legacy-wrapper@1.0.0") || !graph.Has("legacy-shrinkwrapped-child@2.0.0") {
		t.Fatalf("legacy shrinkwrapped packages should import wrapper and non-bundled children: %#v", graph.Packages())
	}
	if graph.Has("legacy-bundled-child@1.0.0") {
		t.Fatalf("legacy bundled child should not be acquired as registry tarball: %#v", graph.Packages())
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

func TestLoadInputUsesYarnLockForIncompleteLockMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package-lock.json"), []byte(`{
  "name": "root",
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "root", "version": "1.0.0"},
    "node_modules/left-pad": {}
  },
  "dependencies": {
    "left-pad": {}
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(`left-pad@^1.3.0:
  version "1.3.0"
  resolved "https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz#abc"
  integrity sha512-yarn-left-pad
`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := LoadInput(context.Background(), NewClient("https://example.test"), dir, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("left-pad@1.3.0") {
		t.Fatalf("expected yarn.lock to supply missing lock metadata: %#v", graph.Packages())
	}
	pkgs := graph.Packages()
	if len(pkgs) != 1 {
		t.Fatalf("unexpected packages: %#v", pkgs)
	}
	if pkgs[0].Tarball != "https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz" {
		t.Fatalf("left-pad tarball = %s", pkgs[0].Tarball)
	}
	if pkgs[0].Integrity != "sha512-yarn-left-pad" {
		t.Fatalf("left-pad integrity = %s", pkgs[0].Integrity)
	}
}

func TestLoadLockfilePathUsesSiblingYarnLockForIncompleteMetadata(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "package-lock.json")
	if err := os.WriteFile(lockPath, []byte(`{
  "name": "root",
  "dependencies": {
    "@scope/pkg": {
      "from": "@scope/pkg@^2.0.0"
    }
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "yarn.lock"), []byte(`"@scope/pkg@^2.0.0":
  version "2.1.0"
  resolved "https://registry.npmjs.org/@scope/pkg/-/pkg-2.1.0.tgz"
  integrity sha512-yarn-scope
`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := LoadInput(context.Background(), NewClient("https://example.test"), lockPath, ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Has("@scope/pkg@2.1.0") {
		t.Fatalf("expected sibling yarn.lock to enrich direct lockfile input: %#v", graph.Packages())
	}
	pkg := graph.Packages()[0]
	if pkg.Tarball != "https://registry.npmjs.org/@scope/pkg/-/pkg-2.1.0.tgz" || pkg.Integrity != "sha512-yarn-scope" {
		t.Fatalf("unexpected enriched package: %#v", pkg)
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

func TestLoadLockfileDerivesMissingVersionFromResolved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "root", "version": "1.0.0"},
    "node_modules/no-version": {
      "resolved": "https://registry.npmjs.org/no-version/-/no-version-4.5.6.tgz",
      "integrity": "sha512-no-version"
    }
  },
  "dependencies": {
    "legacy-no-version": {
      "resolved": "https://registry.npmjs.org/legacy-no-version/-/legacy-no-version-7.8.9.tgz"
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
	if !graph.Has("no-version@4.5.6") || !graph.Has("legacy-no-version@7.8.9") {
		t.Fatalf("missing-version entries should derive semver from resolved URL: %#v", graph.Packages())
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

func TestLoadLockfileSkipsInBundleEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "root", "version": "1.0.0"},
    "node_modules/parent": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/parent/-/parent-1.0.0.tgz"
    },
    "node_modules/bundled": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/bundled/-/bundled-1.0.0.tgz",
      "inBundle": true
    }
  },
  "dependencies": {
    "legacy-bundled": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/legacy-bundled/-/legacy-bundled-1.0.0.tgz",
      "inBundle": true
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
	if !graph.Has("parent@1.0.0") {
		t.Fatalf("parent should be imported: %#v", graph.Packages())
	}
	if graph.Has("bundled@1.0.0") || graph.Has("legacy-bundled@1.0.0") {
		t.Fatalf("inBundle entries should not be separate tarball acquisitions: %#v", graph.Packages())
	}
}

func TestLoadLockfileSkipsLinkEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "root", "version": "1.0.0"},
    "node_modules/local-link": {
      "version": "1.0.0",
      "resolved": "../local-link",
      "link": true
    },
    "node_modules/registry-pkg": {
      "version": "2.0.0",
      "resolved": "https://registry.npmjs.org/registry-pkg/-/registry-pkg-2.0.0.tgz"
    }
  },
  "dependencies": {
    "legacy-link": {
      "version": "1.0.0",
      "resolved": "../legacy-link",
      "link": true
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
	if graph.Has("local-link@1.0.0") || graph.Has("legacy-link@1.0.0") {
		t.Fatalf("link entries should not be treated as registry tarballs: %#v", graph.Packages())
	}
	if !graph.Has("registry-pkg@2.0.0") {
		t.Fatalf("registry package should still be imported: %#v", graph.Packages())
	}
}

func TestLoadLockfileSkipsLocalAndGitResolvedEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "root", "version": "1.0.0"},
    "node_modules/local-file": {
      "version": "1.0.0",
      "resolved": "file:../local-file.tgz"
    },
    "node_modules/relative-file": {
      "version": "1.0.0",
      "resolved": "../relative-file.tgz"
    },
    "node_modules/git-package": {
      "version": "1.0.0",
      "resolved": "git+ssh://git@github.com/example/git-package.git#abc123"
    },
    "node_modules/remote-tarball": {
      "version": "1.0.0",
      "resolved": "https://example.test/remote-tarball-1.0.0.tgz"
    }
  },
  "dependencies": {
    "legacy-local": {
      "version": "1.0.0",
      "resolved": "file:legacy-local.tgz"
    },
    "legacy-remote": {
      "version": "1.0.0",
      "resolved": "https://example.test/legacy-remote-1.0.0.tgz"
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
	for _, key := range []string{
		"local-file@1.0.0",
		"relative-file@1.0.0",
		"git-package@1.0.0",
		"legacy-local@1.0.0",
	} {
		if graph.Has(key) {
			t.Fatalf("%s should not be imported as a registry tarball: %#v", key, graph.Packages())
		}
	}
	if !graph.Has("remote-tarball@1.0.0") || !graph.Has("legacy-remote@1.0.0") {
		t.Fatalf("remote tarballs should still be imported: %#v", graph.Packages())
	}
}

func TestLoadLockfileNormalizesIncompleteResolvedForms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "root", "version": "1.0.0"},
    "node_modules/protocol-relative": {
      "version": "1.0.0",
      "resolved": "//registry.npmjs.org/protocol-relative/-/protocol-relative-1.0.0.tgz"
    },
    "node_modules/root-relative": {
      "version": "2.0.0",
      "resolved": "/root-relative/-/root-relative-2.0.0.tgz"
    },
    "node_modules/bare-host": {
      "version": "3.0.0",
      "resolved": "registry.npmjs.org/bare-host/-/bare-host-3.0.0.tgz"
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
	if got["protocol-relative@1.0.0"].Tarball != "https://registry.npmjs.org/protocol-relative/-/protocol-relative-1.0.0.tgz" {
		t.Fatalf("protocol-relative tarball = %s", got["protocol-relative@1.0.0"].Tarball)
	}
	if got["root-relative@2.0.0"].Tarball != "https://registry.npmjs.org/root-relative/-/root-relative-2.0.0.tgz" {
		t.Fatalf("root-relative tarball = %s", got["root-relative@2.0.0"].Tarball)
	}
	if got["bare-host@3.0.0"].Tarball != "https://registry.npmjs.org/bare-host/-/bare-host-3.0.0.tgz" {
		t.Fatalf("bare-host tarball = %s", got["bare-host@3.0.0"].Tarball)
	}
}

func TestLoadLockfileSkipsNonSemverVersionEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "root", "version": "1.0.0"},
    "node_modules/git-version": {
      "version": "git+ssh://git@github.com/example/git-version.git#abc123",
      "resolved": "git+ssh://git@github.com/example/git-version.git#abc123"
    },
    "node_modules/file-version": {
      "version": "file:../file-version.tgz",
      "resolved": "file:../file-version.tgz"
    },
    "node_modules/registry-pkg": {
      "version": "1.2.3",
      "resolved": "https://registry.npmjs.org/registry-pkg/-/registry-pkg-1.2.3.tgz"
    }
  },
  "dependencies": {
    "legacy-git-version": {
      "version": "github:example/legacy-git-version"
    },
    "legacy-registry": {
      "version": "2.0.0",
      "resolved": "https://registry.npmjs.org/legacy-registry/-/legacy-registry-2.0.0.tgz"
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
	for _, key := range []string{
		"git-version@git+ssh://git@github.com/example/git-version.git#abc123",
		"file-version@file:../file-version.tgz",
		"legacy-git-version@github:example/legacy-git-version",
	} {
		if graph.Has(key) {
			t.Fatalf("%s should not be imported as a registry package: %#v", key, graph.Packages())
		}
	}
	if !graph.Has("registry-pkg@1.2.3") || !graph.Has("legacy-registry@2.0.0") {
		t.Fatalf("registry packages should still be imported: %#v", graph.Packages())
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

func TestLoadLockfileMirrorsOptionalPlatformPackagesAcrossHosts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "lockfileVersion": 3,
  "packages": {
    "": {"name": "root", "version": "1.0.0"},
    "node_modules/normal": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/normal/-/normal-1.0.0.tgz"
    },
    "node_modules/platform-linux-x64": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/platform-linux-x64/-/platform-linux-x64-1.0.0.tgz",
      "optional": true,
      "os": ["linux"],
      "cpu": ["x64"]
    },
    "node_modules/platform-darwin-arm64": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/platform-darwin-arm64/-/platform-darwin-arm64-1.0.0.tgz",
      "optional": true,
      "os": ["darwin"],
      "cpu": ["arm64"]
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
	for _, key := range []string{
		"normal@1.0.0",
		"platform-linux-x64@1.0.0",
		"platform-darwin-arm64@1.0.0",
	} {
		if !graph.Has(key) {
			t.Fatalf("lockfile mirror should include optional platform tarball %s: %#v", key, graph.Packages())
		}
	}
}

func TestLoadLockfileMirrorsLegacyOptionalPlatformDependencies(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "package-lock.json")
	data := []byte(`{
  "name": "root",
  "lockfileVersion": 1,
  "dependencies": {
    "legacy-platform-linux": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/legacy-platform-linux/-/legacy-platform-linux-1.0.0.tgz",
      "optional": true,
      "os": ["linux"],
      "cpu": ["x64"]
    },
    "legacy-platform-win": {
      "version": "1.0.0",
      "resolved": "https://registry.npmjs.org/legacy-platform-win/-/legacy-platform-win-1.0.0.tgz",
      "optional": true,
      "os": ["win32"],
      "cpu": ["x64"]
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
	if !graph.Has("legacy-platform-linux@1.0.0") || !graph.Has("legacy-platform-win@1.0.0") {
		t.Fatalf("legacy lockfile optional platform packages should be mirrored regardless host platform: %#v", graph.Packages())
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
