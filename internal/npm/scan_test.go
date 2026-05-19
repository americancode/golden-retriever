package npm

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestScanStateOSVOfflineProvider(t *testing.T) {
	statePath := writeScanTestState(t)
	installFakeOSVScanner(t, `{"results":[{"packages":[{"package":{"name":"left-pad","version":"1.3.0","ecosystem":"npm"},"vulnerabilities":[{"id":"GHSA-test-123","summary":"left-pad test vulnerability","database_specific":{"severity":"high"}}]}]}]}`)

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
	if len(rec.ScanVulnDescriptions) != 1 || !strings.Contains(rec.ScanVulnDescriptions[0], "left-pad test vulnerability") {
		t.Fatalf("ScanVulnDescriptions = %v, want OSV summary", rec.ScanVulnDescriptions)
	}
	if len(report.Findings) != 1 || len(report.Findings[0].VulnDescriptions) != 1 || !strings.Contains(report.Findings[0].VulnDescriptions[0], "left-pad test vulnerability") {
		t.Fatalf("report.Findings = %+v, want vulnerability description", report.Findings)
	}
}

func TestScanStateOSVAPIIncludesDescriptions(t *testing.T) {
	statePath := writeScanTestState(t)
	server := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/querybatch":
			fmt.Fprint(w, `{"results":[{"vulns":[{"id":"GHSA-api-123"}]}]}`)
		case "/vulns/GHSA-api-123":
			fmt.Fprint(w, `{"id":"GHSA-api-123","summary":"api summary","details":"api details","database_specific":{"severity":"high"}}`)
		default:
			http.NotFound(w, r)
		}
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
	if len(report.Findings) != 1 || len(report.Findings[0].VulnDescriptions) != 1 || !strings.Contains(report.Findings[0].VulnDescriptions[0], "api summary") {
		t.Fatalf("report.Findings = %+v, want OSV API summary", report.Findings)
	}
}

func TestScanStateOSVOfflineProviderDoesNotCallAPI(t *testing.T) {
	statePath := writeScanTestState(t)
	installFakeOSVScanner(t, `{"results":[{"packages":[{"package":{"name":"left-pad","version":"1.3.0","ecosystem":"npm"},"vulnerabilities":[{"id":"GHSA-test-offline","database_specific":{"severity":"high"}}]}]}]}`)
	requests := 0
	var progress []string
	server := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "should not be called", http.StatusInternalServerError)
	}))
	defer server.Close()

	report, err := ScanState(context.Background(), ScanOptions{
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
	if !slices.Contains(progress, "scan:provider provider=osv-offline inventory=target vuln=true") {
		t.Fatalf("progress logs = %v, want explicit offline provider line", progress)
	}
	foundVuln := false
	for _, line := range progress {
		if strings.Contains(line, "scan:vuln-json severity=high provider=osv-scanner finding={") &&
			strings.Contains(line, `"package":"left-pad@1.3.0"`) &&
			strings.Contains(line, `"status":"fail"`) {
			foundVuln = true
			break
		}
	}
	if !foundVuln {
		t.Fatalf("progress logs = %v, want severity in vuln log", progress)
	}
}

func TestScanStateOSVAPIReturnsErrorWithoutOfflineFallback(t *testing.T) {
	statePath := writeScanTestState(t)
	server := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "blocked", http.StatusBadGateway)
	}))
	defer server.Close()

	_, err := ScanState(context.Background(), ScanOptions{
		StatePath:       statePath,
		Source:          "target",
		UseOSV:          true,
		OSVProvider:     "osv-api",
		OSVEndpoint:     server.URL + "/querybatch",
		MinSeverity:     "high",
		UnknownSeverity: "high",
	})
	if err == nil {
		t.Fatalf("ScanState error = nil, want OSV API error")
	}
}

func TestScanStateOSVAPIStopsAfterSingleFailure(t *testing.T) {
	statePath := writeScanTestState(t)
	requests := 0
	server := newTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, fmt.Sprintf("blocked-%d", requests), http.StatusBadGateway)
	}))
	defer server.Close()

	_, err := ScanState(context.Background(), ScanOptions{
		StatePath:       statePath,
		Source:          "target",
		UseOSV:          true,
		OSVProvider:     "osv-api",
		OSVEndpoint:     server.URL + "/querybatch",
		OSVAPIBatchSize: 1,
		MinSeverity:     "high",
		UnknownSeverity: "high",
	})
	if err == nil {
		t.Fatalf("ScanState error = nil, want OSV API error")
	}
	if requests != 1 {
		t.Fatalf("OSV API requests = %d, want 1", requests)
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
	if !slices.Contains(progress, "scan:skip reason=no-packages inventory=target") {
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

	_, err := ScanState(context.Background(), ScanOptions{
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

func TestScanStateOSVOfflineProviderParallelChunksRetryFailed(t *testing.T) {
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
		OSVOfflineRetryFailed: true,
		MinSeverity:           "high",
		UnknownSeverity:       "high",
		Progress: func(format string, args ...any) {
			progress = append(progress, fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		t.Fatalf("ScanState error = %v", err)
	}
	if report.Total != 4 || report.Passed != 4 || report.Failed != 0 || report.Errors != 0 {
		t.Fatalf("report = %+v, want total=4 passed=4 failed=0 errors=0", report)
	}
	if !containsPrefix(progress, "osv:scanner:chunk:retry chunk=") {
		t.Fatalf("progress logs = %v, want retry log", progress)
	}
	if !containsPrefix(progress, "osv:scanner:parallel-done ") {
		t.Fatalf("progress logs = %v, want parallel done log", progress)
	}
}

func TestScanStateTrivyProvider(t *testing.T) {
	statePath := writeScanTestState(t)
	installFakeTrivy(t, `{"Results":[{"Target":"package-lock.json","Class":"lang-pkgs","Type":"npm","Vulnerabilities":[{"VulnerabilityID":"GHSA-trivy-123","PkgName":"left-pad","InstalledVersion":"1.3.0","Severity":"HIGH","Title":"left-pad trivy title","Description":"left-pad trivy description","PrimaryURL":"https://avd.aquasec.com/nvd/GHSA-trivy-123"}]}]}`)
	var progress []string

	report, err := ScanState(context.Background(), ScanOptions{
		StatePath:         statePath,
		Source:            "target",
		UseOSV:            true,
		OSVProvider:       "trivy",
		TrivyOfflineScan:  true,
		TrivySkipDBUpdate: true,
		MinSeverity:       "high",
		UnknownSeverity:   "high",
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
	state, err := loadState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	rec := state.Target["left-pad@1.3.0"]
	if rec.ScanStatus != "fail" {
		t.Fatalf("ScanStatus = %q, want fail", rec.ScanStatus)
	}
	if !strings.Contains(rec.ScanReason, "GHSA-trivy-123") {
		t.Fatalf("ScanReason = %q, want trivy vuln id", rec.ScanReason)
	}
	if len(rec.ScanVulnURLs) != 1 || rec.ScanVulnURLs[0] != "https://avd.aquasec.com/nvd/GHSA-trivy-123" {
		t.Fatalf("ScanVulnURLs = %v, want Trivy primary URL", rec.ScanVulnURLs)
	}
	if len(rec.ScanVulnDescriptions) != 1 || !strings.Contains(rec.ScanVulnDescriptions[0], "left-pad trivy title") {
		t.Fatalf("ScanVulnDescriptions = %v, want Trivy title", rec.ScanVulnDescriptions)
	}
	if len(report.Findings) != 1 || len(report.Findings[0].VulnDescriptions) != 1 || !strings.Contains(report.Findings[0].VulnDescriptions[0], "left-pad trivy title") {
		t.Fatalf("report.Findings = %+v, want vulnerability description", report.Findings)
	}
	if !containsPrefix(progress, "trivy:start packages=1 offline_scan=true skip_db_update=true") {
		t.Fatalf("progress logs = %v, want trivy start line", progress)
	}
	if containsPrefix(progress, "trivy:db:") {
		t.Fatalf("progress logs = %v, did not expect db update logs with skip-db-update", progress)
	}
}

func TestScanStateTrivyProviderReturnsErrorWithoutOfflineFallback(t *testing.T) {
	statePath := writeScanTestState(t)
	installFakeTrivyFail(t)
	var progress []string

	_, err := ScanState(context.Background(), ScanOptions{
		StatePath:       statePath,
		Source:          "target",
		UseOSV:          true,
		OSVProvider:     "trivy",
		TrivyChunkSize:  100,
		MinSeverity:     "high",
		UnknownSeverity: "high",
		Progress: func(format string, args ...any) {
			progress = append(progress, fmt.Sprintf(format, args...))
		},
	})
	if err == nil {
		t.Fatalf("ScanState error = nil, want Trivy error")
	}
	if !containsPrefix(progress, "trivy:db:start packages=1") {
		t.Fatalf("progress logs = %v, want db start log", progress)
	}
	if !containsPrefix(progress, "trivy:db:fail elapsed=") {
		t.Fatalf("progress logs = %v, want db fail log", progress)
	}
	if containsPrefix(progress, "trivy:provider:fallback from=trivy to=trivy-offline ") {
		t.Fatalf("progress logs = %v, did not expect trivy offline fallback", progress)
	}
}

func TestScanStateTrivyOfflineProviderUsesLocalDBOnly(t *testing.T) {
	statePath := writeScanTestState(t)
	installFakeTrivy(t, `{"Results":[]}`)

	_, err := ScanState(context.Background(), ScanOptions{
		StatePath:       statePath,
		Source:          "target",
		UseOSV:          true,
		OSVProvider:     "trivy-offline",
		MinSeverity:     "high",
		UnknownSeverity: "high",
	})
	if err != nil {
		t.Fatalf("ScanState error = %v", err)
	}
}

func TestScanStateTrivyProviderParallelChunks(t *testing.T) {
	statePath := writeScanTestStateWithPackages(t, []StateRecord{
		{Name: "pkg-a", Version: "1.0.0"},
		{Name: "pkg-b", Version: "1.0.0"},
		{Name: "pkg-c", Version: "1.0.0"},
		{Name: "pkg-d", Version: "1.0.0"},
	})
	installFakeTrivyParallel(t, 2*time.Second, `{"Results":[]}`)
	var progress []string
	start := time.Now()

	report, err := ScanState(context.Background(), ScanOptions{
		StatePath:         statePath,
		Source:            "target",
		UseOSV:            true,
		OSVProvider:       "trivy",
		TrivyOfflineScan:  true,
		TrivySkipDBUpdate: true,
		TrivyChunkSize:    1,
		TrivyConcurrency:  2,
		MinSeverity:       "high",
		UnknownSeverity:   "high",
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
	if !containsPrefix(progress, "trivy:parallel-start ") {
		t.Fatalf("progress logs = %v, want parallel start", progress)
	}
	if !containsPrefix(progress, "trivy:chunk:start chunk=") {
		t.Fatalf("progress logs = %v, want chunk start", progress)
	}
	if !containsPrefix(progress, "trivy:chunk:done chunk=") {
		t.Fatalf("progress logs = %v, want chunk done", progress)
	}
}

func TestScanStateTrivyProviderParallelWarmsDBOnce(t *testing.T) {
	statePath := writeScanTestStateWithPackages(t, []StateRecord{
		{Name: "pkg-a", Version: "1.0.0"},
		{Name: "pkg-b", Version: "1.0.0"},
		{Name: "pkg-c", Version: "1.0.0"},
	})
	installFakeTrivyParallel(t, 0, `{"Results":[]}`)
	var progress []string

	_, err := ScanState(context.Background(), ScanOptions{
		StatePath:        statePath,
		Source:           "target",
		UseOSV:           true,
		OSVProvider:      "trivy",
		TrivyOfflineScan: true,
		TrivyChunkSize:   1,
		TrivyConcurrency: 2,
		MinSeverity:      "high",
		UnknownSeverity:  "high",
		Progress: func(format string, args ...any) {
			progress = append(progress, fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		t.Fatalf("ScanState error = %v", err)
	}
	if !containsPrefix(progress, "trivy:db:download-start packages=3") {
		t.Fatalf("progress logs = %v, want db download start", progress)
	}
	if !containsPrefix(progress, "trivy:db:start packages=3") {
		t.Fatalf("progress logs = %v, want db start log", progress)
	}
	if !containsPrefix(progress, "trivy:db:done elapsed=") {
		t.Fatalf("progress logs = %v, want db done log", progress)
	}
	if containsPrefix(progress, "trivy:start chunk=warmup ") {
		t.Fatalf("progress logs = %v, did not expect warmup scan chunk", progress)
	}
	if !containsPrefix(progress, "trivy:start chunk=1/3 packages=1 offline_scan=true skip_db_update=true") {
		t.Fatalf("progress logs = %v, want worker chunk 1/3", progress)
	}
	if !containsPrefix(progress, "trivy:start chunk=2/3 packages=1 offline_scan=true skip_db_update=true") {
		t.Fatalf("progress logs = %v, want worker chunk 2/3", progress)
	}
}

func TestScanStateTrivyProviderRealIntegration(t *testing.T) {
	if os.Getenv("GOLDEN_RETRIEVER_TRIVY_INTEGRATION") != "1" {
		t.Skip("set GOLDEN_RETRIEVER_TRIVY_INTEGRATION=1 to run real Trivy integration test")
	}
	if _, err := exec.LookPath("trivy"); err != nil {
		t.Skipf("trivy not available: %v", err)
	}
	statePath := writeScanTestStateWithPackages(t, []StateRecord{
		{Name: "left-pad", Version: "1.3.0"},
		{Name: "minimist", Version: "0.0.8"},
		{Name: "lodash", Version: "4.17.20"},
		{Name: "debug", Version: "2.6.8"},
		{Name: "ansi-regex", Version: "3.0.0"},
	})
	report, err := ScanState(context.Background(), ScanOptions{
		StatePath:        statePath,
		Source:           "target",
		UseOSV:           true,
		OSVProvider:      "trivy",
		TrivyOfflineScan: true,
		TrivyChunkSize:   1,
		TrivyConcurrency: 4,
		MinSeverity:      "high",
		UnknownSeverity:  "high",
	})
	if err != nil {
		t.Fatalf("ScanState real Trivy error = %v", err)
	}
	if report.Total != 5 {
		t.Fatalf("report.Total = %d, want 5", report.Total)
	}
}

func TestVulnerabilityProviderFor(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "", want: "osv-api"},
		{raw: "osv-api", want: "osv-api"},
		{raw: "osv-offline", want: "osv-offline"},
		{raw: "trivy", want: "trivy"},
		{raw: "trivy-offline", want: "trivy-offline"},
	}
	for _, tt := range tests {
		provider, err := vulnerabilityProviderFor(tt.raw)
		if err != nil {
			t.Fatalf("vulnerabilityProviderFor(%q) error = %v", tt.raw, err)
		}
		if provider.Name() != tt.want {
			t.Fatalf("vulnerabilityProviderFor(%q).Name = %q, want %q", tt.raw, provider.Name(), tt.want)
		}
	}
	if _, err := vulnerabilityProviderFor("unknown"); err == nil {
		t.Fatalf("vulnerabilityProviderFor unknown error = nil, want error")
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

func installFakeTrivy(t testing.TB, json string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script helper is unix-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "trivy")
	script := "#!/bin/sh\nif [ \"$1\" != \"fs\" ]; then echo \"expected fs command\" >&2; exit 9; fi\nfound_offline=0\nfound_skip=0\nfor arg in \"$@\"; do\n  if [ \"$arg\" = \"--offline-scan\" ]; then found_offline=1; fi\n  if [ \"$arg\" = \"--skip-db-update\" ]; then found_skip=1; fi\ndone\nif [ \"$found_offline\" != \"1\" ]; then echo \"missing --offline-scan\" >&2; exit 10; fi\nif [ \"$found_skip\" != \"1\" ]; then echo \"missing --skip-db-update\" >&2; exit 11; fi\nif [ ! -f package-lock.json ]; then echo \"missing package-lock.json\" >&2; exit 12; fi\ncat <<'EOF'\n" + json + "\nEOF\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
}

func installFakeTrivyParallel(t testing.TB, delay time.Duration, json string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script helper is unix-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "trivy")
	sleepLine := ""
	if delay > 0 {
		sleepLine = fmt.Sprintf("sleep %d\n", int(delay/time.Second))
	}
	script := "#!/bin/sh\nif [ \"$1\" != \"fs\" ]; then echo \"expected fs command\" >&2; exit 9; fi\n" + sleepLine + "cat <<'EOF'\n" + json + "\nEOF\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+oldPath)
}

func installFakeTrivyFail(t testing.TB) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script helper is unix-only")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "trivy")
	script := "#!/bin/sh\nif [ \"$1\" != \"fs\" ]; then echo \"expected fs command\" >&2; exit 9; fi\necho \"simulated trivy failure\" >&2\nexit 2\n"
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
