package main

import (
	"context"
	"fmt"

	"golden-retriever/internal/npm"
)

func runScan(ctx context.Context, opts *scanOptions) error {
	tracef := newTraceLogger(opts.Trace)
	progressf := newProgressLogger(!opts.Trace && !opts.JSONOut)
	report, err := npm.ScanState(ctx, npm.ScanOptions{
		StatePath:             opts.StatePath,
		Concurrency:           opts.Concurrency,
		Source:                opts.Source,
		BlocklistPath:         opts.BlocklistPath,
		DenyPackagePrefix:     opts.DenyPackagePrefix,
		UseOSV:                opts.UseOSV,
		OSVProvider:           opts.OSVProvider,
		OSVEndpoint:           opts.OSVEndpoint,
		OSVOfflineDBDir:       opts.OSVOfflineDBDir,
		OSVAPIBatchSize:       opts.OSVAPIBatchSize,
		OSVAPIConcurrency:     opts.OSVAPIConcurrency,
		OSVOfflineChunkSize:   opts.OSVOfflineChunkSize,
		OSVOfflineRetryFailed: opts.OSVOfflineRetryFailed,
		TrivyOfflineScan:      opts.TrivyOfflineScan,
		TrivySkipDBUpdate:     opts.TrivySkipDBUpdate,
		TrivyChunkSize:        opts.TrivyChunkSize,
		MinSeverity:           opts.MinSeverity,
		UnknownSeverity:       opts.UnknownSeverity,
		ExceptionsPath:        opts.ExceptionsPath,
		OSVOfflineConcurrency: opts.OSVOfflineConcurrency,
		TrivyConcurrency:      opts.TrivyConcurrency,
		Progress:              pickProgressLogger(opts.Trace, tracef, progressf),
	})
	if writeErr := writeScanReport(opts.ReportPath, opts.StatePath, report); writeErr != nil && err == nil {
		err = writeErr
	}
	if opts.JSONOut {
		return printJSON(struct {
			Command string         `json:"command"`
			State   string         `json:"state"`
			Source  string         `json:"source"`
			Report  string         `json:"report"`
			Scan    npm.ScanReport `json:"scan"`
		}{Command: "scan", State: opts.StatePath, Source: opts.Source, Report: opts.ReportPath, Scan: report})
	}
	fmt.Printf("scan source=%s total=%d passed=%d failed=%d errors=%d elapsed=%s state=%s report=%s\n",
		opts.Source, report.Total, report.Passed, report.Failed, report.Errors, report.Elapsed, opts.StatePath, opts.ReportPath)
	return err
}
