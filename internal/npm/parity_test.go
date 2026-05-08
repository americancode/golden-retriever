package npm

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
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

			cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--progress=false")
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
				missing, extra := stringSetDiff(want.Keys, got.Keys)
				t.Fatalf("package set mismatch missing=%v extra=%v", missing, extra)
			}
			if !equalStringSlices(got.Tarballs, want.Tarballs) {
				missing, extra := stringSetDiff(want.Tarballs, got.Tarballs)
				t.Fatalf("tarball set mismatch missing=%v extra=%v", missing, extra)
			}
		})
	}
}

func TestNPMParityRealPackageJSONFixtures(t *testing.T) {
	if os.Getenv("NPM_PARITY") != "1" {
		t.Skip("set NPM_PARITY=1 to run npm-backed parity test")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found")
	}

	fixtures := realPackageJSONFixtures(t)
	for _, fixture := range fixtures {
		t.Run(strings.TrimSuffix(filepath.Base(fixture), ".package.json"), func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "package.json")
			data, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(input, data, 0o644); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--progress=false")
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
				missing, extra := stringSetDiff(want.Keys, got.Keys)
				t.Fatalf("package set mismatch missing=%v extra=%v", missing, extra)
			}
			if !equalStringSlices(got.Tarballs, want.Tarballs) {
				missing, extra := stringSetDiff(want.Tarballs, got.Tarballs)
				t.Fatalf("tarball set mismatch missing=%v extra=%v", missing, extra)
			}

			outDir := filepath.Join(dir, "tgzs")
			statePath := filepath.Join(dir, "state.json")
			report, err := FetchAll(context.Background(), client, graph.Packages(), FetchOptions{
				OutDir:      outDir,
				StatePath:   statePath,
				Concurrency: 16,
				MaxRetries:  2,
			})
			if err != nil {
				t.Fatalf("fetch failed: %v report=%+v", err, report)
			}
			if report.Downloaded != len(got.Keys) {
				t.Fatalf("downloaded %d tgzs, want %d; report=%+v", report.Downloaded, len(got.Keys), report)
			}
			files, err := filepath.Glob(filepath.Join(outDir, "*.tgz"))
			if err != nil {
				t.Fatal(err)
			}
			if len(files) != len(got.Keys) {
				t.Fatalf("tgz file count = %d, want %d", len(files), len(got.Keys))
			}
		})
	}
}

func realPackageJSONFixtures(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "test", "package-jsons", "*.package.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no real package.json fixtures found under test/package-jsons")
	}
	sort.Strings(paths)
	return paths
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
	keys := map[string]bool{}
	tarballs := map[string]bool{}
	for loc, pkg := range lock.Packages {
		if loc == "" || pkg.Version == "" {
			continue
		}
		name := pkg.Name
		if name == "" {
			name = nameFromNodeModulesPath(loc)
		}
		key := name + "@" + pkg.Version
		if !keys[key] {
			keys[key] = true
			out.Keys = append(out.Keys, key)
		}
		if pkg.Resolved != "" && !tarballs[pkg.Resolved] {
			tarballs[pkg.Resolved] = true
			out.Tarballs = append(out.Tarballs, pkg.Resolved)
		}
	}
	sort.Strings(out.Keys)
	sort.Strings(out.Tarballs)
	return out
}

func resolvedPackageSet(pkgs []Package) parityPackageSet {
	var out parityPackageSet
	keys := map[string]bool{}
	tarballs := map[string]bool{}
	for _, pkg := range pkgs {
		if key := pkg.Key(); !keys[key] {
			keys[key] = true
			out.Keys = append(out.Keys, key)
		}
		if pkg.Tarball != "" && !tarballs[pkg.Tarball] {
			tarballs[pkg.Tarball] = true
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

func stringSetDiff(want, got []string) ([]string, []string) {
	wantSet := map[string]bool{}
	gotSet := map[string]bool{}
	for _, item := range want {
		wantSet[item] = true
	}
	for _, item := range got {
		gotSet[item] = true
	}
	var missing []string
	for _, item := range want {
		if !gotSet[item] {
			missing = append(missing, item)
		}
	}
	var extra []string
	for _, item := range got {
		if !wantSet[item] {
			extra = append(extra, item)
		}
	}
	return missing, extra
}
