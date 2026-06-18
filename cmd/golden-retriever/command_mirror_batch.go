package main

import (
	"context"
	"fmt"
	"golden-retriever/internal/npm"
	"time"
)

type mirrorManyOptions struct {
	Inputs                    []string
	ProjectConcurrency        int
	OutBase                   string
	StateBase                 string
	Registry                  string
	TargetRegistry            string
	TargetInsecureSkipVerify  bool
	NPMRC                     string
	TargetNPMRC               string
	MetadataCacheBase         string
	MetadataCacheTTL          time.Duration
	MetadataRetries           int
	FetchConcurrency          int
	PushConcurrency           int
	TargetConcurrency         int
	MaxRetries                int
	PublishRetries            int
	Tag                       string
	Access                    string
	SyncTarget                bool
	OutputNaming              string
	ResolveOptions            npm.ResolveOptions
	JSONOut                   bool
	Tracef                    func(format string, args ...any)
	Progressf                 func(format string, args ...any)
	ScanAuto                  bool
	ScanEnforce               bool
	ScanDenyPrefixes          []string
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
	ScanExceptionsPath        string
	ScanOSVAPIConcurrency     int
	ScanOSVOfflineConcurrency int
	ScanTrivyConcurrency      int
	ScanBlocklistPath         string
	ScanReportPath            string
}

func mirrorMany(ctx context.Context, opts mirrorManyOptions) error {
	packages, perProjectCounts, err := resolveProjectsParallel(ctx, opts.Inputs, opts.ProjectConcurrency, opts.ResolveOptions.NPMPlatforms, opts.Progressf, func(input string, platform *npm.NPMPlatform) (*npm.Graph, error) {
		_, _, metadata := multiProjectPaths(input, opts.OutBase, opts.StateBase, opts.MetadataCacheBase)
		sourceClient, err := newClient(input, opts.Registry, opts.NPMRC, metadata, opts.MetadataCacheTTL, opts.MetadataRetries)
		if err != nil {
			return nil, err
		}
		if platform != nil {
			return npm.LoadInputForPlatform(ctx, sourceClient, input, opts.ResolveOptions, *platform)
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
			StatePath:             opts.StateBase,
			Concurrency:           opts.FetchConcurrency,
			BlocklistPath:         opts.ScanBlocklistPath,
			DenyPackagePrefix:     opts.ScanDenyPrefixes,
			UseOSV:                opts.ScanOSV,
			OSVProvider:           opts.ScanProvider,
			OSVEndpoint:           opts.ScanOSVEndpoint,
			OSVOfflineDBDir:       opts.ScanOSVOfflineDBDir,
			OSVAPIBatchSize:       opts.ScanOSVAPIBatchSize,
			OSVAPIConcurrency:     opts.ScanOSVAPIConcurrency,
			OSVOfflineChunkSize:   opts.ScanOSVOfflineChunkSize,
			OSVOfflineRetryFailed: opts.ScanOSVOfflineRetryFailed,
			TrivyOfflineScan:      opts.ScanTrivyOfflineScan,
			TrivySkipDBUpdate:     opts.ScanTrivySkipDBUpdate,
			TrivyChunkSize:        opts.ScanTrivyChunkSize,
			MinSeverity:           opts.ScanMinSeverity,
			UnknownSeverity:       opts.ScanUnknownSeverity,
			ExceptionsPath:        opts.ScanExceptionsPath,
			OSVOfflineConcurrency: opts.ScanOSVOfflineConcurrency,
			TrivyConcurrency:      opts.ScanTrivyConcurrency,
			Progress:              pickProgressLogger(false, opts.Tracef, opts.Progressf),
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
