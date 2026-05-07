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
	registry := fs.String("registry", "https://registry.npmjs.org", "npm registry base URL")
	concurrency := fs.Int("concurrency", max(8, runtime.NumCPU()*4), "parallel download count")
	resolveConcurrency := fs.Int("resolve-concurrency", max(8, runtime.NumCPU()*4), "parallel registry metadata fetch count")
	includeDev := fs.Bool("include-dev", true, "include devDependencies from package.json roots")
	includeOptional := fs.Bool("include-optional", true, "include optionalDependencies")
	timeout := fs.Duration("timeout", 5*time.Minute, "network timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	client := npm.NewClient(*registry)
	graph, err := npm.LoadInput(ctx, client, *input, npm.ResolveOptions{
		IncludeDev:          *includeDev,
		IncludeOptional:     *includeOptional,
		ResolveConcurrency: *resolveConcurrency,
	})
	if err != nil {
		return err
	}

	report, err := npm.FetchAll(ctx, client, graph.Packages(), npm.FetchOptions{
		OutDir:      *out,
		StatePath:   *state,
		Concurrency: *concurrency,
	})
	if err != nil {
		return err
	}
	fmt.Printf("packages=%d downloaded=%d skipped=%d failed=%d out=%s state=%s\n",
		len(graph.Packages()), report.Downloaded, report.Skipped, report.Failed, *out, *state)
	return nil
}

func resolve(args []string) error {
	fs := flag.NewFlagSet("resolve", flag.ExitOnError)
	input := fs.String("input", "package.json", "package.json, package-lock.json, or npm-shrinkwrap.json")
	registry := fs.String("registry", "https://registry.npmjs.org", "npm registry base URL")
	includeDev := fs.Bool("include-dev", true, "include devDependencies from package.json roots")
	includeOptional := fs.Bool("include-optional", true, "include optionalDependencies")
	resolveConcurrency := fs.Int("resolve-concurrency", max(8, runtime.NumCPU()*4), "parallel registry metadata fetch count")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client := npm.NewClient(*registry)
	graph, err := npm.LoadInput(context.Background(), client, *input, npm.ResolveOptions{
		IncludeDev:          *includeDev,
		IncludeOptional:     *includeOptional,
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

func usage() {
	fmt.Fprintln(os.Stderr, `golden-retriever collects npm tarballs for air-gapped installs.

Commands:
  fetch     resolve and download every package tarball
  resolve   print the resolved package tarball set

Run "golden-retriever fetch -h" for flags.`)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
