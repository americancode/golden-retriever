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
	npmPlatformsRaw := fs.String("npm-platforms", os.Getenv("GOLDEN_RETRIEVER_NPM_PLATFORMS"), "comma-separated npm resolve platforms for package.json inputs, such as linux/x64/glibc,darwin/arm64,win32/x64")
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
	npmPlatforms, err := parseNPMPlatforms(*npmPlatformsRaw)
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
		NPMPlatforms:       npmPlatforms,
		Before:             before,
		DefaultTag:         *defaultTag,
		IncludeStaged:      *includeStaged,
		Avoid:              *avoid,
		AvoidStrict:        *avoidStrict,
		ResolveConcurrency: *resolveConcurrency,
		Progress:           newProgressLogger(true),
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
