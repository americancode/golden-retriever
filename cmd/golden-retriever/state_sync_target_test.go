package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"golden-retriever/internal/npm"
)

func TestStateSyncTargetWithoutInputsRebuildsFromRegistry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v4/projects/1/packages":
			_, _ = w.Write([]byte(`[{"name":"left-pad","version":"1.3.0","package_type":"npm"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	statePath := filepath.Join(t.TempDir(), "state.json")
	if err := run([]string{
		"state", "sync-target",
		"--state", statePath,
		"--target-registry", srv.URL + "/api/v4/projects/1/packages/npm",
		"--timeout", "2s",
	}); err != nil {
		t.Fatalf("run state sync-target: %v", err)
	}

	state, err := npm.LoadState(statePath)
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	if _, ok := state.Target["left-pad@1.3.0"]; !ok {
		t.Fatalf("expected rebuilt target entry, got %#v", state.Target)
	}
}
