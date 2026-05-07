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
	var publishPath string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
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
	if publishPath != "/demo" {
		t.Fatalf("publish path = %s", publishPath)
	}
	if body["_id"] != "demo" {
		t.Fatalf("body _id = %#v", body["_id"])
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

func TestPublishAllSkipsTargetPresent(t *testing.T) {
	state := NewState()
	state.Local["demo@1.0.0"] = StateRecord{Name: "demo", Version: "1.0.0", Path: "/nope"}
	MarkTargetPresent(state, Package{Name: "demo", Version: "1.0.0"}, "test")
	report, err := PublishAll(context.Background(), NewClient("https://example.test"), state, PublishOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if report.Skipped != 1 || report.Pushed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
}

func testPackageTarball(t *testing.T, packageJSON string) []byte {
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
