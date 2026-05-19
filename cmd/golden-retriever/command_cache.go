package main

import (
	"flag"
	"fmt"
	"time"

	"golden-retriever/internal/npm"
)

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
