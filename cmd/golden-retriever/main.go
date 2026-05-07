package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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
	case "resolve":
		return resolve(args[1:])
	case "state":
		return stateCmd(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
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
	maxRetries := fs.Int("max-retries", 3, "tarball download retry count for transient failures")
	includeDev := fs.Bool("include-dev", true, "include devDependencies from package.json roots")
	includeOptional := fs.Bool("include-optional", true, "include optionalDependencies")
	legacyPeerDeps := fs.Bool("legacy-peer-deps", false, "ignore peerDependencies")
	strictPeerDeps := fs.Bool("strict-peer-deps", false, "fail on peer dependency conflicts")
	timeout := fs.Duration("timeout", 5*time.Minute, "network timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client, err := newClient(*input, *registry, *npmrc, *metadataCache, *metadataCacheTTL, *metadataRetries, *offline)
	if err != nil {
		return err
	}
	graph, err := npm.LoadInput(ctx, client, *input, npm.ResolveOptions{
		IncludeDev:         *includeDev,
		IncludeOptional:    *includeOptional,
		LegacyPeerDeps:     *legacyPeerDeps,
		StrictPeerDeps:     *strictPeerDeps,
		ResolveConcurrency: *resolveConcurrency,
	})
	if err != nil {
		return err
	}

	report, err := npm.FetchAll(ctx, client, graph.Packages(), npm.FetchOptions{
		OutDir:      *out,
		StatePath:   *state,
		Concurrency: *concurrency,
		MaxRetries:  *maxRetries,
	})
	if err != nil {
		return err
	}
	fmt.Printf("packages=%d downloaded=%d local_skipped=%d target_skipped=%d failed=%d out=%s state=%s\n",
		len(graph.Packages()), report.Downloaded, report.Skipped, report.TargetSkipped, report.Failed, *out, *state)
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
	legacyPeerDeps := fs.Bool("legacy-peer-deps", false, "ignore peerDependencies")
	strictPeerDeps := fs.Bool("strict-peer-deps", false, "fail on peer dependency conflicts")
	resolveConcurrency := fs.Int("resolve-concurrency", max(8, runtime.NumCPU()*4), "parallel registry metadata fetch count")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, err := newClient(*input, *registry, *npmrc, *metadataCache, *metadataCacheTTL, *metadataRetries, *offline)
	if err != nil {
		return err
	}
	graph, err := npm.LoadInput(context.Background(), client, *input, npm.ResolveOptions{
		IncludeDev:         *includeDev,
		IncludeOptional:    *includeOptional,
		LegacyPeerDeps:     *legacyPeerDeps,
		StrictPeerDeps:     *strictPeerDeps,
		ResolveConcurrency: *resolveConcurrency,
	})
	if err != nil {
		return err
	}
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
	case "mark-target":
		return stateMarkTarget(args[1:])
	case "sync-target":
		return stateSyncTarget(args[1:])
	default:
		return fmt.Errorf("unknown state subcommand %q", args[0])
	}
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
	legacyPeerDeps := fs.Bool("legacy-peer-deps", false, "ignore peerDependencies")
	strictPeerDeps := fs.Bool("strict-peer-deps", false, "fail on peer dependency conflicts")
	resolveConcurrency := fs.Int("resolve-concurrency", max(8, runtime.NumCPU()*4), "parallel source registry metadata fetch count")
	concurrency := fs.Int("concurrency", max(8, runtime.NumCPU()*4), "parallel target registry query count")
	timeout := fs.Duration("timeout", 5*time.Minute, "network timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targetRegistry == "" {
		return fmt.Errorf("missing --target-registry")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	sourceClient, err := newClient(*input, *registry, *npmrc, *metadataCache, *metadataCacheTTL, *metadataRetries, *offline)
	if err != nil {
		return err
	}
	graph, err := npm.LoadInput(ctx, sourceClient, *input, npm.ResolveOptions{
		IncludeDev:         *includeDev,
		IncludeOptional:    *includeOptional,
		LegacyPeerDeps:     *legacyPeerDeps,
		StrictPeerDeps:     *strictPeerDeps,
		ResolveConcurrency: *resolveConcurrency,
	})
	if err != nil {
		return err
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
	fmt.Printf("packages=%d target_present=%d target_missing=%d failed=%d state=%s target=%s\n",
		len(graph.Packages()), report.Present, report.Missing, report.Failed, *statePath, *targetRegistry)
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

func newClient(input, registry, npmrc, metadataCache string, metadataCacheTTL time.Duration, metadataRetries int, offline bool) (*npm.Client, error) {
	cfg, err := npm.DiscoverConfig(filepath.Dir(input), npmrc)
	if err != nil {
		return nil, err
	}
	if registry != "" {
		cfg.Registry = registry
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
  resolve   print the resolved package tarball set
  state     manage target registry inventory state

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
