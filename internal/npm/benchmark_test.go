package npm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkResolveLargeRegistryTree(b *testing.B) {
	const packages = 250
	client := NewClient("https://registry.example.test")
	client.packuments = map[string]*Packument{}
	for i := 0; i < packages; i++ {
		name := fmt.Sprintf("pkg-%03d", i)
		deps := map[string]string{}
		if i+1 < packages {
			deps[fmt.Sprintf("pkg-%03d", i+1)] = "1.0.0"
		}
		client.packuments[name] = &Packument{
			Name:     name,
			DistTags: map[string]string{"latest": "1.0.0"},
			Versions: map[string]VersionManifest{
				"1.0.0": {
					Name:         name,
					Version:      "1.0.0",
					Dependencies: deps,
					Dist: struct {
						Tarball   string `json:"tarball"`
						Integrity string `json:"integrity"`
						Shasum    string `json:"shasum"`
					}{Tarball: fmt.Sprintf("https://registry.example.test/%s/-/%s-1.0.0.tgz", name, name)},
				},
			},
		}
	}
	deps := map[string]string{"pkg-000": "1.0.0"}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		resolver := &Resolver{Client: client, Options: ResolveOptions{IncludeOptional: true, ResolveConcurrency: 64}}
		graph, err := resolver.Resolve(context.Background(), deps)
		if err != nil {
			b.Fatal(err)
		}
		if len(graph.Packages()) != packages {
			b.Fatalf("got %d packages, want %d", len(graph.Packages()), packages)
		}
	}
}

func BenchmarkFetchLargeTarballSet(b *testing.B) {
	const packages = 250
	payload := []byte("benchmark tarball")
	integrity := sri(payload)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	defer srv.Close()

	pkgs := make([]Package, 0, packages)
	for i := 0; i < packages; i++ {
		name := fmt.Sprintf("pkg-%03d", i)
		pkgs = append(pkgs, Package{
			Name:      name,
			Version:   "1.0.0",
			Tarball:   fmt.Sprintf("%s/%s/-/%s-1.0.0.tgz", srv.URL, name, name),
			Integrity: integrity,
		})
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		dir := b.TempDir()
		report, err := FetchAll(context.Background(), NewClient(srv.URL), pkgs, FetchOptions{
			OutDir:      filepath.Join(dir, "tgzs"),
			StatePath:   filepath.Join(dir, ".gr", "state.json"),
			Concurrency: 64,
			MaxRetries:  0,
		})
		if err != nil {
			b.Fatal(err)
		}
		if report.Downloaded != packages {
			b.Fatalf("downloaded %d packages, want %d", report.Downloaded, packages)
		}
	}
}

func BenchmarkPublishLargeTarballSet(b *testing.B) {
	const packages = 100
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	tarballs := make(map[string][]byte, packages)
	for i := 0; i < packages; i++ {
		name := fmt.Sprintf("pkg-%03d", i)
		tarballs[name] = testPackageTarball(b, fmt.Sprintf(`{"name":%q,"version":"1.0.0"}`, name))
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		dir := b.TempDir()
		state := NewState()
		for name, data := range tarballs {
			path := filepath.Join(dir, name+"-1.0.0.tgz")
			if err := os.WriteFile(path, data, 0o644); err != nil {
				b.Fatal(err)
			}
			state.Local[name+"@1.0.0"] = StateRecord{Name: name, Version: "1.0.0", Path: path}
		}
		report, err := PublishAll(context.Background(), NewClient(srv.URL), state, PublishOptions{
			Concurrency: 32,
			MaxRetries:  0,
		})
		if err != nil {
			b.Fatal(err)
		}
		if report.Pushed != packages {
			b.Fatalf("pushed %d packages, want %d", report.Pushed, packages)
		}
	}
}
