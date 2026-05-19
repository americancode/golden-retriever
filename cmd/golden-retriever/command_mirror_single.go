package main

import (
	"context"
	"fmt"

	"golden-retriever/internal/npm"
)

func runSingleMirror(ctx context.Context, opts *mirrorOptions) error {
	tracef := newTraceLogger(opts.Trace)
	progressf := newProgressLogger(!opts.Trace && !opts.JSONOut)
	logf := pickProgressLogger(opts.Trace, tracef, progressf)

	selectedInput := opts.Inputs[0]
	tracef("mirror:start input=%s target=%s timeout=%s", selectedInput, opts.TargetRegistry, opts.Timeout)
	if progressf != nil {
		progressf("resolve:start input=%s", selectedInput)
	}

	sourceClient, err := newClient(selectedInput, opts.Registry, opts.NPMRC, opts.MetadataCache, opts.MetadataCacheTTL, opts.MetadataRetries)
	if err != nil {
		return err
	}
	resolveOpts := opts.ResolveOptions
	resolveOpts.Progress = progressf
	graph, err := npm.LoadInput(ctx, sourceClient, selectedInput, resolveOpts)
	if err != nil {
		return err
	}
	if progressf != nil {
		progressf("resolve:done input=%s packages=%d", selectedInput, len(graph.Packages()))
	}
	tracef("mirror:resolve:done packages=%d", len(graph.Packages()))
	if !opts.JSONOut {
		printEngineWarnings(graph)
		printDeprecationWarnings(graph)
	}

	targetClient, err := newTargetClient(selectedInput, opts.TargetRegistry, firstNonEmpty(opts.TargetNPMRC, opts.NPMRC), opts.MetadataRetries, opts.TargetInsecureSkipVerify)
	if err != nil {
		return err
	}
	targetClient.UseStaleOnFailure = false
	logf("target-auth source=%s header=%s registry=%s", detectTargetAuthSource(opts.TargetRegistry, targetClient.Config), authHeaderKind(targetClient.Config, opts.TargetRegistry), opts.TargetRegistry)

	var syncReport npm.SyncTargetReport
	if opts.SyncTarget {
		tracef("mirror:sync-target:start")
		state, err := npm.LoadState(opts.StatePath)
		if err != nil {
			return err
		}
		syncReport, err = npm.SyncTarget(ctx, targetClient, state, graph.Packages(), npm.SyncTargetOptions{
			Concurrency: opts.TargetConcurrency,
			Source:      opts.TargetRegistry,
			Progress:    pickProgressLogger(opts.Trace, tracef, progressf),
		})
		if saveErr := npm.SaveState(opts.StatePath, state); saveErr != nil && err == nil {
			err = saveErr
		}
		if err != nil {
			return err
		}
		if !opts.JSONOut {
			fmt.Printf("target_sync packages=%d present=%d missing=%d failed=%d state=%s target=%s\n",
				len(graph.Packages()), syncReport.Present, syncReport.Missing, syncReport.Failed, opts.StatePath, opts.TargetRegistry)
		}
		tracef("mirror:sync-target:done present=%d missing=%d failed=%d", syncReport.Present, syncReport.Missing, syncReport.Failed)
	}

	tracef("mirror:fetch:start")
	fetchReport, err := npm.FetchAll(ctx, sourceClient, graph.Packages(), npm.FetchOptions{
		OutDir:             opts.Out,
		StatePath:          opts.StatePath,
		Concurrency:        opts.FetchConcurrency,
		MaxRetries:         opts.MaxRetries,
		OutputNameStrategy: opts.OutputNaming,
		Progress:           pickProgressLogger(opts.Trace, tracef, progressf),
	})
	if err != nil {
		return err
	}
	tracef("mirror:fetch:done downloaded=%d target_skipped=%d local_skipped=%d failed=%d", fetchReport.Downloaded, fetchReport.TargetSkipped, fetchReport.Skipped, fetchReport.Failed)

	if opts.ScanAuto {
		scanReport, scanErr := npm.ScanState(ctx, npm.ScanOptions{
			StatePath:             opts.StatePath,
			Concurrency:           opts.FetchConcurrency,
			BlocklistPath:         opts.ScanBlocklist,
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
			ExceptionsPath:        opts.ScanExceptions,
			OSVOfflineConcurrency: opts.ScanOSVOfflineConcurrency,
			TrivyConcurrency:      opts.ScanTrivyConcurrency,
			Progress:              pickProgressLogger(opts.Trace, tracef, progressf),
		})
		if writeErr := writeScanReport(opts.ScanReportPath, opts.StatePath, scanReport); writeErr != nil && scanErr == nil {
			scanErr = writeErr
		}
		fmt.Printf("scan total=%d passed=%d failed=%d errors=%d report=%s\n", scanReport.Total, scanReport.Passed, scanReport.Failed, scanReport.Errors, opts.ScanReportPath)
		tracef("mirror:scan:done total=%d passed=%d failed=%d errors=%d elapsed=%s", scanReport.Total, scanReport.Passed, scanReport.Failed, scanReport.Errors, scanReport.Elapsed)
		if scanErr != nil && opts.ScanEnforce {
			return scanErr
		}
	}

	state, err := npm.LoadState(opts.StatePath)
	if err != nil {
		return err
	}
	tracef("mirror:publish:start")
	pushReport, err := npm.PublishAll(ctx, targetClient, state, npm.PublishOptions{
		Concurrency:     opts.PushConcurrency,
		Source:          opts.TargetRegistry,
		Tag:             opts.Tag,
		Access:          opts.Access,
		MaxRetries:      opts.PublishRetries,
		Progress:        pickProgressLogger(opts.Trace, tracef, progressf),
		RequireScanPass: opts.ScanEnforce,
	})
	if saveErr := npm.SaveState(opts.StatePath, state); saveErr != nil && err == nil {
		err = saveErr
	}
	if err != nil {
		return err
	}
	tracef("mirror:publish:done pushed=%d present=%d skipped=%d failed=%d", pushReport.Pushed, pushReport.Present, pushReport.Skipped, pushReport.Failed)

	if opts.JSONOut {
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
			Command:             "mirror",
			Packages:            len(graph.Packages()),
			Fetch:               fetchReport,
			Push:                pushReport,
			TargetSync:          syncReport,
			TargetSynced:        opts.SyncTarget,
			Out:                 opts.Out,
			State:               opts.StatePath,
			TargetRegistry:      opts.TargetRegistry,
			EngineWarnings:      graph.EngineWarnings,
			DeprecationWarnings: graph.DeprecationWarnings,
		})
	}
	fmt.Printf("mirror packages=%d downloaded=%d downloaded_bytes=%d local_skipped=%d target_skipped=%d fetch_failed=%d pushed=%d already_present=%d push_skipped=%d push_failed=%d out=%s state=%s target=%s\n",
		len(graph.Packages()), fetchReport.Downloaded, fetchReport.DownloadedBytes, fetchReport.Skipped, fetchReport.TargetSkipped, fetchReport.Failed,
		pushReport.Pushed, pushReport.Present, pushReport.Skipped, pushReport.Failed, opts.Out, opts.StatePath, opts.TargetRegistry)
	return nil
}
