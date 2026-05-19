package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type osvOfflineProvider struct{}

type osvScannerCustomLockfile struct {
	Results []struct {
		Packages []struct {
			Package struct {
				Name      string `json:"name"`
				Version   string `json:"version,omitempty"`
				Ecosystem string `json:"ecosystem,omitempty"`
			} `json:"package"`
		} `json:"packages"`
	} `json:"results"`
}

type osvScannerOutput struct {
	Results []struct {
		Packages []struct {
			Package struct {
				Name      string `json:"name"`
				Version   string `json:"version,omitempty"`
				Ecosystem string `json:"ecosystem,omitempty"`
			} `json:"package"`
			Vulnerabilities []struct {
				ID               string   `json:"id"`
				Aliases          []string `json:"aliases"`
				Summary          string   `json:"summary"`
				Details          string   `json:"details"`
				DatabaseSpecific struct {
					Severity string `json:"severity"`
				} `json:"database_specific"`
			} `json:"vulnerabilities"`
		} `json:"packages"`
	} `json:"results"`
}

type osvScannerRecord struct {
	Key     string
	Name    string
	Version string
}

type osvScannerChunkError struct {
	Label    string
	Packages int
	First    string
	Last     string
	Err      error
}

func (e osvScannerChunkError) Error() string {
	return fmt.Sprintf("chunk=%s packages=%d first=%s last=%s: %v", e.Label, e.Packages, e.First, e.Last, e.Err)
}

func (e osvScannerChunkError) Unwrap() error {
	return e.Err
}

func (osvOfflineProvider) Name() string {
	return "osv-offline"
}

func (osvOfflineProvider) ApplyFindings(ctx context.Context, state *State, opts ScanOptions, keys []string) error {
	return applyOSVScannerFindings(ctx, state, opts, keys, true)
}

func applyOSVScannerFindings(ctx context.Context, state *State, opts ScanOptions, keys []string, offline bool) error {
	if _, err := exec.LookPath("osv-scanner"); err != nil {
		return fmt.Errorf("osv-scanner not available: %w", err)
	}
	exceptions, err := loadExceptions(opts.ExceptionsPath)
	if err != nil {
		return err
	}
	minLevel, err := parseSeverityLevel(opts.MinSeverity)
	if err != nil {
		return err
	}
	unknownLevel, err := parseSeverityLevel(opts.UnknownSeverity)
	if err != nil {
		return err
	}

	records := collectOSVScannerRecords(state, keys, opts.Source)
	if len(records) == 0 {
		if opts.Progress != nil {
			mode := "online"
			if offline {
				mode = "offline"
			}
			opts.Progress("osv:scanner:skip mode=%s provider=osv-scanner reason=no-packages", mode)
		}
		return nil
	}
	if offline && opts.OSVOfflineConcurrency > 1 && opts.OSVOfflineChunkSize > 0 && len(records) > opts.OSVOfflineChunkSize {
		return applyOSVScannerFindingsParallel(ctx, state, opts, records, minLevel, unknownLevel, exceptions)
	}
	lockfile, err := buildOSVScannerLockfileForRecords(records)
	if err != nil {
		return err
	}
	parsed, err := runOSVScanner(ctx, opts, lockfile, offline, "")
	if err != nil {
		return err
	}
	applyOSVScannerOutput(state, opts, parsed, minLevel, unknownLevel, exceptions)
	state.UpdatedAt = time.Now().UTC()
	if opts.Progress != nil {
		mode := "online"
		if offline {
			mode = "offline"
		}
		opts.Progress("osv:scanner:done mode=%s provider=osv-scanner", mode)
	}
	return nil
}

func applyOSVScannerFindingsParallel(ctx context.Context, state *State, opts ScanOptions, records []osvScannerRecord, minLevel, unknownLevel severityLevel, exceptions []ScanException) error {
	chunks := chunkOSVScannerRecords(records, opts.OSVOfflineChunkSize)
	if opts.Progress != nil {
		opts.Progress("osv:scanner:parallel-start chunks=%d chunk_size=%d concurrency=%d packages=%d", len(chunks), opts.OSVOfflineChunkSize, opts.OSVOfflineConcurrency, len(records))
	}
	type chunkResult struct {
		index   int
		outputs []osvScannerOutput
		err     error
	}
	jobs := make(chan int)
	results := make(chan chunkResult, len(chunks))
	workerCount := opts.OSVOfflineConcurrency
	if workerCount > len(chunks) {
		workerCount = len(chunks)
	}
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				chunk := chunks[idx]
				chunkLabel := fmt.Sprintf("%d/%d", idx+1, len(chunks))
				if opts.Progress != nil {
					opts.Progress("osv:scanner:chunk:start chunk=%s packages=%d", chunkLabel, len(chunk))
				}
				outputs, err := runOSVScannerChunk(ctx, opts, chunk, chunkLabel)
				if err != nil {
					err = osvScannerChunkError{
						Label:    chunkLabel,
						Packages: len(chunk),
						First:    packageLabel(firstOSVScannerRecord(chunk)),
						Last:     packageLabel(lastOSVScannerRecord(chunk)),
						Err:      err,
					}
				}
				results <- chunkResult{index: idx, outputs: outputs, err: err}
			}
		}()
	}
	for i := range chunks {
		jobs <- i
	}
	close(jobs)
	go func() {
		wg.Wait()
		close(results)
	}()
	completed := 0
	var firstErr error
	for result := range results {
		completed++
		chunkLabel := fmt.Sprintf("%d/%d", result.index+1, len(chunks))
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
			}
			if opts.Progress != nil {
				opts.Progress("osv:scanner:chunk:fail chunk=%s error=%v", chunkLabel, result.err)
			}
			continue
		}
		for _, output := range result.outputs {
			applyOSVScannerOutput(state, opts, output, minLevel, unknownLevel, exceptions)
		}
		if opts.Progress != nil {
			opts.Progress("osv:scanner:chunk:done chunk=%s completed=%d/%d", chunkLabel, completed, len(chunks))
		}
	}
	state.UpdatedAt = time.Now().UTC()
	if opts.Progress != nil {
		if firstErr != nil {
			opts.Progress("osv:scanner:parallel-fail chunks=%d completed=%d error=%v", len(chunks), completed, firstErr)
		} else {
			opts.Progress("osv:scanner:parallel-done chunks=%d completed=%d", len(chunks), completed)
			opts.Progress("osv:scanner:done mode=offline provider=osv-scanner")
		}
	}
	return firstErr
}

func runOSVScannerChunk(ctx context.Context, opts ScanOptions, chunk []osvScannerRecord, chunkLabel string) ([]osvScannerOutput, error) {
	lockfile, err := buildOSVScannerLockfileForRecords(chunk)
	if err != nil {
		return nil, err
	}
	parsed, err := runOSVScanner(ctx, opts, lockfile, true, chunkLabel)
	if err == nil {
		return []osvScannerOutput{parsed}, nil
	}
	if !opts.OSVOfflineRetryFailed || len(chunk) <= 1 {
		return nil, err
	}
	nextSize := len(chunk) / 2
	if nextSize < 1 {
		nextSize = 1
	}
	if opts.Progress != nil {
		opts.Progress("osv:scanner:chunk:retry chunk=%s packages=%d next_chunk_size=%d error=%v", chunkLabel, len(chunk), nextSize, err)
	}
	subchunks := chunkOSVScannerRecords(chunk, nextSize)
	outputs := make([]osvScannerOutput, 0, len(subchunks))
	for i, subchunk := range subchunks {
		subLabel := fmt.Sprintf("%s.%d/%d", chunkLabel, i+1, len(subchunks))
		if opts.Progress != nil {
			opts.Progress("osv:scanner:chunk:start chunk=%s packages=%d", subLabel, len(subchunk))
		}
		subOutputs, subErr := runOSVScannerChunk(ctx, opts, subchunk, subLabel)
		if subErr != nil {
			return nil, subErr
		}
		outputs = append(outputs, subOutputs...)
		if opts.Progress != nil {
			opts.Progress("osv:scanner:chunk:done chunk=%s completed=%d/%d", subLabel, i+1, len(subchunks))
		}
	}
	return outputs, nil
}

func runOSVScanner(ctx context.Context, opts ScanOptions, lockfile osvScannerCustomLockfile, offline bool, chunkLabel string) (osvScannerOutput, error) {
	tmpDir, err := os.MkdirTemp("", "golden-retriever-osv-*")
	if err != nil {
		return osvScannerOutput{}, err
	}
	defer os.RemoveAll(tmpDir)

	lockfilePath := filepath.Join(tmpDir, "osv-scanner.json")
	data, err := json.Marshal(lockfile)
	if err != nil {
		return osvScannerOutput{}, err
	}
	if err := os.WriteFile(lockfilePath, data, 0o644); err != nil {
		return osvScannerOutput{}, err
	}

	args := []string{"scan", "--format", "json", "--lockfile", "osv-scanner:" + lockfilePath}
	if offline {
		args = append(args, "--experimental-offline-vulnerabilities")
	}
	if opts.Progress != nil {
		mode := "online"
		if offline {
			mode = "offline"
		}
		packageCount := countOSVScannerPackages(lockfile)
		if chunkLabel == "" {
			opts.Progress("osv:scanner:start mode=%s provider=osv-scanner packages=%d", mode, packageCount)
		} else {
			opts.Progress("osv:scanner:start mode=%s provider=osv-scanner chunk=%s packages=%d", mode, chunkLabel, packageCount)
		}
	}
	cmd := exec.CommandContext(ctx, "osv-scanner", args...)
	cmd.Dir = tmpDir
	env := os.Environ()
	if strings.TrimSpace(opts.OSVOfflineDBDir) != "" {
		env = append(env, "OSV_SCANNER_LOCAL_DB_CACHE_DIRECTORY="+opts.OSVOfflineDBDir)
	}
	cmd.Env = env
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := runOSVScannerCommand(ctx, cmd, opts, offline, chunkLabel, countOSVScannerPackages(lockfile))
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) || (exitErr.ExitCode() != 1 && exitErr.ExitCode() != 0) {
			return osvScannerOutput{}, fmt.Errorf("osv-scanner failed: %w: %s", runErr, strings.TrimSpace(stderr.String()))
		}
	}

	var parsed osvScannerOutput
	if err := json.Unmarshal([]byte(stdout.String()), &parsed); err != nil {
		return osvScannerOutput{}, fmt.Errorf("parse osv-scanner output: %w", err)
	}
	return parsed, nil
}

func applyOSVScannerOutput(state *State, opts ScanOptions, parsed osvScannerOutput, minLevel, unknownLevel severityLevel, exceptions []ScanException) {
	for _, result := range parsed.Results {
		for _, item := range result.Packages {
			name := strings.TrimSpace(item.Package.Name)
			version := strings.TrimSpace(item.Package.Version)
			if name == "" || version == "" || len(item.Vulnerabilities) == 0 {
				continue
			}
			key := name + "@" + version
			rec, bucket := getStateRecord(state, key, opts.Source)
			if rec.Name == "" || rec.Version == "" {
				continue
			}
			hitIDs := make([]string, 0, len(item.Vulnerabilities))
			descriptions := make([]string, 0, len(item.Vulnerabilities))
			block := false
			highest := sevNone
			for _, vuln := range item.Vulnerabilities {
				if vuln.ID == "" {
					continue
				}
				if isExceptionMatch(exceptions, rec, vuln.ID) {
					continue
				}
				hitIDs = append(hitIDs, vuln.ID)
				descriptions = appendVulnDescription(descriptions, vuln.ID, firstNonEmptyString(vuln.Summary, vuln.Details))
				level := parseScannerSeverity(vuln.DatabaseSpecific.Severity, unknownLevel)
				if level > highest {
					highest = level
				}
				if level >= minLevel {
					block = true
				}
			}
			if len(hitIDs) == 0 || !block {
				continue
			}
			rec.ScanStatus = "fail"
			rec.ScanReason = fmt.Sprintf("osv vulnerabilities (%s+): %s", opts.MinSeverity, strings.Join(hitIDs, ","))
			rec.ScanVulnURLs = vulnURLs(hitIDs)
			rec.ScanVulnDescriptions = appendUniqueStrings(rec.ScanVulnDescriptions, descriptions...)
			rec.ScannedAt = time.Now().UTC()
			setStateRecord(state, key, bucket, rec)
			if opts.Progress != nil {
				opts.Progress("scan:vuln package=%s@%s severity=%s ids=%s urls=%s descriptions=%s provider=osv-scanner", rec.Name, rec.Version, highest.String(), strings.Join(hitIDs, ","), strings.Join(rec.ScanVulnURLs, ","), strings.Join(rec.ScanVulnDescriptions, " | "))
			}
		}
	}
}

func runOSVScannerCommand(ctx context.Context, cmd *exec.Cmd, opts ScanOptions, offline bool, chunkLabel string, packageCount int) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()
	if opts.Progress == nil {
		return <-done
	}
	mode := "online"
	if offline {
		mode = "offline"
	}
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	start := time.Now()
	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			if chunkLabel == "" {
				opts.Progress("osv:scanner:progress mode=%s elapsed=%s packages=%d", mode, time.Since(start).Round(time.Second), packageCount)
			} else {
				opts.Progress("osv:scanner:progress mode=%s chunk=%s elapsed=%s packages=%d", mode, chunkLabel, time.Since(start).Round(time.Second), packageCount)
			}
		case <-ctx.Done():
			<-done
			return ctx.Err()
		}
	}
}

func buildOSVScannerLockfile(state *State, keys []string, source string) (osvScannerCustomLockfile, error) {
	return buildOSVScannerLockfileForRecords(collectOSVScannerRecords(state, keys, source))
}

func collectOSVScannerRecords(state *State, keys []string, source string) []osvScannerRecord {
	seen := map[string]struct{}{}
	records := make([]osvScannerRecord, 0, len(keys))
	for _, key := range keys {
		rec, _ := getStateRecord(state, key, source)
		if rec.Name == "" || rec.Version == "" {
			continue
		}
		pkgKey := rec.Name + "@" + rec.Version
		if _, ok := seen[pkgKey]; ok {
			continue
		}
		seen[pkgKey] = struct{}{}
		records = append(records, osvScannerRecord{Key: pkgKey, Name: rec.Name, Version: rec.Version})
	}
	return records
}

func buildOSVScannerLockfileForRecords(records []osvScannerRecord) (osvScannerCustomLockfile, error) {
	lockfile := osvScannerCustomLockfile{}
	result := struct {
		Packages []struct {
			Package struct {
				Name      string `json:"name"`
				Version   string `json:"version,omitempty"`
				Ecosystem string `json:"ecosystem,omitempty"`
			} `json:"package"`
		} `json:"packages"`
	}{Packages: make([]struct {
		Package struct {
			Name      string `json:"name"`
			Version   string `json:"version,omitempty"`
			Ecosystem string `json:"ecosystem,omitempty"`
		} `json:"package"`
	}, 0, len(records))}
	for _, rec := range records {
		item := struct {
			Package struct {
				Name      string `json:"name"`
				Version   string `json:"version,omitempty"`
				Ecosystem string `json:"ecosystem,omitempty"`
			} `json:"package"`
		}{}
		item.Package.Name = rec.Name
		item.Package.Version = rec.Version
		item.Package.Ecosystem = "npm"
		result.Packages = append(result.Packages, item)
	}
	lockfile.Results = append(lockfile.Results, result)
	return lockfile, nil
}

func chunkOSVScannerRecords(records []osvScannerRecord, chunkSize int) [][]osvScannerRecord {
	if chunkSize <= 0 || len(records) == 0 || len(records) <= chunkSize {
		return [][]osvScannerRecord{records}
	}
	chunks := make([][]osvScannerRecord, 0, (len(records)+chunkSize-1)/chunkSize)
	for i := 0; i < len(records); i += chunkSize {
		end := i + chunkSize
		if end > len(records) {
			end = len(records)
		}
		chunks = append(chunks, records[i:end])
	}
	return chunks
}

func firstOSVScannerRecord(records []osvScannerRecord) osvScannerRecord {
	if len(records) == 0 {
		return osvScannerRecord{}
	}
	return records[0]
}

func lastOSVScannerRecord(records []osvScannerRecord) osvScannerRecord {
	if len(records) == 0 {
		return osvScannerRecord{}
	}
	return records[len(records)-1]
}

func packageLabel(rec osvScannerRecord) string {
	if rec.Name == "" && rec.Version == "" {
		return ""
	}
	return rec.Name + "@" + rec.Version
}

func countOSVScannerPackages(lockfile osvScannerCustomLockfile) int {
	total := 0
	for _, result := range lockfile.Results {
		total += len(result.Packages)
	}
	return total
}
