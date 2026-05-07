package npm

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

func TestNPMParityTarballSet(t *testing.T) {
	if os.Getenv("NPM_PARITY") != "1" {
		t.Skip("set NPM_PARITY=1 to run npm-backed parity test")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found")
	}

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{
  "name": "parity-fixture",
  "version": "1.0.0",
  "dependencies": {
    "is-number": "^7.0.0",
    "left-pad": "1.3.0"
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm install failed: %v\n%s", err, out)
	}

	lockSet := lockTarballSet(t, filepath.Join(dir, "package-lock.json"))
	client := NewClient("https://registry.npmjs.org")
	graph, err := ResolvePackageJSON(context.Background(), client, input, ResolveOptions{IncludeDev: true, IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	got := packageTarballSet(graph.Packages())
	if !equalStringSlices(got, lockSet) {
		t.Fatalf("tarball set mismatch\ngot:  %v\nwant: %v", got, lockSet)
	}
}

func lockTarballSet(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Packages map[string]struct {
			Resolved string `json:"resolved"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	var out []string
	for loc, pkg := range lock.Packages {
		if loc != "" && pkg.Resolved != "" {
			out = append(out, pkg.Resolved)
		}
	}
	sort.Strings(out)
	return out
}

func packageTarballSet(pkgs []Package) []string {
	out := make([]string, 0, len(pkgs))
	for _, pkg := range pkgs {
		out = append(out, pkg.Tarball)
	}
	sort.Strings(out)
	return out
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
