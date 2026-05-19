package main

import (
	"context"
	"fmt"
	"time"

	"golden-retriever/internal/npm"
)

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
