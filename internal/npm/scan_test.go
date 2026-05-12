package npm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScanStateOSVOfflineProvider(t *testing.T) {
	statePath := writeScanTestState(t)
	installFakeOSVScanner(t, `{"results":[{"packages":[{"package":{"name":"left-pad","version":"1.3.0","ecosystem":"npm"},"vulnerabilities":[{"id":"GHSA-test-123","database_specific":{"severity":"high"}}]}]}]}`)

	report, err := ScanState(context.Background(), ScanOptions{
		StatePath:       statePath,
		Source:          "target",
		UseOSV:          true,
		OSVProvider:     "osv-offline",
		MinSeverity:     "high",
		UnknownSeverity: "high",
	})
	if err != nil {
		t.Fatalf("ScanState error = %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("report.Failed = %d, want 1", report.Failed)
	}
	state, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	rec := state.Target["left-pad@1.3.0"]
	if rec.ScanStatus != "fail" {
		t.Fatalf("ScanStatus = %q, want fail", rec.ScanStatus)
	}
	if !strings.Contains(rec.ScanReason, "GHSA-test-123") {
		t.Fatalf("ScanReason = %q, want vuln id", rec.ScanReason)
	}
}

func TestScanStateOSVAPIFallsBackOffline(t *testing.T) {
	statePath := writeScanTestState(t)
	installFakeOSVScanner(t, `{"results":[{"packages":[{"package":{"name":"left-pad","version":"1.3.0","ecosystem":"npm"},"vulnerabilities":[{"id":"GHSA-test-456","database_specific":{"severity":"critical"}}]}]}]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusBadGateway)
	}))
	defer server.Close()

	report, err := ScanState(context.Background(), ScanOptions{
		StatePath:       statePath,
		Source:          "target",
		UseOSV:          true,
		OSVProvider:     "osv-api",
		OSVEndpoint:     server.URL + "/querybatch",
		MinSeverity:     "high",
		UnknownSeverity: "high",
	})
	if err != nil {
		t.Fatalf("ScanState error = %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("report.Failed = %d, want 1", report.Failed)
	}
	state, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	rec := state.Target["left-pad@1.3.0"]
	if !strings.Contains(rec.ScanReason, "GHSA-test-456") {
		t.Fatalf("ScanReason = %q, want fallback vuln id", rec.ScanReason)
	}
}

func TestScanStateOSVAPIFallsBackAfterSingleFailure(t *testing.T) {
	statePath := writeScanTestState(t)
	installFakeOSVScanner(t, `{"results":[{"packages":[{"package":{"name":"left-pad","version":"1.3.0","ecosystem":"npm"},"vulnerabilities":[{"id":"GHSA-test-789","database_specific":{"severity":"high"}}]}]}]}`)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, fmt.Sprintf("blocked-%d", requests), http.StatusBadGateway)
	}))
	defer server.Close()

	report, err := ScanState(context.Background(), ScanOptions{
		StatePath:       statePath,
		Source:          "target",
		UseOSV:          true,
		OSVProvider:     "osv-api",
		OSVEndpoint:     server.URL + "/querybatch",
		OSVBatchSize:    1,
		MinSeverity:     "high",
		UnknownSeverity: "high",
	})
	if err != nil {
		t.Fatalf("ScanState error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("OSV API requests = %d, want 1", requests)
	}
	if report.Failed != 1 {
		t.Fatalf("report.Failed = %d, want 1", report.Failed)
	}
}

func writeScanTestState(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	state := &State{
		SchemaVersion: 1,
		Target: map[string]StateRecord{
			"left-pad@1.3.0": {
				Name:    "left-pad",
				Version: "1.3.0",
			},
		},
		Local: map[string]StateRecord{},
	}
	if err := saveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	return statePath
}

func installFakeOSVScanner(t *testing.T, json string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script helper is unix-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "osv-scanner")
	script := "#!/bin/sh\nif [ \"$1\" != \"scan\" ]; then echo \"expected scan command\" >&2; exit 9; fi\nif [ \"$2\" = \"source\" ]; then echo \"unexpected source positional\" >&2; exit 10; fi\nfor arg in \"$@\"; do\n  if [ \"$arg\" = \"--offline-vulnerabilities\" ]; then echo \"unexpected legacy offline flag\" >&2; exit 11; fi\ndone\ncat <<'EOF'\n" + json + "\nEOF\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
}
