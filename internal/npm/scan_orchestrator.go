package npm

import (
	"context"
	"strings"
	"sync"
	"time"
)

func ScanState(ctx context.Context, opts ScanOptions) (ScanReport, error) {
	start := time.Now()
	if opts.Concurrency <= 0 {
		opts.Concurrency = 8
	}
	if opts.OSVEndpoint == "" {
		opts.OSVEndpoint = "https://api.osv.dev/v1/querybatch"
	}
	if opts.OSVProvider == "" {
		opts.OSVProvider = "osv-api"
	}
	if opts.OSVAPIBatchSize <= 0 {
		opts.OSVAPIBatchSize = 200
	}
	if opts.OSVOfflineChunkSize <= 0 {
		opts.OSVOfflineChunkSize = 100
	}
	if opts.Source == "" {
		opts.Source = "local"
	}
	if opts.OSVAPIConcurrency <= 0 {
		opts.OSVAPIConcurrency = maxInt(4, opts.Concurrency/2)
	}
	if opts.OSVOfflineConcurrency <= 0 {
		opts.OSVOfflineConcurrency = maxInt(4, opts.Concurrency/2)
	}
	if opts.TrivyChunkSize <= 0 {
		opts.TrivyChunkSize = 100
	}
	if opts.TrivyConcurrency <= 0 {
		opts.TrivyConcurrency = maxInt(4, opts.Concurrency/2)
	}
	if opts.MinSeverity == "" {
		opts.MinSeverity = "high"
	}
	if opts.UnknownSeverity == "" {
		opts.UnknownSeverity = "high"
	}
	if opts.Progress != nil {
		provider := "disabled"
		if opts.UseOSV {
			provider = strings.ToLower(strings.TrimSpace(opts.OSVProvider))
			if provider == "" {
				provider = "osv-api"
			}
		}
		opts.Progress("scan:provider provider=%s source=%s osv=%t", provider, opts.Source, opts.UseOSV)
	}

	state, err := loadState(opts.StatePath)
	if err != nil {
		return ScanReport{}, err
	}
	normalizeState(state)

	blocklist, err := loadBlocklist(opts.BlocklistPath)
	if err != nil {
		return ScanReport{}, err
	}
	if len(blocklist.PackagePrefixes) > 0 {
		opts.DenyPackagePrefix = append(opts.DenyPackagePrefix, blocklist.PackagePrefixes...)
	}

	jobs := make(chan string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	keys := selectedScanKeys(state, opts.Source)
	report := ScanReport{Total: len(keys)}
	if len(keys) == 0 {
		if opts.Progress != nil {
			opts.Progress("scan:skip reason=no-packages source=%s", opts.Source)
		}
		report.Elapsed = time.Since(start)
		return report, saveState(opts.StatePath, state)
	}

	var firstErr error
	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for key := range jobs {
				select {
				case <-ctx.Done():
					return
				default:
				}
				mu.Lock()
				rec, bucket := getStateRecord(state, key, opts.Source)
				mu.Unlock()
				status, reason, err := scanRecord(rec, opts, blocklist, bucket == "local")
				mu.Lock()
				if err != nil {
					report.Errors++
					status = "fail"
					reason = err.Error()
					if firstErr == nil {
						firstErr = err
					}
				}
				if status == "pass" {
					report.Passed++
				} else {
					report.Failed++
					if opts.Progress != nil {
						opts.Progress("scan:drop package=%s@%s reason=%s", rec.Name, rec.Version, reason)
					}
				}
				rec.ScanStatus = status
				rec.ScanReason = reason
				rec.ScannedAt = time.Now().UTC()
				setStateRecord(state, key, bucket, rec)
				mu.Unlock()
			}
		}()
	}

	for _, key := range keys {
		jobs <- key
	}
	close(jobs)
	wg.Wait()

	state.UpdatedAt = time.Now().UTC()
	if opts.UseOSV {
		err := applyVulnerabilityProviderFindings(ctx, state, opts, keys)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		report = recomputeScanReport(state, keys, opts.Source)
	}

	if err := saveState(opts.StatePath, state); err != nil {
		return report, err
	}
	report.Elapsed = time.Since(start)
	return report, firstErr
}
