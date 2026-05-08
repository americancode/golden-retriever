package npm

import (
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
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

func TestFetchSkipsTargetPresentPackage(t *testing.T) {
	tgz := []byte("already pushed")
	integrity := sri(tgz)
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Write(tgz)
	}))
	defer srv.Close()

	dir := t.TempDir()
	statePath := filepath.Join(dir, ".gr", "state.json")
	state := NewState()
	MarkTargetPresent(state, Package{
		Name: "present", Version: "1.0.0", Tarball: srv.URL + "/present-1.0.0.tgz", Integrity: integrity,
	}, "test")
	if err := SaveState(statePath, state); err != nil {
		t.Fatal(err)
	}

	report, err := FetchAll(context.Background(), NewClient(srv.URL), []Package{{
		Name: "present", Version: "1.0.0", Tarball: srv.URL + "/present-1.0.0.tgz", Integrity: integrity,
	}}, FetchOptions{
		OutDir:      filepath.Join(dir, "tgzs"),
		StatePath:   statePath,
		Concurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.TargetSkipped != 1 || report.Downloaded != 0 || atomic.LoadInt64(&hits) != 0 {
		t.Fatalf("report=%#v hits=%d", report, hits)
	}
}

func TestFetchSkipsExistingValidTarballWithoutState(t *testing.T) {
	tgz := []byte("already local")
	integrity := sri(tgz)
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.Write(tgz)
	}))
	defer srv.Close()

	dir := t.TempDir()
	outDir := filepath.Join(dir, "tgzs")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	pkg := Package{Name: "local", Version: "1.0.0", Tarball: srv.URL + "/local-1.0.0.tgz", Integrity: integrity}
	if err := os.WriteFile(filepath.Join(outDir, tarballFileName(pkg)), tgz, 0o644); err != nil {
		t.Fatal(err)
	}

	statePath := filepath.Join(dir, ".gr", "state.json")
	report, err := FetchAll(context.Background(), NewClient(srv.URL), []Package{pkg}, FetchOptions{
		OutDir:      outDir,
		StatePath:   statePath,
		Concurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Skipped != 1 || report.Downloaded != 0 || atomic.LoadInt64(&hits) != 0 {
		t.Fatalf("report=%#v hits=%d", report, hits)
	}
	state, err := LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Local[pkg.Key()].Path == "" {
		t.Fatalf("existing tarball should be recorded in state: %#v", state.Local)
	}
}

func TestLoadStateMigratesDownloadedToLocal(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, ".gr", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte(`{
  "downloaded": {
    "left-pad@1.3.0": {"name":"left-pad","version":"1.3.0","tarball":"https://example/left-pad.tgz"}
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Local["left-pad@1.3.0"].Name != "left-pad" {
		t.Fatalf("local migration failed: %#v", state)
	}
	if state.Downloaded != nil {
		t.Fatalf("legacy downloaded should be cleared after migration")
	}
}

func TestFetchRecordsFailureAndClearsOnSuccess(t *testing.T) {
	var fail bool = true
	tgz := []byte("retry later")
	integrity := sri(tgz)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "temporary", http.StatusInternalServerError)
			return
		}
		w.Write(tgz)
	}))
	defer srv.Close()

	dir := t.TempDir()
	statePath := filepath.Join(dir, ".gr", "state.json")
	pkg := Package{Name: "flaky", Version: "1.0.0", Tarball: srv.URL + "/flaky.tgz", Integrity: integrity}
	report, err := FetchAll(context.Background(), NewClient(srv.URL), []Package{pkg}, FetchOptions{
		OutDir:      filepath.Join(dir, "tgzs"),
		StatePath:   statePath,
		Concurrency: 1,
		MaxRetries:  0,
	})
	if err == nil || report.Failed != 1 {
		t.Fatalf("got report=%#v err=%v, want failure", report, err)
	}
	state, err := LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.Failures[pkg.Key()].Attempts != 1 || state.Failures[pkg.Key()].LastError == "" {
		t.Fatalf("failure not recorded: %#v", state.Failures)
	}

	fail = false
	report, err = FetchAll(context.Background(), NewClient(srv.URL), []Package{pkg}, FetchOptions{
		OutDir:      filepath.Join(dir, "tgzs"),
		StatePath:   statePath,
		Concurrency: 1,
		MaxRetries:  0,
	})
	if err != nil || report.Downloaded != 1 {
		t.Fatalf("got report=%#v err=%v, want success", report, err)
	}
	state, err = LoadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := state.Failures[pkg.Key()]; ok {
		t.Fatalf("failure should be cleared after success: %#v", state.Failures)
	}
}

func TestValidateStateFilesRemovesInvalidLocalRecords(t *testing.T) {
	dir := t.TempDir()
	validPath := filepath.Join(dir, "valid.tgz")
	validData := []byte("valid")
	if err := os.WriteFile(validPath, validData, 0o644); err != nil {
		t.Fatal(err)
	}
	state := NewState()
	state.Local["valid@1.0.0"] = StateRecord{
		Name: "valid", Version: "1.0.0", Path: validPath, Integrity: sri(validData),
	}
	state.Local["missing@1.0.0"] = StateRecord{
		Name: "missing", Version: "1.0.0", Path: filepath.Join(dir, "missing.tgz"),
	}

	report := ValidateStateFiles(state)
	if report.CheckedLocal != 2 || report.ValidLocal != 1 || report.RemovedLocal != 1 {
		t.Fatalf("unexpected validation report: %#v", report)
	}
	if _, ok := state.Local["valid@1.0.0"]; !ok {
		t.Fatalf("valid local record removed")
	}
	if _, ok := state.Local["missing@1.0.0"]; ok {
		t.Fatalf("invalid local record retained")
	}
}

func TestTarballOutputPathStrategies(t *testing.T) {
	pkg := Package{Name: "@scope/pkg", Version: "1.2.3"}
	flat, err := tarballOutputPath("out", pkg, "flat")
	if err != nil {
		t.Fatal(err)
	}
	if flat != filepath.Join("out", "@scope+pkg-1.2.3.tgz") {
		t.Fatalf("flat path = %s", flat)
	}
	registry, err := tarballOutputPath("out", pkg, "registry")
	if err != nil {
		t.Fatal(err)
	}
	if registry != filepath.Join("out", "@scope/pkg", "-", "pkg-1.2.3.tgz") {
		t.Fatalf("registry path = %s", registry)
	}
	if _, err := tarballOutputPath("out", pkg, "bad"); err == nil {
		t.Fatalf("bad strategy should fail")
	}
}

func TestRetryAfterDelay(t *testing.T) {
	if got := retryAfterDelay("2"); got != 2*time.Second {
		t.Fatalf("seconds retry-after = %s", got)
	}
	when := time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat)
	if got := retryAfterDelay(when); got <= 0 {
		t.Fatalf("date retry-after = %s", got)
	}
	if got := retryAfterDelay("bad"); got != 0 {
		t.Fatalf("bad retry-after = %s", got)
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

func TestResolveAliasWithoutSpecUsesWildcardSelection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/real":
			fmt.Fprintf(w, `{
  "name": "real",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "1.5.0": {
      "name": "real",
      "version": "1.5.0",
      "dist": {"tarball": "%s/real/-/real-1.5.0.tgz"}
    },
    "2.0.0": {
      "name": "real",
      "version": "2.0.0",
      "deprecated": "use maintained release",
      "dist": {"tarball": "%s/real/-/real-2.0.0.tgz"}
    }
  }
}`, serverURL(r), serverURL(r))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	input := filepath.Join(dir, "package.json")
	if err := os.WriteFile(input, []byte(`{"dependencies":{"alias":"npm:real"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, err := ResolvePackageJSON(context.Background(), NewClient(srv.URL), input, ResolveOptions{IncludeOptional: true})
	if err != nil {
		t.Fatal(err)
	}
	pkgs := graph.Packages()
	if len(pkgs) != 1 || pkgs[0].Name != "real" || pkgs[0].Version != "1.5.0" {
		t.Fatalf("alias without spec should use wildcard manifest selection: %#v", pkgs)
	}
}

func TestFetchRetriesTransientFailure(t *testing.T) {
	tgz := []byte("retry tarball")
	integrity := sri(tgz)
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt64(&hits, 1) == 1 {
			http.Error(w, "temporary", http.StatusInternalServerError)
			return
		}
		w.Write(tgz)
	}))
	defer srv.Close()

	dir := t.TempDir()
	report, err := FetchAll(context.Background(), NewClient(srv.URL), []Package{{
		Name:      "retry",
		Version:   "1.0.0",
		Tarball:   srv.URL + "/retry-1.0.0.tgz",
		Integrity: integrity,
	}}, FetchOptions{
		OutDir:      filepath.Join(dir, "tgzs"),
		StatePath:   filepath.Join(dir, ".gr", "state.json"),
		Concurrency: 1,
		MaxRetries:  2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Downloaded != 1 || atomic.LoadInt64(&hits) != 2 {
		t.Fatalf("report=%#v hits=%d", report, hits)
	}
}

func TestFetchContinuesAfterTarballFailureAndDownloadsRemaining(t *testing.T) {
	okTGZ := []byte("ok tarball")
	okIntegrity := sri(okTGZ)
	var okHits int64
	var failHits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok-1.0.0.tgz":
			atomic.AddInt64(&okHits, 1)
			w.Write(okTGZ)
		case "/fail-1.0.0.tgz":
			atomic.AddInt64(&failHits, 1)
			http.Error(w, "missing", http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	statePath := filepath.Join(dir, ".gr", "state.json")
	report, err := FetchAll(context.Background(), NewClient(srv.URL), []Package{
		{Name: "ok", Version: "1.0.0", Tarball: srv.URL + "/ok-1.0.0.tgz", Integrity: okIntegrity},
		{Name: "fail", Version: "1.0.0", Tarball: srv.URL + "/fail-1.0.0.tgz"},
	}, FetchOptions{
		OutDir:      filepath.Join(dir, "tgzs"),
		StatePath:   statePath,
		Concurrency: 2,
		MaxRetries:  2,
	})
	if err == nil {
		t.Fatalf("expected mixed fetch run to return error for failed package")
	}
	if report.Downloaded != 1 || report.Failed != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if atomic.LoadInt64(&okHits) != 1 || atomic.LoadInt64(&failHits) != 1 {
		t.Fatalf("unexpected hit counts ok=%d fail=%d", okHits, failHits)
	}
	state, loadErr := LoadState(statePath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if state.Local["ok@1.0.0"].Path == "" {
		t.Fatalf("successful download should be recorded in state: %#v", state.Local)
	}
	if state.Failures["fail@1.0.0"].Attempts != 1 {
		t.Fatalf("failed tarball should be recorded once for non-retryable 404: %#v", state.Failures)
	}
}

func TestFetchDoesNotRetryNonRetryableTarballFailure(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer srv.Close()

	dir := t.TempDir()
	_, err := FetchAll(context.Background(), NewClient(srv.URL), []Package{{
		Name:    "missing",
		Version: "1.0.0",
		Tarball: srv.URL + "/missing-1.0.0.tgz",
	}}, FetchOptions{
		OutDir:      filepath.Join(dir, "tgzs"),
		StatePath:   filepath.Join(dir, ".gr", "state.json"),
		Concurrency: 1,
		MaxRetries:  5,
	})
	if err == nil {
		t.Fatalf("expected fetch failure for 404 tarball")
	}
	if atomic.LoadInt64(&hits) != 1 {
		t.Fatalf("non-retryable 404 should be attempted once, hits=%d", hits)
	}
}

func TestVerifySha1SRI(t *testing.T) {
	data := []byte("legacy")
	sum := sha1.Sum(data)
	pkg := Package{Name: "legacy", Version: "1.0.0", Integrity: "sha1-" + base64.StdEncoding.EncodeToString(sum[:])}
	if err := verifyHashes(nil, sum[:], pkg); err != nil {
		t.Fatal(err)
	}
}

func TestFetchAppliesTarballAuth(t *testing.T) {
	tgz := []byte("private tarball")
	integrity := sri(tgz)
	var authHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		w.Write(tgz)
	}))
	defer srv.Close()

	cfg := DefaultConfig()
	cfg.values[nerfDart(srv.URL)+":_authToken"] = "secret"
	client := NewClientWithConfig(cfg)

	dir := t.TempDir()
	_, err := FetchAll(context.Background(), client, []Package{{
		Name: "private", Version: "1.0.0", Tarball: srv.URL + "/private-1.0.0.tgz", Integrity: integrity,
	}}, FetchOptions{
		OutDir:      filepath.Join(dir, "tgzs"),
		StatePath:   filepath.Join(dir, ".gr", "state.json"),
		Concurrency: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if authHeader != "Bearer secret" {
		t.Fatalf("auth header = %s", authHeader)
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
