package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestPublishAllPublishesLocalTarballAndMarksTarget(t *testing.T) {
	tgz := testPackageTarball(t, `{"name":"demo","version":"1.0.0","description":"demo package"}`)
	dir := t.TempDir()
	tgzPath := filepath.Join(dir, "demo-1.0.0.tgz")
	if err := os.WriteFile(tgzPath, tgz, 0o644); err != nil {
		t.Fatal(err)
	}

	var authHeader string
	var userAgent string
	var npmCommand string
	var npmAuthType string
	var publishPath string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		userAgent = r.Header.Get("User-Agent")
		npmCommand = r.Header.Get("npm-command")
		npmAuthType = r.Header.Get("npm-auth-type")
		publishPath = r.URL.EscapedPath()
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.Registry = srv.URL
	cfg.values[nerfDart(srv.URL)+":_authToken"] = "secret"
	client := NewClientWithConfig(cfg)
	state := NewState()
	state.Local["demo@1.0.0"] = StateRecord{Name: "demo", Version: "1.0.0", Path: tgzPath}

	report, err := PublishAll(context.Background(), client, state, PublishOptions{Concurrency: 2, Source: "test-registry"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Pushed != 1 || report.Present != 0 || report.Failed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if authHeader != "Bearer secret" {
		t.Fatalf("auth header = %s", authHeader)
	}
	if userAgent == "" {
		t.Fatalf("user-agent missing")
	}
	if npmCommand != "publish" {
		t.Fatalf("npm-command = %q", npmCommand)
	}
	if npmAuthType != "legacy" {
		t.Fatalf("npm-auth-type = %q", npmAuthType)
	}
	if publishPath != "/demo" {
		t.Fatalf("publish path = %s", publishPath)
	}
	if body["_id"] != "demo" {
		t.Fatalf("body _id = %#v", body["_id"])
	}
	if got := body["access"]; got != "public" {
		t.Fatalf("access = %#v, want public", got)
	}
	attachments := body["_attachments"].(map[string]any)
	attachment := attachments["demo-1.0.0.tgz"].(map[string]any)
	if attachment["data"] == "" {
		t.Fatalf("attachment missing data")
	}
	if _, err := base64.StdEncoding.DecodeString(attachment["data"].(string)); err != nil {
		t.Fatal(err)
	}
	if state.Target["demo@1.0.0"].Source != "test-registry" {
		t.Fatalf("target not marked: %#v", state.Target)
	}
}

func TestBuildPublishDocumentRewritesHTTPDistTarball(t *testing.T) {
	manifest := publishManifest{
		Name:    "demo",
		Version: "1.0.0",
		Raw: map[string]any{
			"name":    "demo",
			"version": "1.0.0",
		},
	}
	doc, _, err := buildPublishDocument("https://registry.example.test", manifest, testPackageTarball(t, `{"name":"demo","version":"1.0.0"}`), PublishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	versions := doc["versions"].(map[string]any)
	v := versions["1.0.0"].(map[string]any)
	dist := v["dist"].(map[string]any)
	tarball := dist["tarball"].(string)
	if tarball != "http://registry.example.test/demo/-/demo-1.0.0.tgz" {
		t.Fatalf("unexpected tarball url: %s", tarball)
	}
}

func TestPublishAllTreatsConflictAsPresent(t *testing.T) {
	tgz := testPackageTarball(t, `{"name":"demo","version":"1.0.0"}`)
	dir := t.TempDir()
	tgzPath := filepath.Join(dir, "demo-1.0.0.tgz")
	if err := os.WriteFile(tgzPath, tgz, 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	state := NewState()
	state.Local["demo@1.0.0"] = StateRecord{Name: "demo", Version: "1.0.0", Path: tgzPath}
	report, err := PublishAll(context.Background(), NewClient(srv.URL), state, PublishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Present != 1 || report.Pushed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if state.Target["demo@1.0.0"].Name != "demo" {
		t.Fatalf("target not marked on conflict: %#v", state.Target)
	}
}

func TestPublishAllTreatsGitLabAlreadyExistsAsPresent(t *testing.T) {
	tgz := testPackageTarball(t, `{"name":"demo","version":"1.0.0"}`)
	dir := t.TempDir()
	tgzPath := filepath.Join(dir, "demo-1.0.0.tgz")
	if err := os.WriteFile(tgzPath, tgz, 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Package already exists.","error":"Package already exists."}`))
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.Registry = srv.URL + "/api/v4/projects/1/packages/npm"
	cfg.values[nerfDart(cfg.Registry+"/")+":_authToken"] = "secret"
	state := NewState()
	state.Local["demo@1.0.0"] = StateRecord{Name: "demo", Version: "1.0.0", Path: tgzPath}

	report, err := PublishAll(context.Background(), NewClientWithConfig(cfg), state, PublishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Present != 1 || report.Failed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if state.Target["demo@1.0.0"].Name != "demo" {
		t.Fatalf("target not marked on gitlab already-exists: %#v", state.Target)
	}
}

func TestPublishAllPublishesScopedPackageWithAuth(t *testing.T) {
	tgz := testPackageTarball(t, `{"name":"@scope/demo","version":"1.0.0"}`)
	dir := t.TempDir()
	tgzPath := filepath.Join(dir, "scope-demo-1.0.0.tgz")
	if err := os.WriteFile(tgzPath, tgz, 0o644); err != nil {
		t.Fatal(err)
	}

	var authHeader string
	var publishPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		publishPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.ScopeRegistries["@scope"] = srv.URL
	cfg.values[nerfDart(srv.URL+"/")+":_authToken"] = "scoped-secret"
	state := NewState()
	state.Local["@scope/demo@1.0.0"] = StateRecord{Name: "@scope/demo", Version: "1.0.0", Path: tgzPath}

	report, err := PublishAll(context.Background(), NewClientWithConfig(cfg), state, PublishOptions{Source: "scoped-publish"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Pushed != 1 || report.Failed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if publishPath != "/@scope%2Fdemo" {
		t.Fatalf("publish path = %s", publishPath)
	}
	if authHeader != "Bearer scoped-secret" {
		t.Fatalf("auth header = %s", authHeader)
	}
	if state.Target["@scope/demo@1.0.0"].Source != "scoped-publish" {
		t.Fatalf("target not marked: %#v", state.Target)
	}
}

func TestPublishAllRetriesTransientFailure(t *testing.T) {
	tgz := testPackageTarball(t, `{"name":"demo","version":"1.0.0"}`)
	dir := t.TempDir()
	tgzPath := filepath.Join(dir, "demo-1.0.0.tgz")
	if err := os.WriteFile(tgzPath, tgz, 0o644); err != nil {
		t.Fatal(err)
	}
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&hits, 1) == 1 {
			http.Error(w, "temporary", http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	state := NewState()
	state.Local["demo@1.0.0"] = StateRecord{Name: "demo", Version: "1.0.0", Path: tgzPath}
	report, err := PublishAll(context.Background(), NewClient(srv.URL), state, PublishOptions{MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Pushed != 1 || atomic.LoadInt64(&hits) != 2 {
		t.Fatalf("report=%#v hits=%d", report, hits)
	}
}

func TestPublishAllSkipsTargetPresent(t *testing.T) {
	state := NewState()
	state.Local["demo@1.0.0"] = StateRecord{Name: "demo", Version: "1.0.0", Path: "/nope"}
	MarkTargetPresent(state, Package{Name: "demo", Version: "1.0.0"}, "test")
	report, err := PublishAll(context.Background(), NewClient("https://example.test"), state, PublishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Skipped != 0 || report.Pushed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if _, ok := state.Local["demo@1.0.0"]; ok {
		t.Fatalf("invalid local record should be removed before publish")
	}
}

func TestPublishAllGitLabCIJobTokenFallbackAuth(t *testing.T) {
	t.Setenv("CI_JOB_TOKEN", "job-token-123")
	tgz := testPackageTarball(t, `{"name":"demo","version":"1.0.0"}`)
	dir := t.TempDir()
	tgzPath := filepath.Join(dir, "demo-1.0.0.tgz")
	if err := os.WriteFile(tgzPath, tgz, 0o644); err != nil {
		t.Fatal(err)
	}
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		got := r.Header.Get("Authorization")
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("gitlab-ci-token:job-token-123"))
		if got != want {
			t.Fatalf("authorization header = %q want %q", got, want)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.Registry = srv.URL + "/api/v4/projects/1/packages/npm"
	cfg.values[nerfDart(cfg.Registry+"/")+":_authToken"] = "stale-or-wrong"
	state := NewState()
	state.Local["demo@1.0.0"] = StateRecord{Name: "demo", Version: "1.0.0", Path: tgzPath}

	report, err := PublishAll(context.Background(), NewClientWithConfig(cfg), state, PublishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Pushed != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if calls < 2 {
		t.Fatalf("expected retry fallback call, got %d", calls)
	}
}

func testPackageTarball(t testing.TB, packageJSON string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	data := []byte(packageJSON)
	if err := tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
