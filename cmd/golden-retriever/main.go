package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"golden-retriever/internal/npm"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return fmt.Errorf("missing command")
	}

	switch args[0] {
	case "fetch":
		return fetch(args[1:])
	case "mirror":
		return mirror(args[1:])
	case "push", "publish":
		return push(args[1:])
	case "resolve":
		return resolve(args[1:])
	case "scan":
		return scan(args[1:])
	case "state":
		return stateCmd(args[1:])
	case "cache":
		return cacheCmd(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func cacheCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing cache subcommand")
	}
	switch args[0] {
	case "prune":
		return cachePrune(args[1:])
	case "clear":
		return cacheClear(args[1:])
	default:
		return fmt.Errorf("unknown cache subcommand %q", args[0])
	}
}

func cachePrune(args []string) error {
	fs := flag.NewFlagSet("cache prune", flag.ExitOnError)
	cacheDir := fs.String("metadata-cache", ".gr/metadata", "packument metadata cache directory")
	maxAge := fs.Duration("max-age", 7*24*time.Hour, "remove cache entries older than this duration")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON summary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	report, err := npm.PruneMetadataCache(npm.CachePruneOptions{Dir: *cacheDir, MaxAge: *maxAge})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(struct {
			Command string               `json:"command"`
			Cache   string               `json:"cache"`
			Prune   npm.CachePruneReport `json:"prune"`
		}{Command: "cache prune", Cache: *cacheDir, Prune: report})
	}
	fmt.Printf("cache=%s scanned=%d removed=%d failed=%d elapsed=%s\n", *cacheDir, report.Scanned, report.Removed, report.Failed, report.Elapsed)
	return nil
}

func cacheClear(args []string) error {
	fs := flag.NewFlagSet("cache clear", flag.ExitOnError)
	cacheDir := fs.String("metadata-cache", ".gr/metadata", "packument metadata cache directory")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON summary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	report, err := npm.PruneMetadataCache(npm.CachePruneOptions{Dir: *cacheDir, RemoveAll: true})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(struct {
			Command string               `json:"command"`
			Cache   string               `json:"cache"`
			Prune   npm.CachePruneReport `json:"prune"`
		}{Command: "cache clear", Cache: *cacheDir, Prune: report})
	}
	fmt.Printf("cache=%s scanned=%d removed=%d failed=%d elapsed=%s\n", *cacheDir, report.Scanned, report.Removed, report.Failed, report.Elapsed)
	return nil
}

func mirror(args []string) error {
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
	scanOSV := fs.Bool("scan-osv", true, "query OSV for known vulnerable package versions")
	scanProvider := fs.String("scan-provider", "osv-api", "scan provider: osv-api or osv-offline")
	scanOSVEndpoint := fs.String("scan-osv-endpoint", "https://api.osv.dev/v1/querybatch", "OSV querybatch API endpoint")
	scanOSVOfflineDBDir := fs.String("scan-osv-offline-db", os.Getenv("OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY"), "local OSV scanner database cache directory for offline fallback")
	scanOSVBatchSize := fs.Int("scan-osv-batch-size", 200, "OSV query batch size")
	scanOSVOfflineChunkSize := fs.Int("scan-osv-offline-chunk-size", 100, "offline osv-scanner package chunk size")
	scanMinSeverity := fs.String("scan-min-severity", "high", "minimum OSV severity to fail: low, medium, high, critical")
	scanUnknownSeverity := fs.String("scan-unknown-severity", "high", "severity to assume when OSV severity is unavailable")
	scanExceptions := fs.String("scan-exceptions", "", "path to scan exceptions JSON file")
	scanOSVConcurrency := fs.Int("scan-osv-concurrency", max(4, runtime.NumCPU()/2), "parallel OSV vulnerability detail lookup count")
	scanBlocklist := fs.String("scan-blocklist", ".gr/scan-blocklist.json", "path to scan blocklist JSON file")
	scanReportPath := fs.String("scan-report", ".gr/scan-report.json", "scan report JSON output path")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON summary")
	trace := fs.Bool("trace", envBool("GR_TRACE"), "print detailed stage/progress logs")
	timeout := fs.Duration("timeout", 30*time.Minute, "workflow timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targetRegistry == "" {
		return fmt.Errorf("missing --target-registry")
	}
	resolvedInputs, err := resolveInputs(*input, *inputs)
	if err != nil {
		return err
	}
	dependencySet, err := dependencySelection(*includeDev, *includeOptional, *omit, *include)
	if err != nil {
		return err
	}
	before, err := parseBefore(*beforeRaw)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	tracef := newTraceLogger(*trace)
	progressf := newProgressLogger(!*trace && !*jsonOut)
	logf := pickProgressLogger(*trace, tracef, progressf)
	if len(resolvedInputs) > 1 {
		tracef("mirror:batch:start projects=%d target=%s timeout=%s", len(resolvedInputs), *targetRegistry, *timeout)
		return mirrorMany(ctx, mirrorManyOptions{
			Inputs:                   resolvedInputs,
			ProjectConcurrency:       *projectConcurrency,
			OutBase:                  *out,
			StateBase:                *statePath,
			Registry:                 *registry,
			TargetRegistry:           *targetRegistry,
			NPMRC:                    *npmrc,
			TargetNPMRC:              *targetNPMRC,
			TargetInsecureSkipVerify: *targetInsecureSkipVerify,
			MetadataCacheBase:        *metadataCache,
			MetadataCacheTTL:         *metadataCacheTTL,
			MetadataRetries:          *metadataRetries,
			FetchConcurrency:         *fetchConcurrency,
			PushConcurrency:          *pushConcurrency,
			TargetConcurrency:        *targetConcurrency,
			MaxRetries:               *maxRetries,
			PublishRetries:           *publishRetries,
			Tag:                      *tag,
			Access:                   *access,
			SyncTarget:               *syncTarget,
			OutputNaming:             *outputNaming,
			ResolveOptions: npm.ResolveOptions{
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
				Before:             before,
				DefaultTag:         *defaultTag,
				IncludeStaged:      *includeStaged,
				Avoid:              *avoid,
				AvoidStrict:        *avoidStrict,
				ResolveConcurrency: *resolveConcurrency,
			},
			JSONOut:                 *jsonOut,
			Tracef:                  tracef,
			Progressf:               progressf,
			ScanAuto:                *scanAuto,
			ScanEnforce:             *scanEnforce,
			ScanDenyPrefixes:        csvList(*scanDenyPackagePrefixes),
			ScanOSV:                 *scanOSV,
			ScanProvider:            *scanProvider,
			ScanOSVEndpoint:         *scanOSVEndpoint,
			ScanOSVOfflineDBDir:     *scanOSVOfflineDBDir,
			ScanOSVBatchSize:        *scanOSVBatchSize,
			ScanOSVOfflineChunkSize: *scanOSVOfflineChunkSize,
			ScanBlocklistPath:       *scanBlocklist,
			ScanReportPath:          *scanReportPath,
			ScanMinSeverity:         *scanMinSeverity,
			ScanUnknownSeverity:     *scanUnknownSeverity,
			ScanExceptionsPath:      *scanExceptions,
			ScanOSVConcurrency:      *scanOSVConcurrency,
		})
	}
	selectedInput := resolvedInputs[0]
	tracef("mirror:start input=%s target=%s timeout=%s", selectedInput, *targetRegistry, *timeout)
	if progressf != nil {
		progressf("resolve:start input=%s", selectedInput)
	}

	sourceClient, err := newClient(selectedInput, *registry, *npmrc, *metadataCache, *metadataCacheTTL, *metadataRetries)
	if err != nil {
		return err
	}
	tracef("mirror:resolve:start")
	graph, err := npm.LoadInput(ctx, sourceClient, selectedInput, npm.ResolveOptions{
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
		Before:             before,
		DefaultTag:         *defaultTag,
		IncludeStaged:      *includeStaged,
		Avoid:              *avoid,
		AvoidStrict:        *avoidStrict,
		ResolveConcurrency: *resolveConcurrency,
	})
	if err != nil {
		return err
	}
	if progressf != nil {
		progressf("resolve:done input=%s packages=%d", selectedInput, len(graph.Packages()))
	}
	tracef("mirror:resolve:done packages=%d", len(graph.Packages()))
	if !*jsonOut {
		printEngineWarnings(graph)
		printDeprecationWarnings(graph)
	}

	targetClient, err := newTargetClient(selectedInput, *targetRegistry, firstNonEmpty(*targetNPMRC, *npmrc), *metadataRetries, *targetInsecureSkipVerify)
	if err != nil {
		return err
	}
	targetClient.UseStaleOnFailure = false
	logf("target-auth source=%s header=%s registry=%s", detectTargetAuthSource(*targetRegistry, targetClient.Config), authHeaderKind(targetClient.Config, *targetRegistry), *targetRegistry)

	var syncReport npm.SyncTargetReport
	if *syncTarget {
		tracef("mirror:sync-target:start")
		state, err := npm.LoadState(*statePath)
		if err != nil {
			return err
		}
		syncReport, err = npm.SyncTarget(ctx, targetClient, state, graph.Packages(), npm.SyncTargetOptions{
			Concurrency: *targetConcurrency,
			Source:      *targetRegistry,
			Progress:    pickProgressLogger(*trace, tracef, progressf),
		})
		if saveErr := npm.SaveState(*statePath, state); saveErr != nil && err == nil {
			err = saveErr
		}
		if err != nil {
			return err
		}
		if !*jsonOut {
			fmt.Printf("target_sync packages=%d present=%d missing=%d failed=%d state=%s target=%s\n",
				len(graph.Packages()), syncReport.Present, syncReport.Missing, syncReport.Failed, *statePath, *targetRegistry)
		}
		tracef("mirror:sync-target:done present=%d missing=%d failed=%d", syncReport.Present, syncReport.Missing, syncReport.Failed)
	}

	tracef("mirror:fetch:start")
	fetchReport, err := npm.FetchAll(ctx, sourceClient, graph.Packages(), npm.FetchOptions{
		OutDir:             *out,
		StatePath:          *statePath,
		Concurrency:        *fetchConcurrency,
		MaxRetries:         *maxRetries,
		OutputNameStrategy: *outputNaming,
		Progress:           pickProgressLogger(*trace, tracef, progressf),
	})
	if err != nil {
		return err
	}
	tracef("mirror:fetch:done downloaded=%d target_skipped=%d local_skipped=%d failed=%d", fetchReport.Downloaded, fetchReport.TargetSkipped, fetchReport.Skipped, fetchReport.Failed)
	if *scanAuto {
		scanReport, scanErr := npm.ScanState(ctx, npm.ScanOptions{
			StatePath:           *statePath,
			Concurrency:         *fetchConcurrency,
			BlocklistPath:       *scanBlocklist,
			DenyPackagePrefix:   csvList(*scanDenyPackagePrefixes),
			UseOSV:              *scanOSV,
			OSVProvider:         *scanProvider,
			OSVEndpoint:         *scanOSVEndpoint,
			OSVOfflineDBDir:     *scanOSVOfflineDBDir,
			OSVBatchSize:        *scanOSVBatchSize,
			OSVOfflineChunkSize: *scanOSVOfflineChunkSize,
			MinSeverity:         *scanMinSeverity,
			UnknownSeverity:     *scanUnknownSeverity,
			ExceptionsPath:      *scanExceptions,
			OSVConcurrency:      *scanOSVConcurrency,
			Progress:            pickProgressLogger(*trace, tracef, progressf),
		})
		if writeErr := writeScanReport(*scanReportPath, *statePath, scanReport); writeErr != nil && scanErr == nil {
			scanErr = writeErr
		}
		fmt.Printf("scan total=%d passed=%d failed=%d errors=%d report=%s\n", scanReport.Total, scanReport.Passed, scanReport.Failed, scanReport.Errors, *scanReportPath)
		tracef("mirror:scan:done total=%d passed=%d failed=%d errors=%d elapsed=%s", scanReport.Total, scanReport.Passed, scanReport.Failed, scanReport.Errors, scanReport.Elapsed)
		if scanErr != nil && *scanEnforce {
			return scanErr
		}
	}

	state, err := npm.LoadState(*statePath)
	if err != nil {
		return err
	}
	tracef("mirror:publish:start")
	pushReport, err := npm.PublishAll(ctx, targetClient, state, npm.PublishOptions{
		Concurrency:     *pushConcurrency,
		Source:          *targetRegistry,
		Tag:             *tag,
		Access:          *access,
		MaxRetries:      *publishRetries,
		Progress:        pickProgressLogger(*trace, tracef, progressf),
		RequireScanPass: *scanEnforce,
	})
	if saveErr := npm.SaveState(*statePath, state); saveErr != nil && err == nil {
		err = saveErr
	}
	if err != nil {
		return err
	}
	tracef("mirror:publish:done pushed=%d present=%d skipped=%d failed=%d", pushReport.Pushed, pushReport.Present, pushReport.Skipped, pushReport.Failed)

	if *jsonOut {
		return printJSON(struct {
			Command             string                          `json:"command"`
			Packages            int                             `json:"packages"`
			Fetch               npm.FetchReport                 `json:"fetch"`
			Push                npm.PublishReport               `json:"push"`
			TargetSync          npm.SyncTargetReport            `json:"targetSync,omitempty"`
			TargetSynced        bool                            `json:"targetSynced"`
			Out                 string                          `json:"out"`
			State               string                          `json:"state"`
			TargetRegistry      string                          `json:"targetRegistry"`
			EngineWarnings      []*npm.PackageEngineError       `json:"engineWarnings,omitempty"`
			DeprecationWarnings []npm.PackageDeprecationWarning `json:"deprecationWarnings,omitempty"`
		}{
			Command: "mirror", Packages: len(graph.Packages()), Fetch: fetchReport, Push: pushReport,
			TargetSync: syncReport, TargetSynced: *syncTarget, Out: *out, State: *statePath, TargetRegistry: *targetRegistry,
			EngineWarnings: graph.EngineWarnings, DeprecationWarnings: graph.DeprecationWarnings,
		})
	}
	fmt.Printf("mirror packages=%d downloaded=%d downloaded_bytes=%d local_skipped=%d target_skipped=%d fetch_failed=%d fetch_elapsed=%s pushed=%d already_present=%d push_skipped=%d push_failed=%d out=%s state=%s target=%s\n",
		len(graph.Packages()), fetchReport.Downloaded, fetchReport.DownloadedBytes, fetchReport.Skipped, fetchReport.TargetSkipped, fetchReport.Failed,
		fetchReport.Elapsed, pushReport.Pushed, pushReport.Present, pushReport.Skipped, pushReport.Failed, *out, *statePath, *targetRegistry)
	return nil
}

func push(args []string) error {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	input := fs.String("input", "package.json", "package.json path used for project npmrc discovery")
	statePath := fs.String("state", ".gr/state.json", "state inventory file")
	targetRegistry := fs.String("target-registry", "", "target npm registry base URL")
	npmrc := fs.String("npmrc", "", "additional npmrc file for target registry auth")
	targetInsecureSkipVerify := fs.Bool("target-insecure-skip-verify", false, "skip TLS certificate verification for target registry HTTPS connections")
	concurrency := fs.Int("concurrency", max(4, runtime.NumCPU()*2), "parallel target registry publish count")
	tag := fs.String("tag", "latest", "dist-tag to apply while publishing")
	access := fs.String("access", "public", "npm package access value")
	maxRetries := fs.Int("max-retries", 3, "target registry publish retry count for transient failures")
	scanEnforce := fs.Bool("scan-enforce", false, "require local tarballs to pass scan gate before publishing")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON summary")
	trace := fs.Bool("trace", envBool("GR_TRACE"), "print detailed stage/progress logs")
	timeout := fs.Duration("timeout", 10*time.Minute, "network timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targetRegistry == "" {
		return fmt.Errorf("missing --target-registry")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	tracef := newTraceLogger(*trace)
	progressf := newProgressLogger(!*trace && !*jsonOut)
	logf := pickProgressLogger(*trace, tracef, progressf)
	tracef("push:start target=%s timeout=%s", *targetRegistry, *timeout)

	state, err := npm.LoadState(*statePath)
	if err != nil {
		return err
	}
	targetClient, err := newTargetClient(*input, *targetRegistry, *npmrc, 3, *targetInsecureSkipVerify)
	if err != nil {
		return err
	}
	targetClient.UseStaleOnFailure = false
	logf("target-auth source=%s header=%s registry=%s", detectTargetAuthSource(*targetRegistry, targetClient.Config), authHeaderKind(targetClient.Config, *targetRegistry), *targetRegistry)
	report, err := npm.PublishAll(ctx, targetClient, state, npm.PublishOptions{
		Concurrency:     *concurrency,
		Source:          *targetRegistry,
		Tag:             *tag,
		Access:          *access,
		MaxRetries:      *maxRetries,
		Progress:        pickProgressLogger(*trace, tracef, progressf),
		RequireScanPass: *scanEnforce,
	})
	if saveErr := npm.SaveState(*statePath, state); saveErr != nil && err == nil {
		err = saveErr
	}
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(struct {
			Command        string            `json:"command"`
			Push           npm.PublishReport `json:"push"`
			State          string            `json:"state"`
			TargetRegistry string            `json:"targetRegistry"`
		}{Command: "push", Push: report, State: *statePath, TargetRegistry: *targetRegistry})
	}
	fmt.Printf("pushed=%d already_present=%d skipped=%d failed=%d elapsed=%s state=%s target=%s\n",
		report.Pushed, report.Present, report.Skipped, report.Failed, report.Elapsed, *statePath, *targetRegistry)
	return nil
}

func fetch(args []string) error {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	input := fs.String("input", "package.json", "package.json, package-lock.json, or npm-shrinkwrap.json")
	inputs := fs.String("inputs", "", "comma-separated package.json/package-lock.json/npm-shrinkwrap.json paths")
	projectConcurrency := fs.Int("project-concurrency", max(1, runtime.NumCPU()/2), "parallel project workflow count when using --inputs")
	out := fs.String("out", "tgzs", "target directory for downloaded package tarballs")
	state := fs.String("state", ".gr/state.json", "persistent state file")
	registry := fs.String("registry", "", "npm registry base URL override")
	npmrc := fs.String("npmrc", "", "additional npmrc file to load")
	metadataCache := fs.String("metadata-cache", ".gr/metadata", "packument metadata cache directory")
	metadataCacheTTL := fs.Duration("metadata-cache-ttl", 24*time.Hour, "packument metadata cache freshness duration; 0 always revalidates")
	metadataRetries := fs.Int("metadata-retries", 3, "packument metadata retry count for transient failures")
	concurrency := fs.Int("concurrency", max(8, runtime.NumCPU()*4), "parallel download count")
	resolveConcurrency := fs.Int("resolve-concurrency", max(8, runtime.NumCPU()*4), "parallel registry metadata fetch count")
	outputNaming := fs.String("output-naming", "flat", "tarball output naming strategy: flat or registry")
	maxRetries := fs.Int("max-retries", 3, "tarball download retry count for transient failures")
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
	beforeRaw := fs.String("before", os.Getenv("NPM_BEFORE"), "only resolve package versions published at or before this RFC3339 timestamp")
	defaultTag := fs.String("default-tag", "latest", "default npm dist-tag used when a dependency has no explicit spec")
	includeStaged := fs.Bool("include-staged", false, "include npm stagedVersions metadata during manifest selection")
	avoid := fs.String("avoid", "", "semver range of versions to avoid during manifest selection")
	avoidStrict := fs.Bool("avoid-strict", false, "allow npm-pick-manifest style outside-range fallback when all matching versions are avoided")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON summary")
	trace := fs.Bool("trace", envBool("GR_TRACE"), "print detailed stage/progress logs")
	timeout := fs.Duration("timeout", 5*time.Minute, "network timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	resolvedInputs, err := resolveInputs(*input, *inputs)
	if err != nil {
		return err
	}
	dependencySet, err := dependencySelection(*includeDev, *includeOptional, *omit, *include)
	if err != nil {
		return err
	}
	before, err := parseBefore(*beforeRaw)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	tracef := newTraceLogger(*trace)
	progressf := newProgressLogger(!*trace && !*jsonOut)
	if len(resolvedInputs) > 1 {
		tracef("fetch:batch:start projects=%d timeout=%s", len(resolvedInputs), *timeout)
		return fetchMany(ctx, fetchManyOptions{
			Inputs:             resolvedInputs,
			ProjectConcurrency: *projectConcurrency,
			OutBase:            *out,
			StateBase:          *state,
			Registry:           *registry,
			NPMRC:              *npmrc,
			MetadataCacheBase:  *metadataCache,
			MetadataCacheTTL:   *metadataCacheTTL,
			MetadataRetries:    *metadataRetries,
			FetchConcurrency:   *concurrency,
			MaxRetries:         *maxRetries,
			OutputNaming:       *outputNaming,
			ResolveOptions: npm.ResolveOptions{
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
				Before:             before,
				DefaultTag:         *defaultTag,
				IncludeStaged:      *includeStaged,
				Avoid:              *avoid,
				AvoidStrict:        *avoidStrict,
				ResolveConcurrency: *resolveConcurrency,
			},
			JSONOut:   *jsonOut,
			Tracef:    tracef,
			Progressf: progressf,
		})
	}
	selectedInput := resolvedInputs[0]
	tracef("fetch:start input=%s timeout=%s", selectedInput, *timeout)
	if progressf != nil {
		progressf("resolve:start input=%s", selectedInput)
	}

	client, err := newClient(selectedInput, *registry, *npmrc, *metadataCache, *metadataCacheTTL, *metadataRetries)
	if err != nil {
		return err
	}
	tracef("fetch:resolve:start")
	graph, err := npm.LoadInput(ctx, client, selectedInput, npm.ResolveOptions{
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
		Before:             before,
		DefaultTag:         *defaultTag,
		IncludeStaged:      *includeStaged,
		Avoid:              *avoid,
		AvoidStrict:        *avoidStrict,
		ResolveConcurrency: *resolveConcurrency,
	})
	if err != nil {
		return err
	}
	if progressf != nil {
		progressf("resolve:done input=%s packages=%d", selectedInput, len(graph.Packages()))
	}
	tracef("fetch:resolve:done packages=%d", len(graph.Packages()))
	if !*jsonOut {
		printEngineWarnings(graph)
		printDeprecationWarnings(graph)
	}

	report, err := npm.FetchAll(ctx, client, graph.Packages(), npm.FetchOptions{
		OutDir:             *out,
		StatePath:          *state,
		Concurrency:        *concurrency,
		MaxRetries:         *maxRetries,
		OutputNameStrategy: *outputNaming,
		Progress:           pickProgressLogger(*trace, tracef, progressf),
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(struct {
			Command             string                          `json:"command"`
			Packages            int                             `json:"packages"`
			Fetch               npm.FetchReport                 `json:"fetch"`
			Out                 string                          `json:"out"`
			State               string                          `json:"state"`
			EngineWarnings      []*npm.PackageEngineError       `json:"engineWarnings,omitempty"`
			DeprecationWarnings []npm.PackageDeprecationWarning `json:"deprecationWarnings,omitempty"`
		}{Command: "fetch", Packages: len(graph.Packages()), Fetch: report, Out: *out, State: *state, EngineWarnings: graph.EngineWarnings, DeprecationWarnings: graph.DeprecationWarnings})
	}
	fmt.Printf("packages=%d downloaded=%d downloaded_bytes=%d local_skipped=%d target_skipped=%d failed=%d elapsed=%s out=%s state=%s\n",
		len(graph.Packages()), report.Downloaded, report.DownloadedBytes, report.Skipped, report.TargetSkipped, report.Failed, report.Elapsed, *out, *state)
	return nil
}

func resolve(args []string) error {
	fs := flag.NewFlagSet("resolve", flag.ExitOnError)
	input := fs.String("input", "package.json", "package.json, package-lock.json, or npm-shrinkwrap.json")
	registry := fs.String("registry", "", "npm registry base URL override")
	npmrc := fs.String("npmrc", "", "additional npmrc file to load")
	metadataCache := fs.String("metadata-cache", ".gr/metadata", "packument metadata cache directory")
	metadataCacheTTL := fs.Duration("metadata-cache-ttl", 24*time.Hour, "packument metadata cache freshness duration; 0 always revalidates")
	metadataRetries := fs.Int("metadata-retries", 3, "packument metadata retry count for transient failures")
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
	beforeRaw := fs.String("before", os.Getenv("NPM_BEFORE"), "only resolve package versions published at or before this RFC3339 timestamp")
	defaultTag := fs.String("default-tag", "latest", "default npm dist-tag used when a dependency has no explicit spec")
	includeStaged := fs.Bool("include-staged", false, "include npm stagedVersions metadata during manifest selection")
	avoid := fs.String("avoid", "", "semver range of versions to avoid during manifest selection")
	avoidStrict := fs.Bool("avoid-strict", false, "allow npm-pick-manifest style outside-range fallback when all matching versions are avoided")
	resolveConcurrency := fs.Int("resolve-concurrency", max(8, runtime.NumCPU()*4), "parallel registry metadata fetch count")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dependencySet, err := dependencySelection(*includeDev, *includeOptional, *omit, *include)
	if err != nil {
		return err
	}
	before, err := parseBefore(*beforeRaw)
	if err != nil {
		return err
	}

	client, err := newClient(*input, *registry, *npmrc, *metadataCache, *metadataCacheTTL, *metadataRetries)
	if err != nil {
		return err
	}
	graph, err := npm.LoadInput(context.Background(), client, *input, npm.ResolveOptions{
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
		Before:             before,
		DefaultTag:         *defaultTag,
		IncludeStaged:      *includeStaged,
		Avoid:              *avoid,
		AvoidStrict:        *avoidStrict,
		ResolveConcurrency: *resolveConcurrency,
	})
	if err != nil {
		return err
	}
	printEngineWarnings(graph)
	printDeprecationWarnings(graph)
	for _, pkg := range graph.Packages() {
		fmt.Printf("%s@%s %s\n", pkg.Name, pkg.Version, pkg.Tarball)
	}
	return nil
}

func scan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	statePath := fs.String("state", ".gr/state.json", "state inventory file")
	source := fs.String("source", "local", "scan source: local, target, or both")
	concurrency := fs.Int("concurrency", max(4, runtime.NumCPU()*2), "parallel scan worker count")
	blocklist := fs.String("blocklist", ".gr/scan-blocklist.json", "path to scan blocklist JSON file")
	denyPrefixes := fs.String("deny-package-prefixes", "", "comma-separated package name prefixes to block")
	useOSV := fs.Bool("osv", true, "query OSV for known vulnerable package versions")
	provider := fs.String("provider", "osv-api", "scan provider: osv-api or osv-offline")
	osvEndpoint := fs.String("osv-endpoint", "https://api.osv.dev/v1/querybatch", "OSV querybatch API endpoint")
	osvOfflineDBDir := fs.String("osv-offline-db", os.Getenv("OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY"), "local OSV scanner database cache directory for offline fallback")
	osvBatchSize := fs.Int("osv-batch-size", 200, "OSV query batch size")
	osvOfflineChunkSize := fs.Int("osv-offline-chunk-size", 100, "offline osv-scanner package chunk size")
	minSeverity := fs.String("min-severity", "high", "minimum OSV severity to fail: low, medium, high, critical")
	unknownSeverity := fs.String("unknown-severity", "high", "severity to assume when OSV severity is unavailable")
	exceptions := fs.String("exceptions", "", "path to scan exceptions JSON file")
	osvConcurrency := fs.Int("osv-concurrency", max(4, runtime.NumCPU()/2), "parallel OSV vulnerability detail lookup count")
	reportPath := fs.String("report", ".gr/scan-report.json", "scan report JSON output path")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON summary")
	if err := fs.Parse(args); err != nil {
		return err
	}
	report, err := npm.ScanState(context.Background(), npm.ScanOptions{
		StatePath:           *statePath,
		Concurrency:         *concurrency,
		Source:              *source,
		BlocklistPath:       *blocklist,
		DenyPackagePrefix:   csvList(*denyPrefixes),
		UseOSV:              *useOSV,
		OSVProvider:         *provider,
		OSVEndpoint:         *osvEndpoint,
		OSVOfflineDBDir:     *osvOfflineDBDir,
		OSVBatchSize:        *osvBatchSize,
		OSVOfflineChunkSize: *osvOfflineChunkSize,
		MinSeverity:         *minSeverity,
		UnknownSeverity:     *unknownSeverity,
		ExceptionsPath:      *exceptions,
		OSVConcurrency:      *osvConcurrency,
	})
	if writeErr := writeScanReport(*reportPath, *statePath, report); writeErr != nil && err == nil {
		err = writeErr
	}
	if *jsonOut {
		return printJSON(struct {
			Command string         `json:"command"`
			State   string         `json:"state"`
			Source  string         `json:"source"`
			Report  string         `json:"report"`
			Scan    npm.ScanReport `json:"scan"`
		}{Command: "scan", State: *statePath, Source: *source, Report: *reportPath, Scan: report})
	}
	fmt.Printf("scan source=%s total=%d passed=%d failed=%d errors=%d elapsed=%s state=%s report=%s\n",
		*source, report.Total, report.Passed, report.Failed, report.Errors, report.Elapsed, *statePath, *reportPath)
	return err
}

func stateCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("missing state subcommand")
	}
	switch args[0] {
	case "inspect":
		return stateInspect(args[1:])
	case "mark-target":
		return stateMarkTarget(args[1:])
	case "sync-target":
		return stateSyncTarget(args[1:])
	default:
		return fmt.Errorf("unknown state subcommand %q", args[0])
	}
}

func stateInspect(args []string) error {
	fs := flag.NewFlagSet("state inspect", flag.ExitOnError)
	statePath := fs.String("state", ".gr/state.json", "state inventory file")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON")
	validate := fs.Bool("validate-files", false, "verify local tarballs and remove invalid local records")
	if err := fs.Parse(args); err != nil {
		return err
	}
	state, err := npm.LoadState(*statePath)
	if err != nil {
		return err
	}
	var validation npm.StateValidationReport
	if *validate {
		validation = npm.ValidateStateFiles(state)
		if err := npm.SaveState(*statePath, state); err != nil {
			return err
		}
	}
	summary := npm.SummarizeState(state)
	if *jsonOut {
		payload := struct {
			npm.StateSummary
			Validation npm.StateValidationReport `json:"validation,omitempty"`
			StatePath  string                    `json:"statePath"`
		}{
			StateSummary: summary,
			Validation:   validation,
			StatePath:    *statePath,
		}
		data, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	}
	fmt.Printf("state=%s schema=%d target=%d local=%d failures=%d updated=%s\n",
		*statePath, summary.SchemaVersion, summary.Target, summary.Local, summary.Failures, summary.UpdatedAt.Format(time.RFC3339))
	if *validate {
		fmt.Printf("validation checked_local=%d valid_local=%d removed_local=%d\n",
			validation.CheckedLocal, validation.ValidLocal, validation.RemovedLocal)
	}
	return nil
}

func stateSyncTarget(args []string) error {
	fs := flag.NewFlagSet("state sync-target", flag.ExitOnError)
	input := fs.String("input", "package.json", "package.json, package-lock.json, or npm-shrinkwrap.json")
	inputs := fs.String("inputs", "", "comma-separated package.json/package-lock.json/npm-shrinkwrap.json paths")
	projectConcurrency := fs.Int("project-concurrency", max(1, runtime.NumCPU()/2), "parallel project resolution count when using --inputs")
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
	beforeRaw := fs.String("before", os.Getenv("NPM_BEFORE"), "only resolve package versions published at or before this RFC3339 timestamp")
	defaultTag := fs.String("default-tag", "latest", "default npm dist-tag used when a dependency has no explicit spec")
	includeStaged := fs.Bool("include-staged", false, "include npm stagedVersions metadata during manifest selection")
	avoid := fs.String("avoid", "", "semver range of versions to avoid during manifest selection")
	avoidStrict := fs.Bool("avoid-strict", false, "allow npm-pick-manifest style outside-range fallback when all matching versions are avoided")
	resolveConcurrency := fs.Int("resolve-concurrency", max(8, runtime.NumCPU()*4), "parallel source registry metadata fetch count")
	concurrency := fs.Int("concurrency", max(8, runtime.NumCPU()*4), "parallel target registry query count")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON summary")
	timeout := fs.Duration("timeout", 5*time.Minute, "network timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targetRegistry == "" {
		return fmt.Errorf("missing --target-registry")
	}
	explicitInputs := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "input" || f.Name == "inputs" {
			explicitInputs = true
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	state, err := npm.LoadState(*statePath)
	if err != nil {
		return err
	}
	selectedInput := "."
	var (
		packages            []npm.Package
		engineWarnings      []*npm.PackageEngineError
		deprecationWarnings []npm.PackageDeprecationWarning
		resolvedInputs      []string
	)
	if explicitInputs {
		resolvedInputs, err = resolveInputs(*input, *inputs)
		if err != nil {
			return err
		}
		dependencySet, err := dependencySelection(*includeDev, *includeOptional, *omit, *include)
		if err != nil {
			return err
		}
		before, err := parseBefore(*beforeRaw)
		if err != nil {
			return err
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
			Before:             before,
			DefaultTag:         *defaultTag,
			IncludeStaged:      *includeStaged,
			Avoid:              *avoid,
			AvoidStrict:        *avoidStrict,
			ResolveConcurrency: *resolveConcurrency,
		}
		selectedInput = resolvedInputs[0]
		if len(resolvedInputs) > 1 {
			var warningsMu sync.Mutex
			perProjectWarnings := map[string]*npm.Graph{}
			packages, _, err = resolveProjectsParallel(ctx, resolvedInputs, *projectConcurrency, nil, func(currentInput string) (*npm.Graph, error) {
				_, _, metadata := multiProjectPaths(currentInput, "", *statePath, *metadataCache)
				sourceClient, clientErr := newClient(currentInput, *registry, *npmrc, metadata, *metadataCacheTTL, *metadataRetries)
				if clientErr != nil {
					return nil, clientErr
				}
				graph, loadErr := npm.LoadInput(ctx, sourceClient, currentInput, resolveOpts)
				if loadErr == nil {
					warningsMu.Lock()
					perProjectWarnings[currentInput] = graph
					warningsMu.Unlock()
				}
				return graph, loadErr
			})
			if err != nil {
				return err
			}
			for _, currentInput := range resolvedInputs {
				graph := perProjectWarnings[currentInput]
				if graph == nil {
					continue
				}
				engineWarnings = append(engineWarnings, graph.EngineWarnings...)
				deprecationWarnings = append(deprecationWarnings, graph.DeprecationWarnings...)
			}
		} else {
			sourceClient, clientErr := newClient(selectedInput, *registry, *npmrc, *metadataCache, *metadataCacheTTL, *metadataRetries)
			if clientErr != nil {
				return clientErr
			}
			graph, loadErr := npm.LoadInput(ctx, sourceClient, selectedInput, resolveOpts)
			if loadErr != nil {
				return loadErr
			}
			packages = graph.Packages()
			engineWarnings = graph.EngineWarnings
			deprecationWarnings = graph.DeprecationWarnings
		}
		if !*jsonOut {
			printEngineWarnings(&npm.Graph{EngineWarnings: engineWarnings})
			printDeprecationWarnings(&npm.Graph{DeprecationWarnings: deprecationWarnings})
		}
	}
	targetClient, err := newTargetClient(selectedInput, *targetRegistry, firstNonEmpty(*targetNPMRC, *npmrc), *metadataRetries, *targetInsecureSkipVerify)
	if err != nil {
		return err
	}
	targetClient.UseStaleOnFailure = false
	fmt.Fprintf(os.Stderr, "progress target-auth source=%s header=%s registry=%s\n", detectTargetAuthSource(*targetRegistry, targetClient.Config), authHeaderKind(targetClient.Config, *targetRegistry), *targetRegistry)

	var report npm.SyncTargetReport
	if explicitInputs {
		report, err = npm.SyncTarget(ctx, targetClient, state, packages, npm.SyncTargetOptions{
			Concurrency: *concurrency,
			Source:      *targetRegistry,
			Progress:    newProgressLogger(!*jsonOut),
		})
	} else {
		report, err = npm.RebuildTargetFromRegistry(ctx, targetClient, state, npm.SyncTargetOptions{
			Source:   *targetRegistry,
			Progress: newProgressLogger(!*jsonOut),
		})
	}
	if saveErr := npm.SaveState(*statePath, state); saveErr != nil && err == nil {
		err = saveErr
	}
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(struct {
			Command             string                          `json:"command"`
			Packages            int                             `json:"packages"`
			Inputs              []string                        `json:"inputs,omitempty"`
			ProjectConcurrency  int                             `json:"projectConcurrency,omitempty"`
			TargetSync          npm.SyncTargetReport            `json:"targetSync"`
			State               string                          `json:"state"`
			TargetRegistry      string                          `json:"targetRegistry"`
			EngineWarnings      []*npm.PackageEngineError       `json:"engineWarnings,omitempty"`
			DeprecationWarnings []npm.PackageDeprecationWarning `json:"deprecationWarnings,omitempty"`
		}{Command: "state sync-target", Packages: len(packages), Inputs: resolvedInputs, ProjectConcurrency: *projectConcurrency, TargetSync: report, State: *statePath, TargetRegistry: *targetRegistry, EngineWarnings: engineWarnings, DeprecationWarnings: deprecationWarnings})
	}
	fmt.Printf("packages=%d target_present=%d target_missing=%d failed=%d elapsed=%s state=%s target=%s\n",
		len(packages), report.Present, report.Missing, report.Failed, report.Elapsed, *statePath, *targetRegistry)
	return nil
}

func stateMarkTarget(args []string) error {
	fs := flag.NewFlagSet("state mark-target", flag.ExitOnError)
	statePath := fs.String("state", ".gr/state.json", "state inventory file")
	pkgKey := fs.String("package", "", "package version as name@version")
	integrity := fs.String("integrity", "", "known package integrity")
	shasum := fs.String("shasum", "", "known package sha1 shasum")
	tarball := fs.String("tarball", "", "source tarball URL")
	source := fs.String("source", "manual", "inventory source label")
	if err := fs.Parse(args); err != nil {
		return err
	}
	name, version, err := splitPackageKey(*pkgKey)
	if err != nil {
		return err
	}
	state, err := npm.LoadState(*statePath)
	if err != nil {
		return err
	}
	npm.MarkTargetPresent(state, npm.Package{
		Name: name, Version: version, Tarball: *tarball,
		Integrity: *integrity, Shasum: *shasum,
	}, *source)
	if err := npm.SaveState(*statePath, state); err != nil {
		return err
	}
	fmt.Printf("marked target_present package=%s@%s state=%s\n", name, version, *statePath)
	return nil
}

func splitPackageKey(key string) (string, string, error) {
	if key == "" {
		return "", "", fmt.Errorf("missing --package")
	}
	start := 0
	if key[0] == '@' {
		start = 1
	}
	for i := len(key) - 1; i >= start; i-- {
		if key[i] == '@' {
			name := key[:i]
			version := key[i+1:]
			if name == "" || version == "" {
				break
			}
			return name, version, nil
		}
	}
	return "", "", fmt.Errorf("package must be name@version")
}

type dependencySet struct {
	includeDev      bool
	includeOptional bool
	omitPeer        bool
}

func dependencySelection(includeDev, includeOptional bool, omit, include string) (dependencySet, error) {
	set := dependencySet{
		includeDev:      includeDev,
		includeOptional: includeOptional,
	}
	for _, item := range dependencyTypes(omit) {
		switch item {
		case "dev":
			set.includeDev = false
		case "optional":
			set.includeOptional = false
		case "peer":
			set.omitPeer = true
		default:
			return dependencySet{}, fmt.Errorf("unsupported omit dependency type %q", item)
		}
	}
	for _, item := range dependencyTypes(include) {
		switch item {
		case "dev":
			set.includeDev = true
		case "optional":
			set.includeOptional = true
		case "peer":
			set.omitPeer = false
		default:
			return dependencySet{}, fmt.Errorf("unsupported include dependency type %q", item)
		}
	}
	return set, nil
}

func dependencyTypes(value string) []string {
	var out []string
	for _, item := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func parseBefore(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	before, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --before %q: expected RFC3339 timestamp", value)
	}
	return before, nil
}

func newClient(input, registry, npmrc, metadataCache string, metadataCacheTTL time.Duration, metadataRetries int) (*npm.Client, error) {
	cfg, err := npm.DiscoverConfig(filepath.Dir(input), npmrc)
	if err != nil {
		return nil, err
	}
	if registry != "" {
		cfg.Registry = registry
		cfg.ApplyEnvAuthForRegistry(registry)
	}
	client := npm.NewClientWithConfig(cfg)
	client.CacheDir = metadataCache
	client.CacheTTL = metadataCacheTTL
	client.PackumentRetries = metadataRetries
	client.Offline = false
	return client, nil
}

func newTargetClient(input, registry, npmrc string, metadataRetries int, insecureSkipVerify bool) (*npm.Client, error) {
	client, err := newClient(input, registry, npmrc, "", 0, metadataRetries)
	if err != nil {
		return nil, err
	}
	client.SetInsecureSkipVerify(insecureSkipVerify)
	return client, nil
}

type fetchManyOptions struct {
	Inputs             []string
	ProjectConcurrency int
	OutBase            string
	StateBase          string
	Registry           string
	NPMRC              string
	MetadataCacheBase  string
	MetadataCacheTTL   time.Duration
	MetadataRetries    int
	FetchConcurrency   int
	MaxRetries         int
	OutputNaming       string
	ResolveOptions     npm.ResolveOptions
	JSONOut            bool
	Tracef             func(format string, args ...any)
	Progressf          func(format string, args ...any)
}

func fetchMany(ctx context.Context, opts fetchManyOptions) error {
	packages, perProjectCounts, err := resolveProjectsParallel(ctx, opts.Inputs, opts.ProjectConcurrency, opts.Progressf, func(input string) (*npm.Graph, error) {
		_, _, metadata := multiProjectPaths(input, opts.OutBase, opts.StateBase, opts.MetadataCacheBase)
		client, err := newClient(input, opts.Registry, opts.NPMRC, metadata, opts.MetadataCacheTTL, opts.MetadataRetries)
		if err != nil {
			return nil, err
		}
		return npm.LoadInput(ctx, client, input, opts.ResolveOptions)
	})
	if err != nil {
		return err
	}
	primaryInput := opts.Inputs[0]
	client, err := newClient(primaryInput, opts.Registry, opts.NPMRC, opts.MetadataCacheBase, opts.MetadataCacheTTL, opts.MetadataRetries)
	if err != nil {
		return err
	}
	report, err := npm.FetchAll(ctx, client, packages, npm.FetchOptions{
		OutDir:             opts.OutBase,
		StatePath:          opts.StateBase,
		Concurrency:        opts.FetchConcurrency,
		MaxRetries:         opts.MaxRetries,
		OutputNameStrategy: opts.OutputNaming,
		Progress:           pickProgressLogger(false, opts.Tracef, opts.Progressf),
	})
	if err != nil {
		return err
	}
	if opts.JSONOut {
		return printJSON(struct {
			Command            string          `json:"command"`
			Inputs             []string        `json:"inputs"`
			UniquePackages     int             `json:"uniquePackages"`
			PerProject         map[string]int  `json:"perProjectPackages"`
			Fetch              npm.FetchReport `json:"fetch"`
			Out                string          `json:"out"`
			State              string          `json:"state"`
			ProjectConcurrency int             `json:"projectConcurrency"`
		}{
			Command: "fetch", Inputs: opts.Inputs, UniquePackages: len(packages), PerProject: perProjectCounts,
			Fetch: report, Out: opts.OutBase, State: opts.StateBase, ProjectConcurrency: opts.ProjectConcurrency,
		})
	}
	fmt.Printf("fetch inputs=%d unique_packages=%d downloaded=%d local_skipped=%d target_skipped=%d failed=%d elapsed=%s out=%s state=%s\n",
		len(opts.Inputs), len(packages), report.Downloaded, report.Skipped, report.TargetSkipped, report.Failed, report.Elapsed, opts.OutBase, opts.StateBase)
	return nil
}

type mirrorManyOptions struct {
	Inputs                   []string
	ProjectConcurrency       int
	OutBase                  string
	StateBase                string
	Registry                 string
	TargetRegistry           string
	TargetInsecureSkipVerify bool
	NPMRC                    string
	TargetNPMRC              string
	MetadataCacheBase        string
	MetadataCacheTTL         time.Duration
	MetadataRetries          int
	FetchConcurrency         int
	PushConcurrency          int
	TargetConcurrency        int
	MaxRetries               int
	PublishRetries           int
	Tag                      string
	Access                   string
	SyncTarget               bool
	OutputNaming             string
	ResolveOptions           npm.ResolveOptions
	JSONOut                  bool
	Tracef                   func(format string, args ...any)
	Progressf                func(format string, args ...any)
	ScanAuto                 bool
	ScanEnforce              bool
	ScanDenyPrefixes         []string
	ScanOSV                  bool
	ScanProvider             string
	ScanOSVEndpoint          string
	ScanOSVOfflineDBDir      string
	ScanOSVBatchSize         int
	ScanOSVOfflineChunkSize  int
	ScanMinSeverity          string
	ScanUnknownSeverity      string
	ScanExceptionsPath       string
	ScanOSVConcurrency       int
	ScanBlocklistPath        string
	ScanReportPath           string
}

func mirrorMany(ctx context.Context, opts mirrorManyOptions) error {
	packages, perProjectCounts, err := resolveProjectsParallel(ctx, opts.Inputs, opts.ProjectConcurrency, opts.Progressf, func(input string) (*npm.Graph, error) {
		_, _, metadata := multiProjectPaths(input, opts.OutBase, opts.StateBase, opts.MetadataCacheBase)
		sourceClient, err := newClient(input, opts.Registry, opts.NPMRC, metadata, opts.MetadataCacheTTL, opts.MetadataRetries)
		if err != nil {
			return nil, err
		}
		return npm.LoadInput(ctx, sourceClient, input, opts.ResolveOptions)
	})
	if err != nil {
		return err
	}
	primaryInput := opts.Inputs[0]
	sourceClient, err := newClient(primaryInput, opts.Registry, opts.NPMRC, opts.MetadataCacheBase, opts.MetadataCacheTTL, opts.MetadataRetries)
	if err != nil {
		return err
	}
	targetClient, err := newTargetClient(primaryInput, opts.TargetRegistry, firstNonEmpty(opts.TargetNPMRC, opts.NPMRC), opts.MetadataRetries, opts.TargetInsecureSkipVerify)
	if err != nil {
		return err
	}
	targetClient.UseStaleOnFailure = false
	if opts.Progressf != nil {
		opts.Progressf("target-auth source=%s header=%s registry=%s", detectTargetAuthSource(opts.TargetRegistry, targetClient.Config), authHeaderKind(targetClient.Config, opts.TargetRegistry), opts.TargetRegistry)
	}

	if opts.SyncTarget {
		state, err := npm.LoadState(opts.StateBase)
		if err != nil {
			return err
		}
		_, err = npm.SyncTarget(ctx, targetClient, state, packages, npm.SyncTargetOptions{
			Concurrency: opts.TargetConcurrency,
			Source:      opts.TargetRegistry,
			Progress:    pickProgressLogger(false, opts.Tracef, opts.Progressf),
		})
		if saveErr := npm.SaveState(opts.StateBase, state); saveErr != nil && err == nil {
			err = saveErr
		}
		if err != nil {
			return err
		}
	}

	fetchReport, err := npm.FetchAll(ctx, sourceClient, packages, npm.FetchOptions{
		OutDir:             opts.OutBase,
		StatePath:          opts.StateBase,
		Concurrency:        opts.FetchConcurrency,
		MaxRetries:         opts.MaxRetries,
		OutputNameStrategy: opts.OutputNaming,
		Progress:           pickProgressLogger(false, opts.Tracef, opts.Progressf),
	})
	if err != nil {
		return err
	}
	if opts.ScanAuto {
		scanReport, err := npm.ScanState(ctx, npm.ScanOptions{
			StatePath:           opts.StateBase,
			Concurrency:         opts.FetchConcurrency,
			BlocklistPath:       opts.ScanBlocklistPath,
			DenyPackagePrefix:   opts.ScanDenyPrefixes,
			UseOSV:              opts.ScanOSV,
			OSVProvider:         opts.ScanProvider,
			OSVEndpoint:         opts.ScanOSVEndpoint,
			OSVOfflineDBDir:     opts.ScanOSVOfflineDBDir,
			OSVBatchSize:        opts.ScanOSVBatchSize,
			OSVOfflineChunkSize: opts.ScanOSVOfflineChunkSize,
			MinSeverity:         opts.ScanMinSeverity,
			UnknownSeverity:     opts.ScanUnknownSeverity,
			ExceptionsPath:      opts.ScanExceptionsPath,
			OSVConcurrency:      opts.ScanOSVConcurrency,
			Progress:            pickProgressLogger(false, opts.Tracef, opts.Progressf),
		})
		if writeErr := writeScanReport(opts.ScanReportPath, opts.StateBase, scanReport); writeErr != nil && err == nil {
			err = writeErr
		}
		fmt.Printf("scan total=%d passed=%d failed=%d errors=%d report=%s\n", scanReport.Total, scanReport.Passed, scanReport.Failed, scanReport.Errors, opts.ScanReportPath)
		if err != nil && opts.ScanEnforce {
			return err
		}
	}
	state, err := npm.LoadState(opts.StateBase)
	if err != nil {
		return err
	}
	pushReport, err := npm.PublishAll(ctx, targetClient, state, npm.PublishOptions{
		Concurrency:     opts.PushConcurrency,
		Source:          opts.TargetRegistry,
		Tag:             opts.Tag,
		Access:          opts.Access,
		MaxRetries:      opts.PublishRetries,
		Progress:        pickProgressLogger(false, opts.Tracef, opts.Progressf),
		RequireScanPass: opts.ScanEnforce,
	})
	if saveErr := npm.SaveState(opts.StateBase, state); saveErr != nil && err == nil {
		err = saveErr
	}
	if err != nil {
		return err
	}
	if opts.JSONOut {
		return printJSON(struct {
			Command            string            `json:"command"`
			Inputs             []string          `json:"inputs"`
			UniquePackages     int               `json:"uniquePackages"`
			PerProject         map[string]int    `json:"perProjectPackages"`
			Fetch              npm.FetchReport   `json:"fetch"`
			Push               npm.PublishReport `json:"push"`
			Out                string            `json:"out"`
			State              string            `json:"state"`
			ProjectConcurrency int               `json:"projectConcurrency"`
		}{
			Command: "mirror", Inputs: opts.Inputs, UniquePackages: len(packages), PerProject: perProjectCounts,
			Fetch: fetchReport, Push: pushReport, Out: opts.OutBase, State: opts.StateBase, ProjectConcurrency: opts.ProjectConcurrency,
		})
	}
	fmt.Printf("mirror inputs=%d unique_packages=%d downloaded=%d target_skipped=%d local_skipped=%d fetch_failed=%d pushed=%d already_present=%d push_skipped=%d push_failed=%d out=%s state=%s target=%s\n",
		len(opts.Inputs), len(packages), fetchReport.Downloaded, fetchReport.TargetSkipped, fetchReport.Skipped, fetchReport.Failed,
		pushReport.Pushed, pushReport.Present, pushReport.Skipped, pushReport.Failed, opts.OutBase, opts.StateBase, opts.TargetRegistry)
	return nil
}

func resolveProjectsParallel(ctx context.Context, inputs []string, workers int, progressf func(format string, args ...any), resolveFn func(input string) (*npm.Graph, error)) ([]npm.Package, map[string]int, error) {
	type result struct {
		input string
		graph *npm.Graph
		err   error
	}
	jobs := make(chan string)
	results := make(chan result, len(inputs))
	var wg sync.WaitGroup

	for i := 0; i < max(1, workers); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for input := range jobs {
				if progressf != nil {
					if progressf != nil {
						progressf("resolve:start input=%s", input)
					}
				}
				graph, err := resolveFn(input)
				if err == nil && progressf != nil {
					if progressf != nil {
						progressf("resolve:done input=%s packages=%d", input, len(graph.Packages()))
					}
				}
				results <- result{input: input, graph: graph, err: err}
			}
		}()
	}
	for _, input := range inputs {
		jobs <- input
	}
	close(jobs)
	wg.Wait()
	close(results)

	unique := map[string]npm.Package{}
	perProject := map[string]int{}
	for res := range results {
		if res.err != nil {
			return nil, nil, fmt.Errorf("%s: %w", res.input, res.err)
		}
		pkgs := res.graph.Packages()
		perProject[res.input] = len(pkgs)
		for _, pkg := range pkgs {
			unique[pkg.Key()] = pkg
		}
	}
	merged := make([]npm.Package, 0, len(unique))
	for _, pkg := range unique {
		merged = append(merged, pkg)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Key() < merged[j].Key()
	})
	return merged, perProject, nil
}

func usage() {
	fmt.Fprintln(os.Stderr, `golden-retriever collects npm tarballs for air-gapped installs.

Commands:
  fetch     resolve and download every package tarball
  mirror    resolve, optionally sync target state, fetch tarballs, and push missing packages
  push      publish local tarballs missing from target registry
  scan      evaluate local tarballs and persist scan status in state
  resolve   print the resolved package tarball set
  state     manage target registry inventory state; subcommands: inspect, sync-target, mark-target
  cache     manage metadata cache; subcommands: prune, clear

Run "golden-retriever fetch -h" for flags.`)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func resolveInputs(input, inputs string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	add := func(v string) error {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil
		}
		abs, err := filepath.Abs(v)
		if err != nil {
			return err
		}
		if _, ok := seen[abs]; ok {
			return nil
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
		return nil
	}
	hasInputsList := strings.TrimSpace(inputs) != ""
	trimmedInput := strings.TrimSpace(input)
	includePrimaryInput := true
	if hasInputsList && (trimmedInput == "" || (trimmedInput == "package.json" && !fileExists(trimmedInput))) {
		includePrimaryInput = false
	}
	if includePrimaryInput {
		if err := add(input); err != nil {
			return nil, err
		}
	}
	for _, part := range strings.Split(inputs, ",") {
		if err := add(part); err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func projectSlug(input string) string {
	base := filepath.Base(input)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	dir := filepath.Base(filepath.Dir(input))
	slug := dir + "-" + base
	slug = strings.ToLower(slug)
	var b strings.Builder
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func multiProjectPaths(input, outBase, stateBase, metadataBase string) (string, string, string) {
	slug := projectSlug(input)
	out := filepath.Join(outBase, slug)
	metadata := filepath.Join(metadataBase, slug)
	state := stateBase
	if strings.HasSuffix(stateBase, ".json") {
		state = filepath.Join(filepath.Dir(stateBase), strings.TrimSuffix(filepath.Base(stateBase), ".json"), slug+".json")
	} else {
		state = filepath.Join(stateBase, slug+".json")
	}
	return out, state, metadata
}

func envBool(key string) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func csvList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func newTraceLogger(enabled bool) func(format string, args ...any) {
	if !enabled {
		return func(string, ...any) {}
	}
	return func(format string, args ...any) {
		ts := time.Now().UTC().Format(time.RFC3339)
		fmt.Fprintf(os.Stderr, "trace time=%s %s\n", ts, fmt.Sprintf(format, args...))
	}
}

func newProgressLogger(enabled bool) func(format string, args ...any) {
	if !enabled {
		return nil
	}
	return func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "progress %s\n", fmt.Sprintf(format, args...))
	}
}

func pickProgressLogger(traceEnabled bool, tracef, progressf func(format string, args ...any)) func(format string, args ...any) {
	if traceEnabled {
		return tracef
	}
	if progressf != nil {
		return progressf
	}
	return tracef
}

func detectTargetAuthSource(registry string, cfg *npm.Config) string {
	if cfg == nil {
		return "none"
	}
	header := cfg.AuthFor(strings.TrimRight(registry, "/") + "/-/whoami").Header
	if header == "" {
		return "none"
	}
	checkBearer := []string{"NPM_TARGET_TOKEN", "NPM_AUTH_TOKEN", "NODE_AUTH_TOKEN", "NPM_TOKEN", "CI_JOB_TOKEN"}
	for _, key := range checkBearer {
		if v := os.Getenv(key); v != "" && header == "Bearer "+v {
			return key
		}
	}
	checkUserPass := [][2]string{
		{"NPM_TARGET_USERNAME", "NPM_TARGET_PASSWORD"},
		{"CI_DEPLOY_USER", "CI_DEPLOY_PASSWORD"},
		{"NPM_USERNAME", "NPM_PASSWORD"},
	}
	for _, pair := range checkUserPass {
		u := os.Getenv(pair[0])
		p := os.Getenv(pair[1])
		if u == "" || p == "" {
			continue
		}
		if header == "Basic "+base64.StdEncoding.EncodeToString([]byte(u+":"+p)) {
			return pair[0] + "/" + pair[1]
		}
	}
	return "npmrc"
}

func authHeaderKind(cfg *npm.Config, registry string) string {
	if cfg == nil {
		return "none"
	}
	header := cfg.AuthFor(strings.TrimRight(registry, "/") + "/-/whoami").Header
	switch {
	case strings.HasPrefix(header, "Bearer "):
		return "bearer"
	case strings.HasPrefix(header, "Basic "):
		return "basic"
	case header == "":
		return "none"
	default:
		return "other"
	}
}

func printEngineWarnings(graph *npm.Graph) {
	if graph == nil {
		return
	}
	for _, warning := range graph.EngineWarnings {
		if warning == nil {
			continue
		}
		fmt.Fprintf(os.Stderr, "warn EBADENGINE package=%s required=%s@%s current=%s\n",
			warning.Package, warning.Engine, warning.Wanted, warning.Current)
	}
}

func printDeprecationWarnings(graph *npm.Graph) {
	if graph == nil {
		return
	}
	for _, warning := range graph.DeprecationWarnings {
		fmt.Fprintf(os.Stderr, "warn deprecated package=%s message=%s\n", warning.Package, warning.Message)
	}
}

func printJSON(value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func writeScanReport(path, statePath string, report npm.ScanReport) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	payload := struct {
		State string         `json:"state"`
		Scan  npm.ScanReport `json:"scan"`
	}{
		State: statePath,
		Scan:  report,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
