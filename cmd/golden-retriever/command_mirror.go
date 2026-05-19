package main

import (
	"context"
)

func mirror(args []string) error {
	opts, err := parseMirrorArgs(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	tracef := newTraceLogger(opts.Trace)
	progressf := newProgressLogger(!opts.Trace && !opts.JSONOut)
	logf := pickProgressLogger(opts.Trace, tracef, progressf)
	if len(opts.Inputs) > 1 {
		tracef("mirror:batch:start projects=%d target=%s timeout=%s", len(opts.Inputs), opts.TargetRegistry, opts.Timeout)
		return mirrorMany(ctx, mirrorManyOptionsFromOptions(opts, logf, progressf))
	}

	return runSingleMirror(ctx, opts)
}

func mirrorManyOptionsFromOptions(opts *mirrorOptions, logf, progressf func(format string, args ...any)) mirrorManyOptions {
	tracef := logf
	resolveOpts := opts.ResolveOptions
	resolveOpts.Progress = progressf
	return mirrorManyOptions{
		Inputs:                    opts.Inputs,
		ProjectConcurrency:        opts.ProjectConcurrency,
		OutBase:                   opts.Out,
		StateBase:                 opts.StatePath,
		Registry:                  opts.Registry,
		TargetRegistry:            opts.TargetRegistry,
		TargetInsecureSkipVerify:  opts.TargetInsecureSkipVerify,
		NPMRC:                     opts.NPMRC,
		TargetNPMRC:               opts.TargetNPMRC,
		MetadataCacheBase:         opts.MetadataCache,
		MetadataCacheTTL:          opts.MetadataCacheTTL,
		MetadataRetries:           opts.MetadataRetries,
		FetchConcurrency:          opts.FetchConcurrency,
		PushConcurrency:           opts.PushConcurrency,
		TargetConcurrency:         opts.TargetConcurrency,
		MaxRetries:                opts.MaxRetries,
		PublishRetries:            opts.PublishRetries,
		Tag:                       opts.Tag,
		Access:                    opts.Access,
		SyncTarget:                opts.SyncTarget,
		OutputNaming:              opts.OutputNaming,
		ResolveOptions:            resolveOpts,
		JSONOut:                   opts.JSONOut,
		Tracef:                    tracef,
		Progressf:                 progressf,
		ScanAuto:                  opts.ScanAuto,
		ScanEnforce:               opts.ScanEnforce,
		ScanDenyPrefixes:          opts.ScanDenyPrefixes,
		ScanOSV:                   opts.ScanOSV,
		ScanProvider:              opts.ScanProvider,
		ScanOSVEndpoint:           opts.ScanOSVEndpoint,
		ScanOSVOfflineDBDir:       opts.ScanOSVOfflineDBDir,
		ScanOSVAPIBatchSize:       opts.ScanOSVAPIBatchSize,
		ScanOSVOfflineChunkSize:   opts.ScanOSVOfflineChunkSize,
		ScanOSVOfflineRetryFailed: opts.ScanOSVOfflineRetryFailed,
		ScanTrivyOfflineScan:      opts.ScanTrivyOfflineScan,
		ScanTrivySkipDBUpdate:     opts.ScanTrivySkipDBUpdate,
		ScanTrivyChunkSize:        opts.ScanTrivyChunkSize,
		ScanBlocklistPath:         opts.ScanBlocklist,
		ScanReportPath:            opts.ScanReportPath,
		ScanMinSeverity:           opts.ScanMinSeverity,
		ScanUnknownSeverity:       opts.ScanUnknownSeverity,
		ScanExceptionsPath:        opts.ScanExceptions,
		ScanOSVAPIConcurrency:     opts.ScanOSVAPIConcurrency,
		ScanOSVOfflineConcurrency: opts.ScanOSVOfflineConcurrency,
		ScanTrivyConcurrency:      opts.ScanTrivyConcurrency,
	}
}
