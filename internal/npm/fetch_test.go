package npm

import (
	"context"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAndFetchFromMockRegistry(t *testing.T) {
	alphaTGZ := []byte("alpha tarball")
	betaTGZ := []byte("beta tarball")
	alphaIntegrity := sri(alphaTGZ)
	betaIntegrity := sri(betaTGZ)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/alpha":
			fmt.Fprintf(w, `{
  "name": "alpha",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "alpha",
      "version": "1.0.0",
      "dependencies": {"beta": "^2.0.0"},
      "dist": {"tarball": "%s/alpha/-/alpha-1.0.0.tgz", "integrity": "%s"}
    }
  }
}`, serverURL(r), alphaIntegrity)
		case "/beta":
			fmt.Fprintf(w, `{
  "name": "beta",
  "dist-tags": {"latest": "2.1.0"},
  "versions": {
    "2.0.0": {
      "name": "beta",
      "version": "2.0.0",
      "dist": {"tarball": "%s/beta/-/beta-2.0.0.tgz"}
    },
    "2.1.0": {
      "name": "beta",
      "version": "2.1.0",
      "dist": {"tarball": "%s/beta/-/beta-2.1.0.tgz", "integrity": "%s"}
    }
  }
}`, serverURL(r), serverURL(r), betaIntegrity)
		case "/alpha/-/alpha-1.0.0.tgz":
			w.Write(alphaTGZ)
		case "/beta/-/beta-2.1.0.tgz":
			w.Write(betaTGZ)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"dependencies":{"alpha":"latest"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	client := NewClient(srv.URL)
	graph, err := ResolvePackageJSON(context.Background(), client, input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Packages()) != 2 {
		t.Fatalf("got %d packages, want 2", len(graph.Packages()))
	}

	report, err := FetchAll(context.Background(), client, graph.Packages(), FetchOptions{
		OutDir:      filepath.Join(dir, "tgzs"),
		StatePath:   filepath.Join(dir, ".gr", "state.json"),
		Concurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Downloaded != 2 || report.Skipped != 0 || report.Failed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}

	report, err = FetchAll(context.Background(), client, graph.Packages(), FetchOptions{
		OutDir:      filepath.Join(dir, "tgzs"),
		StatePath:   filepath.Join(dir, ".gr", "state.json"),
		Concurrency: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Downloaded != 0 || report.Skipped != 2 || report.Failed != 0 {
		t.Fatalf("unexpected cached report: %#v", report)
	}
}

func TestResolveAliasSpecFromMockRegistry(t *testing.T) {
	tgz := []byte("real tarball")
	integrity := sri(tgz)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/real":
			fmt.Fprintf(w, `{
  "name": "real",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "2.0.0": {
      "name": "real",
      "version": "2.0.0",
      "dist": {"tarball": "%s/real/-/real-2.0.0.tgz", "integrity": "%s"}
    }
  }
}`, serverURL(r), integrity)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"dependencies":{"alias":"npm:real@^2.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	pkgs := graph.Packages()
	if len(pkgs) != 1 || pkgs[0].Name != "real" || pkgs[0].Version != "2.0.0" {
		t.Fatalf("unexpected packages: %#v", pkgs)
	}
}

func sri(data []byte) string {
	sum := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
