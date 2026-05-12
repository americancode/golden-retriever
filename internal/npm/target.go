package npm

import (
	"context"
	"errors"
	"sync"
	"time"
)

type SyncTargetOptions struct {
	Concurrency int
	Source      string
	Progress    func(format string, args ...any)
}

type SyncTargetReport struct {
	Present int
	Missing int
	Failed  int
	Elapsed time.Duration
}

func SyncTarget(ctx context.Context, target *Client, state *State, packages []Package, opts SyncTargetOptions) (SyncTargetReport, error) {
	start := time.Now()
	if opts.Concurrency <= 0 {
		opts.Concurrency = 16
	}
	if opts.Source == "" {
		opts.Source = "target-registry"
	}
	normalizeState(state)
	total := len(packages)
	if opts.Progress != nil {
		opts.Progress("target-sync:start total=%d concurrency=%d source=%s", total, opts.Concurrency, opts.Source)
	}
	if total == 0 {
		report := SyncTargetReport{Elapsed: time.Since(start)}
		if opts.Progress != nil {
			opts.Progress("target-sync:skip reason=no-packages source=%s", opts.Source)
			opts.Progress("target-sync:done total=0 present=0 missing=0 failed=0 elapsed=%s", report.Elapsed)
		}
		state.UpdatedAt = time.Now().UTC()
		return report, nil
	}

	jobs := make(chan Package)
	var stateMu sync.Mutex
	var reportMu sync.Mutex
	var report SyncTargetReport
	var firstErr error
	var processed int
	var wg sync.WaitGroup

	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pkg := range jobs {
				presentPkg, present, err := targetPackageVersion(ctx, target, pkg)
				reportMu.Lock()
				processed++
				if err != nil {
					report.Failed++
					if firstErr == nil {
						firstErr = err
					}
					if opts.Progress != nil {
						opts.Progress("target-sync:fail processed=%d/%d package=%s error=%v", processed, total, pkg.Key(), err)
					}
					reportMu.Unlock()
					continue
				}
				if present {
					report.Present++
					stateMu.Lock()
					MarkTargetPresent(state, presentPkg, opts.Source)
					stateMu.Unlock()
				} else {
					report.Missing++
				}
				if opts.Progress != nil && processed%25 == 0 {
					opts.Progress("target-sync:progress processed=%d/%d present=%d missing=%d failed=%d", processed, total, report.Present, report.Missing, report.Failed)
				}
				reportMu.Unlock()
			}
		}()
	}

	for _, pkg := range packages {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			report.Elapsed = time.Since(start)
			return report, ctx.Err()
		case jobs <- pkg:
		}
	}
	close(jobs)
	wg.Wait()
	state.UpdatedAt = time.Now().UTC()
	report.Elapsed = time.Since(start)
	if opts.Progress != nil {
		opts.Progress("target-sync:done total=%d present=%d missing=%d failed=%d elapsed=%s", total, report.Present, report.Missing, report.Failed, report.Elapsed)
	}
	return report, firstErr
}

func targetPackageVersion(ctx context.Context, target *Client, pkg Package) (Package, bool, error) {
	pack, err := target.Packument(ctx, pkg.Name)
	if err != nil {
		if isHTTPStatus(err, 404) {
			return Package{}, false, nil
		}
		return Package{}, false, err
	}
	manifest, ok := pack.Versions[pkg.Version]
	if !ok {
		return Package{}, false, nil
	}
	present := pkg
	if manifest.Name != "" {
		present.Name = manifest.Name
	}
	if manifest.Version != "" {
		present.Version = manifest.Version
	}
	if manifest.Dist.Tarball != "" {
		present.Tarball = manifest.Dist.Tarball
	}
	if manifest.Dist.Integrity != "" {
		present.Integrity = manifest.Dist.Integrity
	}
	if manifest.Dist.Shasum != "" {
		present.Shasum = manifest.Dist.Shasum
	}
	return present, true, nil
}

func isHTTPStatus(err error, code int) bool {
	var statusErr httpStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode == code
	}
	return false
}
