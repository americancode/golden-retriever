package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestPublishAllAggregatesScanGateBlocks(t *testing.T) {
	tgzA := testPackageTarball(t, `{"name":"demo-a","version":"1.0.0"}`)
	tgzB := testPackageTarball(t, `{"name":"demo-b","version":"1.0.0"}`)
	dir := t.TempDir()
	pathA := filepath.Join(dir, "demo-a-1.0.0.tgz")
	pathB := filepath.Join(dir, "demo-b-1.0.0.tgz")
	if err := os.WriteFile(pathA, tgzA, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathB, tgzB, 0o644); err != nil {
		t.Fatal(err)
	}
	state := NewState()
	state.Local["demo-a@1.0.0"] = StateRecord{
		Name:       "demo-a",
		Version:    "1.0.0",
		Path:       pathA,
		ScanStatus: "fail",
		ScanReason: "trivy vulnerabilities (high+): GHSA-a",
	}
	state.Local["demo-b@1.0.0"] = StateRecord{
		Name:       "demo-b",
		Version:    "1.0.0",
		Path:       pathB,
		ScanStatus: "fail",
		ScanReason: "trivy vulnerabilities (high+): GHSA-b",
	}

	report, err := PublishAll(context.Background(), NewClient("https://example.test"), state, PublishOptions{
		RequireScanPass: true,
		Concurrency:     2,
	})
	if err == nil {
		t.Fatal("PublishAll error = nil, want aggregated scan gate error")
	}
	if got, want := err.Error(), "2 packages blocked by scan gate"; got != want {
		t.Fatalf("PublishAll error = %q, want %q", got, want)
	}
	if strings.Contains(err.Error(), "demo-a@1.0.0") || strings.Contains(err.Error(), "demo-b@1.0.0") {
		t.Fatalf("PublishAll error = %q, want abstract count only", err.Error())
	}
	if report.Failed != 2 {
		t.Fatalf("report.Failed = %d, want 2", report.Failed)
	}
}

func TestPublishAllPreservesSourceMetadataForTargetSkipOnRerun(t *testing.T) {
	tgz := testPackageTarball(t, `{"name":"demo","version":"1.0.0"}`)
	dir := t.TempDir()
	tgzPath := filepath.Join(dir, "demo-1.0.0.tgz")
	if err := os.WriteFile(tgzPath, tgz, 0o644); err != nil {
		t.Fatal(err)
	}
	sha1Sum := sha1.Sum(tgz)
	sha1Integrity := "sha1-" + base64.StdEncoding.EncodeToString(sha1Sum[:])
	shasum := hex.EncodeToString(sha1Sum[:])

	var publishHits int64
	srv := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPut:
			atomic.AddInt64(&publishHits, 1)
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			t.Fatalf("unexpected fetch GET on rerun: %s", r.URL.Path)
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer srv.Close()

	state := NewState()
	state.Local["demo@1.0.0"] = StateRecord{
		Name:      "demo",
		Version:   "1.0.0",
		Tarball:   srv.URL + "/demo/-/demo-1.0.0.tgz",
		Integrity: sha1Integrity,
		Shasum:    shasum,
		Path:      tgzPath,
		ScanStatus: "pass",
	}

	report, err := PublishAll(context.Background(), NewClient(srv.URL), state, PublishOptions{Source: "test-registry"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Pushed != 1 {
		t.Fatalf("unexpected publish report: %#v", report)
	}
	if got := state.Target["demo@1.0.0"].Integrity; got != sha1Integrity {
		t.Fatalf("target integrity = %q, want source integrity %q", got, sha1Integrity)
	}

	statePath := filepath.Join(dir, ".gr", "state.json")
	if err := SaveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	rerunReport, err := FetchAll(context.Background(), NewClient(srv.URL), []Package{{
		Name:      "demo",
		Version:   "1.0.0",
		Tarball:   srv.URL + "/demo/-/demo-1.0.0.tgz",
		Integrity: sha1Integrity,
		Shasum:    shasum,
	}}, FetchOptions{
		OutDir:      filepath.Join(dir, "rerun-tgzs"),
		StatePath:   statePath,
		Concurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rerunReport.TargetSkipped != 1 || rerunReport.Downloaded != 0 {
		t.Fatalf("unexpected rerun fetch report: %#v", rerunReport)
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
