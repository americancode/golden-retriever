package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"golden-retriever/internal/npm"
)

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
	npmPlatformsRaw := fs.String("npm-platforms", os.Getenv("GOLDEN_RETRIEVER_NPM_PLATFORMS"), "comma-separated npm resolve platforms for package.json inputs, such as linux/x64/glibc,darwin/arm64,win32/x64")
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
	npmPlatforms, err := parseNPMPlatforms(*npmPlatformsRaw)
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
				NPMPlatforms:       npmPlatforms,
				Before:             before,
				DefaultTag:         *defaultTag,
				IncludeStaged:      *includeStaged,
				Avoid:              *avoid,
				AvoidStrict:        *avoidStrict,
				ResolveConcurrency: *resolveConcurrency,
				Progress:           progressf,
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
		NPMPlatforms:       npmPlatforms,
		Before:             before,
		DefaultTag:         *defaultTag,
		IncludeStaged:      *includeStaged,
		Avoid:              *avoid,
		AvoidStrict:        *avoidStrict,
		ResolveConcurrency: *resolveConcurrency,
		Progress:           progressf,
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
