package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"golden-retriever/internal/npm"
)

type mirrorOptions struct {
	Inputs                    []string
	ProjectConcurrency        int
	Out                       string
	StatePath                 string
	Registry                  string
	TargetRegistry            string
	NPMRC                     string
	TargetNPMRC               string
	TargetInsecureSkipVerify  bool
	MetadataCache             string
	MetadataCacheTTL          time.Duration
	MetadataRetries           int
	SyncTarget                bool
	ResolveOptions            npm.ResolveOptions
	OutputNaming              string
	FetchConcurrency          int
	TargetConcurrency         int
	PushConcurrency           int
	MaxRetries                int
	PublishRetries            int
	Tag                       string
	Access                    string
	ScanAuto                  bool
	ScanEnforce               bool
	ScanOSV                   bool
	ScanProvider              string
	ScanOSVEndpoint           string
	ScanOSVOfflineDBDir       string
	ScanOSVAPIBatchSize       int
	ScanOSVOfflineChunkSize   int
	ScanOSVOfflineRetryFailed bool
	ScanTrivyOfflineScan      bool
	ScanTrivySkipDBUpdate     bool
	ScanTrivyChunkSize        int
	ScanMinSeverity           string
	ScanUnknownSeverity       string
	ScanExceptions            string
	ScanOSVAPIConcurrency     int
	ScanOSVOfflineConcurrency int
	ScanTrivyConcurrency      int
	ScanDenyPrefixes          []string
	ScanBlocklist             string
	ScanReportPath            string
	JSONOut                   bool
	Trace                     bool
	Timeout                   time.Duration
}

func parseMirrorArgs(args []string) (*mirrorOptions, error) {
	fs := flag.NewFlagSet("mirror", flag.ExitOnError)
	input := fs.String("input", "package.json", "package.json, package-lock.json, or npm-shrinkwrap.json")
	inputs := fs.String("inputs", "", "comma-separated package.json/package-lock.json/npm-shrinkwrap.json paths")
	projectConcurrency := fs.Int("project-concurrency", max(1, runtime.NumCPU()/2), "parallel project workflow count when using --inputs")
	out := fs.String("out", ".gr/tgzs", "target directory for downloaded package tarballs")
	statePath := fs.String("state", ".gr/state.json", "state inventory file")
	registry := fs.String("registry", "", "source npm registry base URL override")
	targetRegistry := fs.String("target-registry", "", "target npm registry base URL")
	npmrc := fs.String("npmrc", "", "additional npmrc file to load")
	targetNPMRC := fs.String("target-npmrc", "", "additional npmrc file for target registry auth")
	targetInsecureSkipVerify := fs.Bool("target-insecure-skip-verify", false, "skip TLS certificate verification for target registry HTTPS connections")
	metadataCache := fs.String("metadata-cache", ".gr/metadata", "source packument metadata cache directory")
	metadataCacheTTL := fs.Duration("metadata-cache-ttl", 24*time.Hour, "source packument metadata cache freshness duration; 0 always revalidates")
	metadataRetries := fs.Int("metadata-retries", 3, "source packument metadata retry count for transient failures")
	includeDev := fs.Bool("include-dev", true, "include devDependencies from package.json roots")
	includeOptional := fs.Bool("include-optional", true, "include optionalDependencies")
	omit := fs.String("omit", "", "comma-separated dependency types to omit: dev, optional, peer")
	include := fs.String("include", "", "comma-separated dependency types to include after omit: dev, optional, peer")
	legacyPeerDeps := fs.Bool("legacy-peer-deps", false, "ignore peerDependencies")
	strictPeerDeps := fs.Bool("strict-peer-deps", false, "fail on peer dependency conflicts")
	preferDedupe := fs.Bool("prefer-dedupe", false, "prefer reusing existing satisfying package versions during resolution")
	installStrategy := fs.String("install-strategy", "nested", "dependency placement strategy: nested, hoisted, or shallow")
	engineStrict := fs.Bool("engine-strict", false, "fail on packages whose engines.node does not match --node-version")
	nodeVersion := fs.String("node-version", os.Getenv("NODE_VERSION"), "Node.js version used for engines.node checks")
	libc := fs.String("libc", os.Getenv("LIBC"), "libc value for package libc filters, such as glibc or musl")
	npmPlatformsRaw := fs.String("npm-platforms", os.Getenv("GOLDEN_RETRIEVER_NPM_PLATFORMS"), "comma-separated npm resolve platforms for package.json inputs, such as linux/x64/glibc,darwin/arm64,win32/x64")
	beforeRaw := fs.String("before", os.Getenv("NPM_BEFORE"), "only resolve package versions published at or before this RFC3339 timestamp")
	defaultTag := fs.String("default-tag", "latest", "default npm dist-tag used when a dependency has no explicit spec")
	includeStaged := fs.Bool("include-staged", false, "include npm stagedVersions metadata during manifest selection")
	avoid := fs.String("avoid", "", "semver range of versions to avoid during manifest selection")
	avoidStrict := fs.Bool("avoid-strict", false, "allow npm-pick-manifest style outside-range fallback when all matching versions are avoided")
	syncTarget := fs.Bool("sync-target", false, "query target registry first and rebuild target-present state for the resolved package set")
	resolveConcurrency := fs.Int("resolve-concurrency", max(8, runtime.NumCPU()*4), "parallel source registry metadata fetch count")
	fetchConcurrency := fs.Int("fetch-concurrency", max(8, runtime.NumCPU()*4), "parallel tarball download count")
	targetConcurrency := fs.Int("target-concurrency", max(8, runtime.NumCPU()*4), "parallel target registry query count")
	pushConcurrency := fs.Int("push-concurrency", max(4, runtime.NumCPU()*2), "parallel target registry publish count")
	outputNaming := fs.String("output-naming", "flat", "tarball output naming strategy: flat or registry")
	maxRetries := fs.Int("max-retries", 3, "tarball download retry count for transient failures")
	publishRetries := fs.Int("publish-retries", 3, "target registry publish retry count for transient failures")
	scanEnforce := fs.Bool("scan-enforce", false, "require local tarballs to pass scan gate before publishing")
	scanAuto := fs.Bool("scan-auto", true, "run scan stage after fetch and before publish")
	tag := fs.String("tag", "latest", "dist-tag to apply while publishing")
	access := fs.String("access", "public", "npm package access value")
	scanDenyPackagePrefixes := fs.String("scan-deny-package-prefixes", "", "comma-separated package name prefixes to block")
	scanVuln := fs.Bool("scan-vuln", true, "query the selected vulnerability provider for known vulnerable package versions")
	scanOSV := fs.Bool("scan-osv", true, "legacy alias for --scan-vuln")
	scanProvider := fs.String("scan-provider", "osv-api", "scan provider: osv-api, osv-offline, trivy, or trivy-offline")
	scanOSVEndpoint := fs.String("scan-osv-endpoint", "https://api.osv.dev/v1/querybatch", "OSV querybatch API endpoint")
	scanOSVOfflineDBDir := fs.String("scan-osv-offline-db", os.Getenv("OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY"), "local OSV scanner database cache directory for osv-offline provider")
	scanOSVAPIBatchSize := fs.Int("scan-osv-api-batch-size", 200, "OSV API query batch size")
	scanOSVOfflineChunkSize := fs.Int("scan-osv-offline-chunk-size", 100, "offline osv-scanner package chunk size")
	scanOSVOfflineRetryFailed := fs.Bool("scan-osv-offline-retry-failed-chunks", true, "split and retry failed offline osv-scanner chunks with smaller package batches")
	scanTrivyOfflineScan := fs.Bool("scan-trivy-offline-scan", false, "pass --offline-scan to Trivy to avoid dependency-identification API calls")
	scanTrivySkipDBUpdate := fs.Bool("scan-trivy-skip-db-update", false, "pass --skip-db-update to Trivy and require an existing local Trivy vulnerability DB")
	scanTrivyChunkSize := fs.Int("scan-trivy-chunk-size", 100, "Trivy package chunk size for parallel scans")
	scanMinSeverity := fs.String("scan-min-severity", "high", "minimum OSV severity to fail: low, medium, high, critical")
	scanUnknownSeverity := fs.String("scan-unknown-severity", "high", "severity to assume when OSV severity is unavailable")
	scanExceptions := fs.String("scan-exceptions", "", "path to scan exceptions JSON file")
	scanOSVAPIConcurrency := fs.Int("scan-osv-api-concurrency", max(4, runtime.NumCPU()/2), "parallel OSV API vulnerability detail lookup count")
	scanOSVOfflineConcurrency := fs.Int("scan-osv-offline-concurrency", max(4, runtime.NumCPU()/2), "parallel offline osv-scanner worker count")
	scanTrivyConcurrency := fs.Int("scan-trivy-concurrency", max(4, runtime.NumCPU()/2), "parallel Trivy worker count")
	scanBlocklist := fs.String("scan-blocklist", ".gr/scan-blocklist.json", "path to scan blocklist JSON file")
	scanReportPath := fs.String("scan-report", ".gr/scan-report.json", "scan report JSON output path")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON summary")
	trace := fs.Bool("trace", envBool("GR_TRACE"), "print detailed stage/progress logs")
	timeout := fs.Duration("timeout", 30*time.Minute, "workflow timeout")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	if *targetRegistry == "" {
		return nil, fmt.Errorf("missing --target-registry")
	}
	resolvedInputs, err := resolveInputs(*input, *inputs)
	if err != nil {
		return nil, err
	}
	dependencySet, err := dependencySelection(*includeDev, *includeOptional, *omit, *include)
	if err != nil {
		return nil, err
	}
	before, err := parseBefore(*beforeRaw)
	if err != nil {
		return nil, err
	}
	npmPlatforms, err := parseNPMPlatforms(*npmPlatformsRaw)
	if err != nil {
		return nil, err
	}

	resolveOpts := npm.ResolveOptions{
		IncludeDev:         dependencySet.includeDev,
		IncludeOptional:    dependencySet.includeOptional,
		LegacyPeerDeps:     *legacyPeerDeps,
		StrictPeerDeps:     *strictPeerDeps,
		OmitPeer:           dependencySet.omitPeer,
		PreferDedupe:       *preferDedupe,
		InstallStrategy:    *installStrategy,
		EngineStrict:       *engineStrict,
		NodeVersion:        *nodeVersion,
		Libc:               *libc,
		NPMPlatforms:       npmPlatforms,
		Before:             before,
		DefaultTag:         *defaultTag,
		IncludeStaged:      *includeStaged,
		Avoid:              *avoid,
		AvoidStrict:        *avoidStrict,
		ResolveConcurrency: *resolveConcurrency,
	}
	return &mirrorOptions{
		Inputs:                    resolvedInputs,
		ProjectConcurrency:        *projectConcurrency,
		Out:                       *out,
		StatePath:                 *statePath,
		Registry:                  *registry,
		TargetRegistry:            *targetRegistry,
		NPMRC:                     *npmrc,
		TargetNPMRC:               *targetNPMRC,
		TargetInsecureSkipVerify:  *targetInsecureSkipVerify,
		MetadataCache:             *metadataCache,
		MetadataCacheTTL:          *metadataCacheTTL,
		MetadataRetries:           *metadataRetries,
		SyncTarget:                *syncTarget,
		ResolveOptions:            resolveOpts,
		OutputNaming:              *outputNaming,
		FetchConcurrency:          *fetchConcurrency,
		TargetConcurrency:         *targetConcurrency,
		PushConcurrency:           *pushConcurrency,
		MaxRetries:                *maxRetries,
		PublishRetries:            *publishRetries,
		Tag:                       *tag,
		Access:                    *access,
		ScanAuto:                  *scanAuto,
		ScanEnforce:               *scanEnforce,
		ScanOSV:                   selectedBoolAlias(fs, "scan-vuln", *scanVuln, "scan-osv", *scanOSV),
		ScanProvider:              *scanProvider,
		ScanOSVEndpoint:           *scanOSVEndpoint,
		ScanOSVOfflineDBDir:       *scanOSVOfflineDBDir,
		ScanOSVAPIBatchSize:       *scanOSVAPIBatchSize,
		ScanOSVOfflineChunkSize:   *scanOSVOfflineChunkSize,
		ScanOSVOfflineRetryFailed: *scanOSVOfflineRetryFailed,
		ScanTrivyOfflineScan:      *scanTrivyOfflineScan,
		ScanTrivySkipDBUpdate:     *scanTrivySkipDBUpdate,
		ScanTrivyChunkSize:        *scanTrivyChunkSize,
		ScanMinSeverity:           *scanMinSeverity,
		ScanUnknownSeverity:       *scanUnknownSeverity,
		ScanExceptions:            *scanExceptions,
		ScanOSVAPIConcurrency:     *scanOSVAPIConcurrency,
		ScanOSVOfflineConcurrency: *scanOSVOfflineConcurrency,
		ScanTrivyConcurrency:      *scanTrivyConcurrency,
		ScanDenyPrefixes:          csvList(*scanDenyPackagePrefixes),
		ScanBlocklist:             *scanBlocklist,
		ScanReportPath:            *scanReportPath,
		JSONOut:                   *jsonOut,
		Trace:                     *trace,
		Timeout:                   *timeout,
	}, nil
}
