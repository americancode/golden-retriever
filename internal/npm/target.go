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

	jobs := make(chan Package)
	var stateMu sync.Mutex
	var reportMu sync.Mutex
	var report SyncTargetReport
	var firstErr error
	var wg sync.WaitGroup

	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pkg := range jobs {
				presentPkg, present, err := targetPackageVersion(ctx, target, pkg)
				reportMu.Lock()
				if err != nil {
					report.Failed++
					if firstErr == nil {
						firstErr = err
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
