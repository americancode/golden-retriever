package npm

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
	requireNPMParity(t)

	fixtures := realPackageJSONFixtures(t)
	filter := strings.TrimSpace(os.Getenv("NPM_PARITY_FIXTURE_FILTER"))
	for _, fixture := range fixtures {
		name := strings.TrimSuffix(filepath.Base(fixture), ".package.json")
		if filter != "" && !strings.Contains(name, filter) {
			continue
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "package.json")
			data, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(input, data, 0o644); err != nil {
				t.Fatal(err)
			}

			runNPMInstallPackageLockOnly(t, dir)

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

			if os.Getenv("NPM_PARITY_DISABLE_FETCH") == "1" {
				return
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

func TestNPMParityRealPackageJSONDependencyChunks(t *testing.T) {
	requireNPMParity(t)
	fixtures := realPackageJSONFixtures(t)
	filter := strings.TrimSpace(os.Getenv("NPM_PARITY_FIXTURE_FILTER"))
	chunkSize := 8
	if raw := strings.TrimSpace(os.Getenv("NPM_PARITY_CHUNK_SIZE")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			chunkSize = parsed
		}
	}

	type depSection struct {
		name string
		deps map[string]string
	}

	for _, fixture := range fixtures {
		fixtureName := strings.TrimSuffix(filepath.Base(fixture), ".package.json")
		if filter != "" && !strings.Contains(fixtureName, filter) {
			continue
		}
		data, err := os.ReadFile(fixture)
		if err != nil {
			t.Fatal(err)
		}
		var root packageJSON
		if err := json.Unmarshal(data, &root); err != nil {
			t.Fatal(err)
		}
		sections := []depSection{
			{name: "dependencies", deps: root.Dependencies},
			{name: "devDependencies", deps: root.DevDependencies},
			{name: "optionalDependencies", deps: root.OptionalDependencies},
		}
		for _, section := range sections {
			if len(section.deps) == 0 {
				continue
			}
			keys := sortedDependencyNames(section.deps)
			for start := 0; start < len(keys); start += chunkSize {
				end := start + chunkSize
				if end > len(keys) {
					end = len(keys)
				}
				chunk := map[string]string{}
				for _, dep := range keys[start:end] {
					chunk[dep] = section.deps[dep]
				}
				testName := fixtureName + "/" + section.name + "/" + strconv.Itoa(start) + "-" + strconv.Itoa(end-1)
				t.Run(testName, func(t *testing.T) {
					dir := t.TempDir()
					input := filepath.Join(dir, "package.json")
					doc := map[string]any{
						"name":    "parity-chunk",
						"version": "1.0.0",
						"private": true,
					}
					switch section.name {
					case "dependencies":
						doc["dependencies"] = chunk
					case "devDependencies":
						doc["devDependencies"] = chunk
					case "optionalDependencies":
						doc["optionalDependencies"] = chunk
					}
					payload, err := json.MarshalIndent(doc, "", "  ")
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(input, payload, 0o644); err != nil {
						t.Fatal(err)
					}

					runNPMInstallPackageLockOnly(t, dir)

					want := lockPackageSet(t, filepath.Join(dir, "package-lock.json"))
					graph, err := ResolvePackageJSON(context.Background(), NewClient("https://registry.npmjs.org"), input, ResolveOptions{
						IncludeDev: true, IncludeOptional: true,
					})
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
	}
}

func TestNPMParityWorkspaceVersionedSpecs(t *testing.T) {
	if os.Getenv("NPM_PARITY") != "1" {
		t.Skip("set NPM_PARITY=1 to run npm-backed parity test")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found")
	}

	t.Run("satisfied workspace versioned spec", func(t *testing.T) {
		dir := t.TempDir()
		workspaceDir := filepath.Join(dir, "packages", "app")
		if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "root",
  "version": "1.0.0",
  "private": true,
  "workspaces": ["packages/*"],
  "dependencies": {"app": "workspace:^1.0.0"}
}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceDir, "package.json"), []byte(`{
  "name": "app",
  "version": "1.2.0",
  "dependencies": {"is-number": "^7.0.0"}
}`), 0o644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--progress=false")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("npm install failed: %v\n%s", err, out)
		}

		want := lockPackageSet(t, filepath.Join(dir, "package-lock.json"))
		graph, err := ResolvePackageJSON(context.Background(), NewClient("https://registry.npmjs.org"), filepath.Join(dir, "package.json"), ResolveOptions{
			IncludeDev: true, IncludeOptional: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		got := resolvedPackageSet(graph.Packages())
		if !equalStringSlices(got.Keys, want.Keys) {
			missing, extra := stringSetDiff(want.Keys, got.Keys)
			t.Fatalf("package set mismatch missing=%v extra=%v", missing, extra)
		}
	})

	t.Run("unsatisfied workspace versioned spec", func(t *testing.T) {
		dir := t.TempDir()
		workspaceDir := filepath.Join(dir, "packages", "app")
		if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "root",
  "version": "1.0.0",
  "private": true,
  "workspaces": ["packages/*"],
  "dependencies": {"app": "workspace:^2.0.0"}
}`), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(workspaceDir, "package.json"), []byte(`{
  "name": "app",
  "version": "1.2.0"
}`), 0o644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--progress=false")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("npm install failed: %v\n%s", err, out)
		}

		want := lockPackageSet(t, filepath.Join(dir, "package-lock.json"))
		graph, err := ResolvePackageJSON(context.Background(), NewClient("https://registry.npmjs.org"), filepath.Join(dir, "package.json"), ResolveOptions{
			IncludeDev: true, IncludeOptional: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		got := resolvedPackageSet(graph.Packages())
		if !equalStringSlices(got.Keys, want.Keys) {
			missing, extra := stringSetDiff(want.Keys, got.Keys)
			t.Fatalf("package set mismatch missing=%v extra=%v", missing, extra)
		}
	})
}

func TestNPMParityFileSpecLocalDir(t *testing.T) {
	if os.Getenv("NPM_PARITY") != "1" {
		t.Skip("set NPM_PARITY=1 to run npm-backed parity test")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found")
	}

	dir := t.TempDir()
	localDir := filepath.Join(dir, "local")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "root",
  "version": "1.0.0",
  "private": true,
  "dependencies": {"local": "file:./local"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "package.json"), []byte(`{
  "name": "local",
  "version": "1.0.0",
  "dependencies": {"is-number": "^7.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--progress=false")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm install failed: %v\n%s", err, out)
	}

	want := lockPackageSet(t, filepath.Join(dir, "package-lock.json"))
	graph, err := ResolvePackageJSON(context.Background(), NewClient("https://registry.npmjs.org"), filepath.Join(dir, "package.json"), ResolveOptions{
		IncludeDev: true, IncludeOptional: true,
	})
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
}

func TestNPMParityLinkSpecLocalDir(t *testing.T) {
	if os.Getenv("NPM_PARITY") != "1" {
		t.Skip("set NPM_PARITY=1 to run npm-backed parity test")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found")
	}

	dir := t.TempDir()
	localDir := filepath.Join(dir, "local")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "root",
  "version": "1.0.0",
  "private": true,
  "dependencies": {"local": "link:./local"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "package.json"), []byte(`{
  "name": "local",
  "version": "1.0.0",
  "dependencies": {"is-number": "^7.0.0"}
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--progress=false")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm install failed: %v\n%s", err, out)
	}

	want := lockPackageSet(t, filepath.Join(dir, "package-lock.json"))
	graph, err := ResolvePackageJSON(context.Background(), NewClient("https://registry.npmjs.org"), filepath.Join(dir, "package.json"), ResolveOptions{
		IncludeDev: true, IncludeOptional: true,
	})
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
}

func TestNPMParityRemoteTarballSpec(t *testing.T) {
	if os.Getenv("NPM_PARITY") != "1" {
		t.Skip("set NPM_PARITY=1 to run npm-backed parity test")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "root",
  "version": "1.0.0",
  "private": true,
  "dependencies": {
    "left-pad": "https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz"
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--progress=false")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm install failed: %v\n%s", err, out)
	}

	want := lockPackageSet(t, filepath.Join(dir, "package-lock.json"))
	graph, err := ResolvePackageJSON(context.Background(), NewClient("https://registry.npmjs.org"), filepath.Join(dir, "package.json"), ResolveOptions{
		IncludeDev: true, IncludeOptional: true,
	})
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
}

func TestNPMParityGitAndHostedSpecs(t *testing.T) {
	if os.Getenv("NPM_PARITY") != "1" {
		t.Skip("set NPM_PARITY=1 to run npm-backed parity test")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found")
	}

	fixtures := []struct {
		name string
		spec string
	}{
		{name: "git+https", spec: "git+https://github.com/isaacs/node-glob.git#v10.5.0"},
		{name: "github-hosted", spec: "github:isaacs/node-glob#v10.5.0"},
		{name: "ssh", spec: "git+ssh://git@github.com/isaacs/node-glob.git#v10.5.0"},
		{name: "svn", spec: "svn+https://github.com/isaacs/node-glob/trunk"},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "root",
  "version": "1.0.0",
  "private": true,
  "dependencies": {"glob-src": "`+fixture.spec+`"}
}`), 0o644); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--progress=false")
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Skipf("npm fixture unavailable in this environment: %v\n%s", err, out)
			}

			want := lockPackageSet(t, filepath.Join(dir, "package-lock.json"))
			graph, err := ResolvePackageJSON(context.Background(), NewClient("https://registry.npmjs.org"), filepath.Join(dir, "package.json"), ResolveOptions{
				IncludeDev: true, IncludeOptional: true,
			})
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

func TestNPMParityPlatformFiltersRootAndTransitive(t *testing.T) {
	if os.Getenv("NPM_PARITY") != "1" {
		t.Skip("set NPM_PARITY=1 to run npm-backed parity test")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found")
	}

	t.Run("root platform-filtered optional package", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "root",
  "version": "1.0.0",
  "private": true,
  "optionalDependencies": {
    "fsevents": "2.3.3"
  }
}`), 0o644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--progress=false")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("npm install failed: %v\n%s", err, out)
		}

		want := lockPackageSet(t, filepath.Join(dir, "package-lock.json"))
		graph, err := ResolvePackageJSON(context.Background(), NewClient("https://registry.npmjs.org"), filepath.Join(dir, "package.json"), ResolveOptions{
			IncludeDev: true, IncludeOptional: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		got := resolvedPackageSet(graph.Packages())
		if !equalStringSlices(got.Keys, want.Keys) {
			missing, extra := stringSetDiff(want.Keys, got.Keys)
			t.Fatalf("package set mismatch missing=%v extra=%v", missing, extra)
		}
	})

	t.Run("transitive platform-filtered optional package", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "root",
  "version": "1.0.0",
  "private": true,
  "dependencies": {
    "chokidar": "3.6.0"
  }
}`), 0o644); err != nil {
			t.Fatal(err)
		}

		cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--progress=false")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("npm install failed: %v\n%s", err, out)
		}

		want := lockPackageSet(t, filepath.Join(dir, "package-lock.json"))
		graph, err := ResolvePackageJSON(context.Background(), NewClient("https://registry.npmjs.org"), filepath.Join(dir, "package.json"), ResolveOptions{
			IncludeDev: true, IncludeOptional: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		got := resolvedPackageSet(graph.Packages())
		if !equalStringSlices(got.Keys, want.Keys) {
			missing, extra := stringSetDiff(want.Keys, got.Keys)
			t.Fatalf("package set mismatch missing=%v extra=%v", missing, extra)
		}
	})
}

func TestNPMParityPlatformFiltersOSCPUAndLibcCombinations(t *testing.T) {
	if os.Getenv("NPM_PARITY") != "1" {
		t.Skip("set NPM_PARITY=1 to run npm-backed parity test")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found")
	}

	fixtures := []struct {
		name         string
		dependencies string
	}{
		{
			name: "os-and-cpu-combo",
			dependencies: `"optionalDependencies": {
    "@esbuild/linux-x64": "0.21.5",
    "@esbuild/darwin-arm64": "0.21.5"
  }`,
		},
		{
			name: "libc-combo",
			dependencies: `"optionalDependencies": {
    "@rollup/rollup-linux-x64-gnu": "4.18.0",
    "@rollup/rollup-linux-x64-musl": "4.18.0"
  }`,
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{
  "name": "root",
  "version": "1.0.0",
  "private": true,
  `+fixture.dependencies+`
}`), 0o644); err != nil {
				t.Fatal(err)
			}

			cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--progress=false")
			cmd.Dir = dir
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("npm install failed: %v\n%s", err, out)
			}

			want := lockPackageSet(t, filepath.Join(dir, "package-lock.json"))
			graph, err := ResolvePackageJSON(context.Background(), NewClient("https://registry.npmjs.org"), filepath.Join(dir, "package.json"), ResolveOptions{
				IncludeDev: true, IncludeOptional: true,
			})
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

func TestNPMParityDeprecationMetadataFixtures(t *testing.T) {
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
			name: "deprecated-exact",
			dependencies: map[string]string{
				"request": "2.88.2",
			},
		},
		{
			name: "deprecated-range",
			dependencies: map[string]string{
				"request": "^2.0.0",
			},
		},
		{
			name: "non-deprecated-control",
			dependencies: map[string]string{
				"is-number": "^7.0.0",
			},
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			dir := t.TempDir()
			input := filepath.Join(dir, "package.json")
			data, err := json.MarshalIndent(map[string]any{
				"name":         "parity-deprecation-fixture",
				"version":      "1.0.0",
				"private":      true,
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

			wantPkgs := lockPackageSet(t, filepath.Join(dir, "package-lock.json"))
			wantDeprecations := lockDeprecatedSet(t, filepath.Join(dir, "package-lock.json"))
			graph, err := ResolvePackageJSON(context.Background(), NewClient("https://registry.npmjs.org"), input, ResolveOptions{
				IncludeDev: true, IncludeOptional: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			gotPkgs := resolvedPackageSet(graph.Packages())
			if !equalStringSlices(gotPkgs.Keys, wantPkgs.Keys) {
				missing, extra := stringSetDiff(wantPkgs.Keys, gotPkgs.Keys)
				t.Fatalf("package set mismatch missing=%v extra=%v", missing, extra)
			}
			gotDeprecations := resolvedDeprecationSet(graph)
			if !equalStringSlices(gotDeprecations, wantDeprecations) {
				missing, extra := stringSetDiff(wantDeprecations, gotDeprecations)
				t.Fatalf("deprecation set mismatch missing=%v extra=%v", missing, extra)
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
		if !strings.Contains(loc, "node_modules/") {
			continue
		}
		if strings.HasPrefix(pkg.Resolved, "file:") || strings.HasPrefix(pkg.Resolved, "link:") {
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

func lockDeprecatedSet(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Packages map[string]struct {
			Name       string `json:"name"`
			Version    string `json:"version"`
			Deprecated string `json:"deprecated"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	out := []string{}
	seen := map[string]bool{}
	for loc, pkg := range lock.Packages {
		if loc == "" || pkg.Version == "" || pkg.Deprecated == "" {
			continue
		}
		if !strings.Contains(loc, "node_modules/") {
			continue
		}
		name := pkg.Name
		if name == "" {
			name = nameFromNodeModulesPath(loc)
		}
		key := name + "@" + pkg.Version
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func resolvedDeprecationSet(graph *Graph) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, warning := range graph.DeprecationWarnings {
		if warning.Package == "" {
			continue
		}
		if !seen[warning.Package] {
			seen[warning.Package] = true
			out = append(out, warning.Package)
		}
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

func requireNPMParity(t *testing.T) {
	t.Helper()
	if os.Getenv("NPM_PARITY") != "1" {
		t.Skip("set NPM_PARITY=1 to run npm-backed parity test")
	}
	if _, err := exec.LookPath("npm"); err != nil {
		t.Skip("npm not found")
	}
}

func runNPMInstallPackageLockOnly(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund", "--progress=false")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("npm install failed: %v\n%s", err, out)
	}
}
