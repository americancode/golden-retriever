package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	out := fs.String("out", ".gr/tgzs", "target directory for downloaded package tarballs")
	statePath := fs.String("state", ".gr/state.json", "state inventory file")
	registry := fs.String("registry", "", "source npm registry base URL override")
	targetRegistry := fs.String("target-registry", "", "target npm registry base URL")
	npmrc := fs.String("npmrc", "", "additional npmrc file to load")
	targetNPMRC := fs.String("target-npmrc", "", "additional npmrc file for target registry auth")
	metadataCache := fs.String("metadata-cache", ".gr/metadata", "source packument metadata cache directory")
	metadataCacheTTL := fs.Duration("metadata-cache-ttl", 24*time.Hour, "source packument metadata cache freshness duration; 0 always revalidates")
	metadataRetries := fs.Int("metadata-retries", 3, "source packument metadata retry count for transient failures")
	offline := fs.Bool("offline", false, "resolve using only cached source registry metadata")
	includeDev := fs.Bool("include-dev", true, "include devDependencies from package.json roots")
	includeOptional := fs.Bool("include-optional", true, "include optionalDependencies")
	omit := fs.String("omit", "", "comma-separated dependency types to omit: dev, optional, peer")
	include := fs.String("include", "", "comma-separated dependency types to include after omit: dev, optional, peer")
	legacyPeerDeps := fs.Bool("legacy-peer-deps", false, "ignore peerDependencies")
	strictPeerDeps := fs.Bool("strict-peer-deps", false, "fail on peer dependency conflicts")
	engineStrict := fs.Bool("engine-strict", false, "fail on packages whose engines.node does not match --node-version")
	nodeVersion := fs.String("node-version", os.Getenv("NODE_VERSION"), "Node.js version used for engines.node checks")
	libc := fs.String("libc", os.Getenv("LIBC"), "libc value for package libc filters, such as glibc or musl")
	beforeRaw := fs.String("before", os.Getenv("NPM_BEFORE"), "only resolve package versions published at or before this RFC3339 timestamp")
	syncTarget := fs.Bool("sync-target", false, "query target registry first and rebuild target-present state for the resolved package set")
	resolveConcurrency := fs.Int("resolve-concurrency", max(8, runtime.NumCPU()*4), "parallel source registry metadata fetch count")
	fetchConcurrency := fs.Int("fetch-concurrency", max(8, runtime.NumCPU()*4), "parallel tarball download count")
	targetConcurrency := fs.Int("target-concurrency", max(8, runtime.NumCPU()*4), "parallel target registry query count")
	pushConcurrency := fs.Int("push-concurrency", max(4, runtime.NumCPU()*2), "parallel target registry publish count")
	outputNaming := fs.String("output-naming", "flat", "tarball output naming strategy: flat or registry")
	maxRetries := fs.Int("max-retries", 3, "tarball download retry count for transient failures")
	publishRetries := fs.Int("publish-retries", 3, "target registry publish retry count for transient failures")
	tag := fs.String("tag", "latest", "dist-tag to apply while publishing")
	access := fs.String("access", "public", "npm package access value")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON summary")
	timeout := fs.Duration("timeout", 30*time.Minute, "workflow timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targetRegistry == "" {
		return fmt.Errorf("missing --target-registry")
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

	sourceClient, err := newClient(*input, *registry, *npmrc, *metadataCache, *metadataCacheTTL, *metadataRetries, *offline)
	if err != nil {
		return err
	}
	graph, err := npm.LoadInput(ctx, sourceClient, *input, npm.ResolveOptions{
		IncludeDev:         dependencySet.includeDev,
		IncludeOptional:    dependencySet.includeOptional,
		LegacyPeerDeps:     *legacyPeerDeps,
		StrictPeerDeps:     *strictPeerDeps,
		OmitPeer:           dependencySet.omitPeer,
		EngineStrict:       *engineStrict,
		NodeVersion:        *nodeVersion,
		Libc:               *libc,
		Before:             before,
		ResolveConcurrency: *resolveConcurrency,
	})
	if err != nil {
		return err
	}
	if !*jsonOut {
		printEngineWarnings(graph)
		printDeprecationWarnings(graph)
	}

	targetClient, err := newClient(*input, *targetRegistry, firstNonEmpty(*targetNPMRC, *npmrc), "", 0, *metadataRetries, false)
	if err != nil {
		return err
	}
	targetClient.UseStaleOnFailure = false

	var syncReport npm.SyncTargetReport
	if *syncTarget {
		state, err := npm.LoadState(*statePath)
		if err != nil {
			return err
		}
		syncReport, err = npm.SyncTarget(ctx, targetClient, state, graph.Packages(), npm.SyncTargetOptions{
			Concurrency: *targetConcurrency,
			Source:      *targetRegistry,
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
	}

	fetchReport, err := npm.FetchAll(ctx, sourceClient, graph.Packages(), npm.FetchOptions{
		OutDir:             *out,
		StatePath:          *statePath,
		Concurrency:        *fetchConcurrency,
		MaxRetries:         *maxRetries,
		OutputNameStrategy: *outputNaming,
	})
	if err != nil {
		return err
	}

	state, err := npm.LoadState(*statePath)
	if err != nil {
		return err
	}
	pushReport, err := npm.PublishAll(ctx, targetClient, state, npm.PublishOptions{
		Concurrency: *pushConcurrency,
		Source:      *targetRegistry,
		Tag:         *tag,
		Access:      *access,
		MaxRetries:  *publishRetries,
	})
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
	concurrency := fs.Int("concurrency", max(4, runtime.NumCPU()*2), "parallel target registry publish count")
	tag := fs.String("tag", "latest", "dist-tag to apply while publishing")
	access := fs.String("access", "public", "npm package access value")
	maxRetries := fs.Int("max-retries", 3, "target registry publish retry count for transient failures")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON summary")
	timeout := fs.Duration("timeout", 10*time.Minute, "network timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targetRegistry == "" {
		return fmt.Errorf("missing --target-registry")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	state, err := npm.LoadState(*statePath)
	if err != nil {
		return err
	}
	targetClient, err := newClient(*input, *targetRegistry, *npmrc, "", 0, 3, false)
	if err != nil {
		return err
	}
	targetClient.UseStaleOnFailure = false
	report, err := npm.PublishAll(ctx, targetClient, state, npm.PublishOptions{
		Concurrency: *concurrency,
		Source:      *targetRegistry,
		Tag:         *tag,
		Access:      *access,
		MaxRetries:  *maxRetries,
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
	out := fs.String("out", "tgzs", "target directory for downloaded package tarballs")
	state := fs.String("state", ".gr/state.json", "persistent state file")
	registry := fs.String("registry", "", "npm registry base URL override")
	npmrc := fs.String("npmrc", "", "additional npmrc file to load")
	metadataCache := fs.String("metadata-cache", ".gr/metadata", "packument metadata cache directory")
	metadataCacheTTL := fs.Duration("metadata-cache-ttl", 24*time.Hour, "packument metadata cache freshness duration; 0 always revalidates")
	metadataRetries := fs.Int("metadata-retries", 3, "packument metadata retry count for transient failures")
	offline := fs.Bool("offline", false, "resolve using only cached registry metadata")
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
	engineStrict := fs.Bool("engine-strict", false, "fail on packages whose engines.node does not match --node-version")
	nodeVersion := fs.String("node-version", os.Getenv("NODE_VERSION"), "Node.js version used for engines.node checks")
	libc := fs.String("libc", os.Getenv("LIBC"), "libc value for package libc filters, such as glibc or musl")
	beforeRaw := fs.String("before", os.Getenv("NPM_BEFORE"), "only resolve package versions published at or before this RFC3339 timestamp")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON summary")
	timeout := fs.Duration("timeout", 5*time.Minute, "network timeout")
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

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client, err := newClient(*input, *registry, *npmrc, *metadataCache, *metadataCacheTTL, *metadataRetries, *offline)
	if err != nil {
		return err
	}
	graph, err := npm.LoadInput(ctx, client, *input, npm.ResolveOptions{
		IncludeDev:         dependencySet.includeDev,
		IncludeOptional:    dependencySet.includeOptional,
		LegacyPeerDeps:     *legacyPeerDeps,
		StrictPeerDeps:     *strictPeerDeps,
		OmitPeer:           dependencySet.omitPeer,
		EngineStrict:       *engineStrict,
		NodeVersion:        *nodeVersion,
		Libc:               *libc,
		Before:             before,
		ResolveConcurrency: *resolveConcurrency,
	})
	if err != nil {
		return err
	}
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
	offline := fs.Bool("offline", false, "resolve using only cached registry metadata")
	includeDev := fs.Bool("include-dev", true, "include devDependencies from package.json roots")
	includeOptional := fs.Bool("include-optional", true, "include optionalDependencies")
	omit := fs.String("omit", "", "comma-separated dependency types to omit: dev, optional, peer")
	include := fs.String("include", "", "comma-separated dependency types to include after omit: dev, optional, peer")
	legacyPeerDeps := fs.Bool("legacy-peer-deps", false, "ignore peerDependencies")
	strictPeerDeps := fs.Bool("strict-peer-deps", false, "fail on peer dependency conflicts")
	engineStrict := fs.Bool("engine-strict", false, "fail on packages whose engines.node does not match --node-version")
	nodeVersion := fs.String("node-version", os.Getenv("NODE_VERSION"), "Node.js version used for engines.node checks")
	libc := fs.String("libc", os.Getenv("LIBC"), "libc value for package libc filters, such as glibc or musl")
	beforeRaw := fs.String("before", os.Getenv("NPM_BEFORE"), "only resolve package versions published at or before this RFC3339 timestamp")
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

	client, err := newClient(*input, *registry, *npmrc, *metadataCache, *metadataCacheTTL, *metadataRetries, *offline)
	if err != nil {
		return err
	}
	graph, err := npm.LoadInput(context.Background(), client, *input, npm.ResolveOptions{
		IncludeDev:         dependencySet.includeDev,
		IncludeOptional:    dependencySet.includeOptional,
		LegacyPeerDeps:     *legacyPeerDeps,
		StrictPeerDeps:     *strictPeerDeps,
		OmitPeer:           dependencySet.omitPeer,
		EngineStrict:       *engineStrict,
		NodeVersion:        *nodeVersion,
		Libc:               *libc,
		Before:             before,
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
	statePath := fs.String("state", ".gr/state.json", "state inventory file")
	registry := fs.String("registry", "", "source npm registry base URL override")
	targetRegistry := fs.String("target-registry", "", "target npm registry base URL")
	npmrc := fs.String("npmrc", "", "additional npmrc file to load")
	targetNPMRC := fs.String("target-npmrc", "", "additional npmrc file for target registry auth")
	metadataCache := fs.String("metadata-cache", ".gr/metadata", "source packument metadata cache directory")
	metadataCacheTTL := fs.Duration("metadata-cache-ttl", 24*time.Hour, "source packument metadata cache freshness duration; 0 always revalidates")
	metadataRetries := fs.Int("metadata-retries", 3, "source packument metadata retry count for transient failures")
	offline := fs.Bool("offline", false, "resolve using only cached source registry metadata")
	includeDev := fs.Bool("include-dev", true, "include devDependencies from package.json roots")
	includeOptional := fs.Bool("include-optional", true, "include optionalDependencies")
	omit := fs.String("omit", "", "comma-separated dependency types to omit: dev, optional, peer")
	include := fs.String("include", "", "comma-separated dependency types to include after omit: dev, optional, peer")
	legacyPeerDeps := fs.Bool("legacy-peer-deps", false, "ignore peerDependencies")
	strictPeerDeps := fs.Bool("strict-peer-deps", false, "fail on peer dependency conflicts")
	engineStrict := fs.Bool("engine-strict", false, "fail on packages whose engines.node does not match --node-version")
	nodeVersion := fs.String("node-version", os.Getenv("NODE_VERSION"), "Node.js version used for engines.node checks")
	libc := fs.String("libc", os.Getenv("LIBC"), "libc value for package libc filters, such as glibc or musl")
	beforeRaw := fs.String("before", os.Getenv("NPM_BEFORE"), "only resolve package versions published at or before this RFC3339 timestamp")
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

	sourceClient, err := newClient(*input, *registry, *npmrc, *metadataCache, *metadataCacheTTL, *metadataRetries, *offline)
	if err != nil {
		return err
	}
	graph, err := npm.LoadInput(ctx, sourceClient, *input, npm.ResolveOptions{
		IncludeDev:         dependencySet.includeDev,
		IncludeOptional:    dependencySet.includeOptional,
		LegacyPeerDeps:     *legacyPeerDeps,
		StrictPeerDeps:     *strictPeerDeps,
		OmitPeer:           dependencySet.omitPeer,
		EngineStrict:       *engineStrict,
		NodeVersion:        *nodeVersion,
		Libc:               *libc,
		Before:             before,
		ResolveConcurrency: *resolveConcurrency,
	})
	if err != nil {
		return err
	}
	if !*jsonOut {
		printEngineWarnings(graph)
		printDeprecationWarnings(graph)
	}
	state, err := npm.LoadState(*statePath)
	if err != nil {
		return err
	}
	targetClient, err := newClient(*input, *targetRegistry, firstNonEmpty(*targetNPMRC, *npmrc), "", 0, *metadataRetries, false)
	if err != nil {
		return err
	}
	targetClient.UseStaleOnFailure = false

	report, err := npm.SyncTarget(ctx, targetClient, state, graph.Packages(), npm.SyncTargetOptions{
		Concurrency: *concurrency,
		Source:      *targetRegistry,
	})
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
			TargetSync          npm.SyncTargetReport            `json:"targetSync"`
			State               string                          `json:"state"`
			TargetRegistry      string                          `json:"targetRegistry"`
			EngineWarnings      []*npm.PackageEngineError       `json:"engineWarnings,omitempty"`
			DeprecationWarnings []npm.PackageDeprecationWarning `json:"deprecationWarnings,omitempty"`
		}{Command: "state sync-target", Packages: len(graph.Packages()), TargetSync: report, State: *statePath, TargetRegistry: *targetRegistry, EngineWarnings: graph.EngineWarnings, DeprecationWarnings: graph.DeprecationWarnings})
	}
	fmt.Printf("packages=%d target_present=%d target_missing=%d failed=%d elapsed=%s state=%s target=%s\n",
		len(graph.Packages()), report.Present, report.Missing, report.Failed, report.Elapsed, *statePath, *targetRegistry)
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

func newClient(input, registry, npmrc, metadataCache string, metadataCacheTTL time.Duration, metadataRetries int, offline bool) (*npm.Client, error) {
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
	client.Offline = offline
	return client, nil
}

func usage() {
	fmt.Fprintln(os.Stderr, `golden-retriever collects npm tarballs for air-gapped installs.

Commands:
  fetch     resolve and download every package tarball
  mirror    resolve, optionally sync target state, fetch tarballs, and push missing packages
  push      publish local tarballs missing from target registry
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
