package main

import (
	"flag"
	"os"
	"runtime"
)

type scanOptions struct {
	StatePath             string
	Source                string
	Concurrency           int
	BlocklistPath         string
	DenyPackagePrefix     []string
	UseOSV                bool
	OSVProvider           string
	OSVEndpoint           string
	OSVOfflineDBDir       string
	OSVAPIBatchSize       int
	OSVAPIConcurrency     int
	OSVOfflineChunkSize   int
	OSVOfflineRetryFailed bool
	TrivyOfflineScan      bool
	TrivySkipDBUpdate     bool
	TrivyChunkSize        int
	MinSeverity           string
	UnknownSeverity       string
	ExceptionsPath        string
	OSVOfflineConcurrency int
	TrivyConcurrency      int
	ReportPath            string
	JSONOut               bool
	Trace                 bool
}

func parseScanArgs(args []string) (*scanOptions, error) {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	statePath := fs.String("state", ".gr/state.json", "state inventory file")
	source := fs.String("source", "local", "scan source: local, target, or both")
	concurrency := fs.Int("concurrency", max(4, runtime.NumCPU()*2), "parallel scan worker count")
	blocklist := fs.String("blocklist", ".gr/scan-blocklist.json", "path to scan blocklist JSON file")
	denyPrefixes := fs.String("deny-package-prefixes", "", "comma-separated package name prefixes to block")
	useVuln := fs.Bool("vuln", true, "query the selected vulnerability provider for known vulnerable package versions")
	useOSV := fs.Bool("osv", true, "legacy alias for --vuln")
	provider := fs.String("provider", "osv-api", "scan provider: osv-api, osv-offline, trivy, or trivy-offline")
	osvEndpoint := fs.String("osv-endpoint", "https://api.osv.dev/v1/querybatch", "OSV querybatch API endpoint")
	osvOfflineDBDir := fs.String("osv-offline-db", os.Getenv("OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY"), "local OSV scanner database cache directory for osv-offline provider")
	osvAPIBatchSize := fs.Int("osv-api-batch-size", 200, "OSV API query batch size")
	osvOfflineChunkSize := fs.Int("osv-offline-chunk-size", 100, "offline osv-scanner package chunk size")
	osvOfflineRetryFailed := fs.Bool("osv-offline-retry-failed-chunks", true, "split and retry failed offline osv-scanner chunks with smaller package batches")
	trivyOfflineScan := fs.Bool("trivy-offline-scan", false, "pass --offline-scan to Trivy to avoid dependency-identification API calls")
	trivySkipDBUpdate := fs.Bool("trivy-skip-db-update", false, "pass --skip-db-update to Trivy and require an existing local Trivy vulnerability DB")
	trivyChunkSize := fs.Int("trivy-chunk-size", 100, "Trivy package chunk size for parallel scans")
	minSeverity := fs.String("min-severity", "high", "minimum OSV severity to fail: low, medium, high, critical")
	unknownSeverity := fs.String("unknown-severity", "high", "severity to assume when OSV severity is unavailable")
	exceptions := fs.String("exceptions", "", "path to scan exceptions JSON file")
	osvAPIConcurrency := fs.Int("osv-api-concurrency", max(4, runtime.NumCPU()/2), "parallel OSV API vulnerability detail lookup count")
	osvOfflineConcurrency := fs.Int("osv-offline-concurrency", max(4, runtime.NumCPU()/2), "parallel offline osv-scanner worker count")
	trivyConcurrency := fs.Int("trivy-concurrency", max(4, runtime.NumCPU()/2), "parallel Trivy worker count")
	reportPath := fs.String("report", ".gr/scan-report.json", "scan report JSON output path")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON summary")
	trace := fs.Bool("trace", envBool("GR_TRACE"), "print detailed stage/progress logs")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	vulnEnabled := selectedBoolAlias(fs, "vuln", *useVuln, "osv", *useOSV)
	return &scanOptions{
		StatePath:             *statePath,
		Source:                *source,
		Concurrency:           *concurrency,
		BlocklistPath:         *blocklist,
		DenyPackagePrefix:     csvList(*denyPrefixes),
		UseOSV:                vulnEnabled,
		OSVProvider:           *provider,
		OSVEndpoint:           *osvEndpoint,
		OSVOfflineDBDir:       *osvOfflineDBDir,
		OSVAPIBatchSize:       *osvAPIBatchSize,
		OSVAPIConcurrency:     *osvAPIConcurrency,
		OSVOfflineChunkSize:   *osvOfflineChunkSize,
		OSVOfflineRetryFailed: *osvOfflineRetryFailed,
		TrivyOfflineScan:      *trivyOfflineScan,
		TrivySkipDBUpdate:     *trivySkipDBUpdate,
		TrivyChunkSize:        *trivyChunkSize,
		MinSeverity:           *minSeverity,
		UnknownSeverity:       *unknownSeverity,
		ExceptionsPath:        *exceptions,
		OSVOfflineConcurrency: *osvOfflineConcurrency,
		TrivyConcurrency:      *trivyConcurrency,
		ReportPath:            *reportPath,
		JSONOut:               *jsonOut,
		Trace:                 *trace,
	}, nil
}
