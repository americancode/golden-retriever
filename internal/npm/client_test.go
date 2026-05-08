package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientCoalescesPackumentFetches(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		time.Sleep(25 * time.Millisecond)
		fmt.Fprint(w, `{"name":"demo","dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{"name":"demo","version":"1.0.0"}}}`)
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.Packument(context.Background(), "demo"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("registry hits = %d, want 1", got)
	}
}

func TestClientUsesPackumentCacheOffline(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		fmt.Fprint(w, `{"name":"demo","dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{"name":"demo","version":"1.0.0"}}}`)
	}))
	defer srv.Close()

	cacheDir := filepath.Join(t.TempDir(), "metadata")
	client := NewClient(srv.URL)
	client.CacheDir = cacheDir
	if _, err := client.Packument(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}

	offline := NewClient(srv.URL)
	offline.CacheDir = cacheDir
	offline.Offline = true
	pack, err := offline.Packument(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if pack.Name != "demo" {
		t.Fatalf("cached packument name = %s", pack.Name)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("registry hits = %d, want 1", got)
	}
}

func TestClientAppliesScopedRegistryAndAuth(t *testing.T) {
	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		if r.URL.EscapedPath() != "/@scope%2Fpkg" {
			t.Errorf("path = %s", r.URL.EscapedPath())
		}
		fmt.Fprint(w, `{"name":"@scope/pkg","dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{"name":"@scope/pkg","version":"1.0.0"}}}`)
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.ScopeRegistries["@scope"] = srv.URL
	cfg.values[nerfDart(srv.URL)+":_authToken"] = "secret"
	client := NewClientWithConfig(cfg)
	if _, err := client.Packument(context.Background(), "@scope/pkg"); err != nil {
		t.Fatal(err)
	}
	if authHeader != "Bearer secret" {
		t.Fatalf("auth header = %s", authHeader)
	}
}

func TestClientIgnoresNonObjectEnginesMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
  "name": "demo",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "demo",
      "version": "1.0.0",
      "engines": ["node >= 18"]
    }
  }
}`)
	}))
	defer srv.Close()

	pack, err := NewClient(srv.URL).Packument(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(pack.Versions["1.0.0"].Engines) != 0 {
		t.Fatalf("array engines metadata should be ignored: %#v", pack.Versions["1.0.0"].Engines)
	}
}

func TestClientRevalidatesStalePackumentWithETag(t *testing.T) {
	var ifNoneMatch string
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		ifNoneMatch = r.Header.Get("If-None-Match")
		if ifNoneMatch == `"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"abc"`)
		fmt.Fprint(w, `{"name":"demo","dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{"name":"demo","version":"1.0.0"}}}`)
	}))
	defer srv.Close()

	cacheDir := filepath.Join(t.TempDir(), "metadata")
	client := NewClient(srv.URL)
	client.CacheDir = cacheDir
	if _, err := client.Packument(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}

	stale := NewClient(srv.URL)
	stale.CacheDir = cacheDir
	stale.CacheTTL = 0
	pack, err := stale.Packument(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if pack.Name != "demo" {
		t.Fatalf("name = %s", pack.Name)
	}
	if ifNoneMatch != `"abc"` {
		t.Fatalf("If-None-Match = %s", ifNoneMatch)
	}
	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Fatalf("hits = %d, want 2", got)
	}
}

func TestClientRetriesTransientPackumentFailure(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&hits, 1) == 1 {
			http.Error(w, "temporary", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"name":"demo","dist-tags":{"latest":"1.0.0"},"versions":{"1.0.0":{"name":"demo","version":"1.0.0"}}}`)
	}))
	defer srv.Close()

	client := NewClient(srv.URL)
	client.PackumentRetries = 2
	if _, err := client.Packument(context.Background(), "demo"); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Fatalf("hits = %d, want 2", got)
	}
}

func TestClientUsesStalePackumentOnTransientFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporary", http.StatusInternalServerError)
	}))
	defer srv.Close()

	cacheDir := filepath.Join(t.TempDir(), "metadata")
	client := NewClient(srv.URL)
	client.CacheDir = cacheDir
	client.CacheTTL = 0
	client.PackumentRetries = 0
	cached := &cachedPackument{
		Registry: srv.URL,
		Name:     "demo",
		CachedAt: time.Now().Add(-time.Hour),
		ETag:     `"stale"`,
		Packument: Packument{
			Name:     "demo",
			DistTags: map[string]string{"latest": "1.0.0"},
			Versions: map[string]VersionManifest{"1.0.0": {Name: "demo", Version: "1.0.0"}},
		},
	}
	data, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	path := client.cachePath("demo", srv.URL)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	pack, err := client.Packument(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if pack.Name != "demo" {
		t.Fatalf("name = %s", pack.Name)
	}
}
