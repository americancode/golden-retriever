package npm

import (
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
