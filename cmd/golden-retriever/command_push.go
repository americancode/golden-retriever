package main

import (
	"context"
	"flag"
	"fmt"
	"runtime"
	"time"

	"golden-retriever/internal/npm"
)

func push(args []string) error {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	input := fs.String("input", "package.json", "package.json path used for project npmrc discovery")
	statePath := fs.String("state", ".gr/state.json", "state inventory file")
	targetRegistry := fs.String("target-registry", "", "target npm registry base URL")
	npmrc := fs.String("npmrc", "", "additional npmrc file for target registry auth")
	targetInsecureSkipVerify := fs.Bool("target-insecure-skip-verify", false, "skip TLS certificate verification for target registry HTTPS connections")
	concurrency := fs.Int("concurrency", max(4, runtime.NumCPU()*2), "parallel target registry publish count")
	tag := fs.String("tag", "latest", "dist-tag to apply while publishing")
	access := fs.String("access", "public", "npm package access value")
	maxRetries := fs.Int("max-retries", 3, "target registry publish retry count for transient failures")
	scanEnforce := fs.Bool("scan-enforce", false, "require local tarballs to pass scan gate before publishing")
	jsonOut := fs.Bool("json", false, "print machine-readable JSON summary")
	trace := fs.Bool("trace", envBool("GR_TRACE"), "print detailed stage/progress logs")
	timeout := fs.Duration("timeout", 10*time.Minute, "network timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *targetRegistry == "" {
		return fmt.Errorf("missing --target-registry")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	tracef := newTraceLogger(*trace)
	progressf := newProgressLogger(!*trace && !*jsonOut)
	logf := pickProgressLogger(*trace, tracef, progressf)
	tracef("push:start target=%s timeout=%s", *targetRegistry, *timeout)

	state, err := npm.LoadState(*statePath)
	if err != nil {
		return err
	}
	targetClient, err := newTargetClient(*input, *targetRegistry, *npmrc, 3, *targetInsecureSkipVerify)
	if err != nil {
		return err
	}
	targetClient.UseStaleOnFailure = false
	logf("target-auth source=%s header=%s registry=%s", detectTargetAuthSource(*targetRegistry, targetClient.Config), authHeaderKind(targetClient.Config, *targetRegistry), *targetRegistry)
	report, err := npm.PublishAll(ctx, targetClient, state, npm.PublishOptions{
		Concurrency:     *concurrency,
		Source:          *targetRegistry,
		Tag:             *tag,
		Access:          *access,
		MaxRetries:      *maxRetries,
		Progress:        pickProgressLogger(*trace, tracef, progressf),
		RequireScanPass: *scanEnforce,
	})
	if saveErr := npm.SaveState(*statePath, state); saveErr != nil && err == nil {
		err = saveErr
	}
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(struct {
			Command        string            `json:"command"`
			Push           npm.PublishReport `json:"push"`
			State          string            `json:"state"`
			TargetRegistry string            `json:"targetRegistry"`
		}{Command: "push", Push: report, State: *statePath, TargetRegistry: *targetRegistry})
	}
	fmt.Printf("pushed=%d already_present=%d skipped=%d failed=%d elapsed=%s state=%s target=%s\n",
		report.Pushed, report.Present, report.Skipped, report.Failed, report.Elapsed, *statePath, *targetRegistry)
	return nil
}
