package npm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
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
	if len(rec.ScanVulnURLs) != 1 || rec.ScanVulnURLs[0] != "https://osv.dev/vulnerability/GHSA-test-123" {
		t.Fatalf("ScanVulnURLs = %v, want OSV URL", rec.ScanVulnURLs)
	}
}

func TestScanStateOSVOfflineProviderDoesNotCallAPI(t *testing.T) {
	statePath := writeScanTestState(t)
	installFakeOSVScanner(t, `{"results":[{"packages":[{"package":{"name":"left-pad","version":"1.3.0","ecosystem":"npm"},"vulnerabilities":[{"id":"GHSA-test-offline","database_specific":{"severity":"high"}}]}]}]}`)
	requests := 0
	var progress []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer server.Close()

	_, err := ScanState(context.Background(), ScanOptions{
		StatePath:       statePath,
		Source:          "target",
		UseOSV:          true,
		OSVProvider:     "osv-offline",
		OSVEndpoint:     server.URL + "/querybatch",
		MinSeverity:     "high",
		UnknownSeverity: "high",
		Progress: func(format string, args ...any) {
			progress = append(progress, fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		t.Fatalf("ScanState error = %v", err)
	}
	if requests != 0 {
		t.Fatalf("OSV API requests = %d, want 0", requests)
	}
	if report.Failed != 1 {
		t.Fatalf("report.Failed = %d, want 1", report.Failed)
	}
	if !slices.Contains(progress, "scan:provider provider=osv-offline source=target osv=true") {
		t.Fatalf("progress logs = %v, want explicit offline provider line", progress)
	}
	foundVuln := false
	for _, line := range progress {
		if strings.Contains(line, "scan:vuln package=left-pad@1.3.0 severity=high ids=GHSA-test-offline") {
			foundVuln = true
			break
		}
	}
	if !foundVuln {
		t.Fatalf("progress logs = %v, want severity in vuln log", progress)
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
	if len(rec.ScanVulnURLs) != 1 || rec.ScanVulnURLs[0] != "https://osv.dev/vulnerability/GHSA-test-456" {
		t.Fatalf("ScanVulnURLs = %v, want OSV URL", rec.ScanVulnURLs)
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
		OSVAPIBatchSize: 1,
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

func TestScanStateOSVOfflineProviderHeartbeat(t *testing.T) {
	statePath := writeScanTestState(t)
	installFakeSlowOSVScanner(t, 11*time.Second, `{"results":[{"packages":[{"package":{"name":"left-pad","version":"1.3.0","ecosystem":"npm"},"vulnerabilities":[{"id":"GHSA-test-heartbeat","database_specific":{"severity":"high"}}]}]}]}`)
	var progress []string

	report, err := ScanState(context.Background(), ScanOptions{
		StatePath:       statePath,
		Source:          "target",
		UseOSV:          true,
		OSVProvider:     "osv-offline",
		MinSeverity:     "high",
		UnknownSeverity: "high",
		Progress: func(format string, args ...any) {
			progress = append(progress, fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		t.Fatalf("ScanState error = %v", err)
	}
	if report.Failed != 1 {
		t.Fatalf("report.Failed = %d, want 1", report.Failed)
	}
	found := false
	for _, line := range progress {
		if strings.HasPrefix(line, "osv:scanner:progress mode=offline elapsed=") && strings.Contains(line, "packages=1") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("progress logs = %v, want heartbeat line", progress)
	}
}

func TestScanStateSkipsOSVWhenNoPackagesSelected(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	state := &State{
		SchemaVersion: 1,
		Target:        map[string]StateRecord{},
		Local:         map[string]StateRecord{},
	}
	if err := saveState(statePath, state); err != nil {
		t.Fatal(err)
	}

	var progress []string
	report, err := ScanState(context.Background(), ScanOptions{
		StatePath:       statePath,
		Source:          "target",
		UseOSV:          true,
		OSVProvider:     "osv-offline",
		MinSeverity:     "high",
		UnknownSeverity: "high",
		Progress: func(format string, args ...any) {
			progress = append(progress, fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		t.Fatalf("ScanState error = %v", err)
	}
	if report.Total != 0 || report.Passed != 0 || report.Failed != 0 || report.Errors != 0 {
		t.Fatalf("report = %+v, want zero counts", report)
	}
	if !slices.Contains(progress, "scan:skip reason=no-packages source=target") {
		t.Fatalf("progress logs = %v, want skip line", progress)
	}
	for _, line := range progress {
		if strings.Contains(line, "osv:scanner:") || strings.Contains(line, "osv:batch:") {
			t.Fatalf("progress logs = %v, did not expect OSV activity", progress)
		}
	}
}

func TestScanStateOSVOfflineProviderParallelChunks(t *testing.T) {
	statePath := writeScanTestStateWithPackages(t, []StateRecord{
		{Name: "pkg-a", Version: "1.0.0"},
		{Name: "pkg-b", Version: "1.0.0"},
		{Name: "pkg-c", Version: "1.0.0"},
		{Name: "pkg-d", Version: "1.0.0"},
	})
	installFakeSlowOSVScanner(t, 2*time.Second, `{"results":[]}`)
	var progress []string
	start := time.Now()

	report, err := ScanState(context.Background(), ScanOptions{
		StatePath:             statePath,
		Source:                "target",
		UseOSV:                true,
		OSVProvider:           "osv-offline",
		OSVOfflineChunkSize:   1,
		OSVOfflineConcurrency: 2,
		MinSeverity:           "high",
		UnknownSeverity:       "high",
		Progress: func(format string, args ...any) {
			progress = append(progress, fmt.Sprintf(format, args...))
		},
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("ScanState error = %v", err)
	}
	if report.Total != 4 || report.Passed != 4 || report.Failed != 0 || report.Errors != 0 {
		t.Fatalf("report = %+v, want total=4 passed=4 failed=0 errors=0", report)
	}
	if elapsed >= 7*time.Second {
		t.Fatalf("elapsed = %s, want parallel chunks substantially under serial 8s", elapsed)
	}
	if !containsPrefix(progress, "osv:scanner:parallel-start ") {
		t.Fatalf("progress logs = %v, want parallel start", progress)
	}
	if !containsPrefix(progress, "osv:scanner:chunk:start chunk=1/4 ") {
		t.Fatalf("progress logs = %v, want chunk start", progress)
	}
	if !containsPrefix(progress, "osv:scanner:chunk:done chunk=") {
		t.Fatalf("progress logs = %v, want chunk done", progress)
	}
}

func TestScanStateOSVOfflineProviderParallelChunksFailureDiagnostics(t *testing.T) {
	statePath := writeScanTestStateWithPackages(t, []StateRecord{
		{Name: "pkg-a", Version: "1.0.0"},
		{Name: "pkg-b", Version: "1.0.0"},
		{Name: "pkg-c", Version: "1.0.0"},
		{Name: "pkg-d", Version: "1.0.0"},
	})
	installFakeOSVScannerFailAbovePackages(t, 1, `{"results":[]}`)
	var progress []string

	report, err := ScanState(context.Background(), ScanOptions{
		StatePath:             statePath,
		Source:                "target",
		UseOSV:                true,
		OSVProvider:           "osv-offline",
		OSVOfflineChunkSize:   2,
		OSVOfflineConcurrency: 2,
		MinSeverity:           "high",
		UnknownSeverity:       "high",
		Progress: func(format string, args ...any) {
			progress = append(progress, fmt.Sprintf(format, args...))
		},
	})
	if err == nil {
		t.Fatalf("ScanState error = nil, want chunk failure")
	}
	if !strings.Contains(err.Error(), "chunk=") || !strings.Contains(err.Error(), "first=") || !strings.Contains(err.Error(), "last=") {
		t.Fatalf("ScanState error = %v, want chunk diagnostics", err)
	}
	if !containsPrefix(progress, "osv:scanner:chunk:fail chunk=") {
		t.Fatalf("progress logs = %v, want chunk fail log", progress)
	}
	if !containsPrefix(progress, "osv:scanner:parallel-fail ") {
		t.Fatalf("progress logs = %v, want parallel fail log", progress)
	}
	if containsPrefix(progress, "osv:scanner:done mode=offline provider=osv-scanner") {
		t.Fatalf("progress logs = %v, did not want done log after chunk failure", progress)
	}
}

func writeScanTestState(t testing.TB) string {
	t.Helper()
	return writeScanTestStateWithPackages(t, []StateRecord{{Name: "left-pad", Version: "1.3.0"}})
}

func writeScanTestStateWithPackages(t testing.TB, packages []StateRecord) string {
	t.Helper()
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	target := make(map[string]StateRecord, len(packages))
	for _, rec := range packages {
		target[rec.Name+"@"+rec.Version] = rec
	}
	state := &State{
		SchemaVersion: 1,
		Target:        target,
		Local:         map[string]StateRecord{},
	}
	if err := saveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	return statePath
}

func installFakeOSVScanner(t testing.TB, json string) {
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

func installFakeSlowOSVScanner(t testing.TB, delay time.Duration, json string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script helper is unix-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "osv-scanner")
	script := fmt.Sprintf("#!/bin/sh\nsleep %d\ncat <<'EOF'\n%s\nEOF\nexit 1\n", int(delay/time.Second), json)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
}

func installFakeOSVScannerFailAbovePackages(t testing.TB, maxPackages int, json string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script helper is unix-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "osv-scanner")
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" != "scan" ]; then echo "expected scan command" >&2; exit 9; fi
lockfile=""
prev=""
for arg in "$@"; do
  if [ "$prev" = "--lockfile" ]; then
    lockfile="$arg"
    break
  fi
  prev="$arg"
done
lockfile="${lockfile#osv-scanner:}"
count=$(grep -o '"version"' "$lockfile" | wc -l | tr -d ' ')
if [ "$count" -gt "%d" ]; then
  echo "scanned $lockfile file as osv-scanner and found $count packages" >&2
  kill -9 $$
fi
cat <<'EOF'
%s
EOF
exit 1
`, maxPackages, json)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
}

func containsPrefix(lines []string, prefix string) bool {
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
