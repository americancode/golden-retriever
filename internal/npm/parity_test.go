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

func TestNPMParityFixtures(t *testing.T) {
	if os.Getenv("NPM_PARITY") != "1" {
		t.Skip("set NPM_PARITY=1 to run npm-backed parity test")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found")
	}

	fixtures := []struct {
		name         string
		dependencies map[string]string
	}{
		{
			name: "basic exact and range",
			dependencies: map[string]string{
				"is-number": "^7.0.0",
				"left-pad":  "1.3.0",
			},
		},
		{
			name: "alias and scoped",
			dependencies: map[string]string{
				"lp":               "npm:left-pad@1.3.0",
				"@isaacs/ttlcache": "^1.4.1",
			},
		},
		{
			name: "dist tag and hyphen range",
			dependencies: map[string]string{
				"is-number":      "latest",
				"balanced-match": "1.0.0 - 1.0.2",
			},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "package.json")
			data, err := json.MarshalIndent(map[string]any{
				"name":         "parity-fixture",
				"version":      "1.0.0",
				"dependencies": fixture.dependencies,
			}, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(input, data, 0o644); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts")
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("npm install failed: %v\n%s", err, out)
			}

			want := lockPackageSet(t, filepath.Join(dir, "package-lock.json"))
			client := NewClient("https://registry.npmjs.org")
			graph, err := ResolvePackageJSON(context.Background(), client, input, ResolveOptions{IncludeDev: true, IncludeOptional: true})
			if err != nil {
				t.Fatal(err)
			}
			got := resolvedPackageSet(graph.Packages())
			if !equalStringSlices(got.Keys, want.Keys) {
				t.Fatalf("package set mismatch\ngot:  %v\nwant: %v", got.Keys, want.Keys)
			}
			if !equalStringSlices(got.Tarballs, want.Tarballs) {
				t.Fatalf("tarball set mismatch\ngot:  %v\nwant: %v", got.Tarballs, want.Tarballs)
			}
		})
	}
}

type parityPackageSet struct {
	Keys     []string
	Tarballs []string
}

func lockPackageSet(t *testing.T, path string) parityPackageSet {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Packages map[string]struct {
			Name     string `json:"name"`
			Version  string `json:"version"`
			Resolved string `json:"resolved"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	var out parityPackageSet
	for loc, pkg := range lock.Packages {
		if loc == "" || pkg.Version == "" {
			continue
		}
		name := pkg.Name
		if name == "" {
			name = nameFromNodeModulesPath(loc)
		}
		out.Keys = append(out.Keys, name+"@"+pkg.Version)
		if pkg.Resolved != "" {
			out.Tarballs = append(out.Tarballs, pkg.Resolved)
		}
	}
	sort.Strings(out.Keys)
	sort.Strings(out.Tarballs)
	return out
}

func resolvedPackageSet(pkgs []Package) parityPackageSet {
	var out parityPackageSet
	for _, pkg := range pkgs {
		out.Keys = append(out.Keys, pkg.Key())
		if pkg.Tarball != "" {
			out.Tarballs = append(out.Tarballs, pkg.Tarball)
		}
	}
	sort.Strings(out.Keys)
	sort.Strings(out.Tarballs)
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
