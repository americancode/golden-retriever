package npm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestSyncTargetMarksPresentAndMissingPackages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/present":
			fmt.Fprintf(w, `{
  "name": "present",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "present",
      "version": "1.0.0",
      "dist": {
        "tarball": "%s/present/-/present-1.0.0.tgz",
        "integrity": "sha512-present"
      }
    }
  }
}`, serverURL(r))
		case "/missing-version":
			fmt.Fprint(w, `{
  "name": "missing-version",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "2.0.0": {"name": "missing-version", "version": "2.0.0"}
  }
}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	state := NewState()
	report, err := SyncTarget(context.Background(), NewClient(srv.URL), state, []Package{
		{Name: "present", Version: "1.0.0", Tarball: "https://source/present.tgz", Integrity: "sha512-source"},
		{Name: "missing-version", Version: "1.0.0"},
		{Name: "missing-package", Version: "1.0.0"},
	}, SyncTargetOptions{Concurrency: 3, Source: "test-target"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Present != 1 || report.Missing != 2 || report.Failed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	rec := state.Target["present@1.0.0"]
	if rec.Name != "present" || rec.Version != "1.0.0" || rec.Source != "test-target" {
		t.Fatalf("unexpected target record: %#v", rec)
	}
	if rec.Tarball == "https://source/present.tgz" {
		t.Fatalf("target record should use target registry tarball when available: %#v", rec)
	}
}

func TestSyncTargetUsesScopedRegistryAuth(t *testing.T) {
	const token = "scoped-secret"
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+token {
			sawAuth = true
		}
		switch r.URL.EscapedPath() {
		case "/@scope%2Fpresent":
			fmt.Fprintf(w, `{
  "name": "@scope/present",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "@scope/present",
      "version": "1.0.0",
      "dist": {
        "tarball": "%s/@scope/present/-/present-1.0.0.tgz",
        "integrity": "sha512-present"
      }
    }
  }
}`, serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.ScopeRegistries["@scope"] = srv.URL
	cfg.values[nerfDart(srv.URL+"/")+":_authToken"] = token
	state := NewState()
	report, err := SyncTarget(context.Background(), NewClientWithConfig(cfg), state, []Package{
		{Name: "@scope/present", Version: "1.0.0"},
	}, SyncTargetOptions{Concurrency: 1, Source: "scoped-target"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Present != 1 || report.Missing != 0 || report.Failed != 0 || !sawAuth {
		t.Fatalf("report=%#v sawAuth=%v", report, sawAuth)
	}
	if state.Target["@scope/present@1.0.0"].Source != "scoped-target" {
		t.Fatalf("target not marked: %#v", state.Target)
	}
}

func TestSyncTargetReportsPartialFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/present":
			fmt.Fprintf(w, `{
  "name": "present",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {"name": "present", "version": "1.0.0"}
  }
}`)
		case "/broken":
			http.Error(w, "broken", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	state := NewState()
	report, err := SyncTarget(context.Background(), NewClient(srv.URL), state, []Package{
		{Name: "present", Version: "1.0.0"},
		{Name: "missing", Version: "1.0.0"},
		{Name: "broken", Version: "1.0.0"},
	}, SyncTargetOptions{Concurrency: 2, Source: "partial"})
	if err == nil {
		t.Fatalf("partial sync should return first registry error")
	}
	if report.Present != 1 || report.Missing != 1 || report.Failed != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if state.Target["present@1.0.0"].Source != "partial" {
		t.Fatalf("successful package should still be marked: %#v", state.Target)
	}
}

func TestSyncTargetRetriesTransientPackumentFailure(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&hits, 1) == 1 {
			http.Error(w, "temporary", http.StatusTooManyRequests)
			return
		}
		fmt.Fprintf(w, `{
  "name": "present",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {"name": "present", "version": "1.0.0"}
  }
}`)
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	client.PackumentRetries = 1
	state := NewState()
	report, err := SyncTarget(context.Background(), client, state, []Package{
		{Name: "present", Version: "1.0.0"},
	}, SyncTargetOptions{Concurrency: 1, Source: "retry"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Present != 1 || atomic.LoadInt64(&hits) != 2 {
		t.Fatalf("report=%#v hits=%d", report, hits)
	}
}

func TestRebuildTargetFromGitLabRegistry(t *testing.T) {
	const token = "target-secret"
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+token {
			sawAuth = true
		}
		switch r.URL.Path {
		case "/api/v4/projects/1/packages":
			switch r.URL.Query().Get("page") {
			case "1":
				w.Header().Set("X-Next-Page", "2")
				fmt.Fprint(w, `[
{"name":"left-pad","version":"1.3.0","package_type":"npm"},
{"name":"@types/node","version":"24.10.1","package_type":"npm"}
]`)
			case "2":
				fmt.Fprint(w, `[
{"name":"left-pad","version":"1.3.0","package_type":"npm"},
{"name":"undici-types","version":"7.16.0","package_type":"npm"}
]`)
			default:
				t.Fatalf("unexpected page: %s", r.URL.RawQuery)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.Registry = srv.URL + "/api/v4/projects/1/packages/npm"
	cfg.values[nerfDart(cfg.Registry+"/")+":_authToken"] = token
	client := NewClientWithConfig(cfg)
	client.Registry = cfg.Registry
	state := NewState()
	state.Target["stale@0.0.1"] = StateRecord{Name: "stale", Version: "0.0.1"}

	report, err := RebuildTargetFromRegistry(context.Background(), client, state, SyncTargetOptions{Source: "gitlab-rebuild"})
	if err != nil {
		t.Fatal(err)
	}
	if !sawAuth {
		t.Fatalf("expected auth header on GitLab listing request")
	}
	if report.Present != 3 || report.Missing != 0 || report.Failed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if _, ok := state.Target["stale@0.0.1"]; ok {
		t.Fatalf("stale target entry should have been replaced during rebuild")
	}
	for _, key := range []string{"left-pad@1.3.0", "@types/node@24.10.1", "undici-types@7.16.0"} {
		rec, ok := state.Target[key]
		if !ok {
			t.Fatalf("missing rebuilt target record for %s", key)
		}
		if rec.Source != "gitlab-rebuild" {
			t.Fatalf("unexpected source for %s: %#v", key, rec)
		}
	}
}
