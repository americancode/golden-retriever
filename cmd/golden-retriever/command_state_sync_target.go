package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"golden-retriever/internal/npm"
)

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
	npmPlatformsRaw := fs.String("npm-platforms", os.Getenv("GOLDEN_RETRIEVER_NPM_PLATFORMS"), "comma-separated npm resolve platforms for package.json inputs, such as linux/x64/glibc,darwin/arm64,win32/x64")
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
	progressf := newProgressLogger(!*jsonOut)
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
		npmPlatforms, err := parseNPMPlatforms(*npmPlatformsRaw)
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
			NPMPlatforms:       npmPlatforms,
			Before:             before,
			DefaultTag:         *defaultTag,
			IncludeStaged:      *includeStaged,
			Avoid:              *avoid,
			AvoidStrict:        *avoidStrict,
			ResolveConcurrency: *resolveConcurrency,
			Progress:           progressf,
		}
		selectedInput = resolvedInputs[0]
		if len(resolvedInputs) > 1 {
			var warningsMu sync.Mutex
			packages, _, err = resolveProjectsParallel(ctx, resolvedInputs, *projectConcurrency, resolveOpts.NPMPlatforms, nil, func(currentInput string, platform *npm.NPMPlatform) (*npm.Graph, error) {
				_, _, metadata := multiProjectPaths(currentInput, "", *statePath, *metadataCache)
				sourceClient, clientErr := newClient(currentInput, *registry, *npmrc, metadata, *metadataCacheTTL, *metadataRetries)
				if clientErr != nil {
					return nil, clientErr
				}
				var (
					graph   *npm.Graph
					loadErr error
				)
				if platform != nil {
					graph, loadErr = npm.LoadInputForPlatform(ctx, sourceClient, currentInput, resolveOpts, *platform)
				} else {
					graph, loadErr = npm.LoadInput(ctx, sourceClient, currentInput, resolveOpts)
				}
				if loadErr == nil {
					warningsMu.Lock()
					engineWarnings = append(engineWarnings, graph.EngineWarnings...)
					deprecationWarnings = append(deprecationWarnings, graph.DeprecationWarnings...)
					warningsMu.Unlock()
				}
				return graph, loadErr
			})
			if err != nil {
				return err
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
