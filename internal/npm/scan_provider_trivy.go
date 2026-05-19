package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type trivyProvider struct{}

type trivyLockfile struct {
	Name            string                      `json:"name"`
	Version         string                      `json:"version"`
	LockfileVersion int                         `json:"lockfileVersion"`
	Requires        bool                        `json:"requires"`
	Packages        map[string]trivyLockPackage `json:"packages"`
	Dependencies    map[string]trivyLockPackage `json:"dependencies"`
}

type trivyLockPackage struct {
	Name         string            `json:"name,omitempty"`
	Version      string            `json:"version,omitempty"`
	Resolved     string            `json:"resolved,omitempty"`
	Integrity    string            `json:"integrity,omitempty"`
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

type trivyOutput struct {
	Results []struct {
		Target          string               `json:"Target"`
		Class           string               `json:"Class"`
		Type            string               `json:"Type"`
		Vulnerabilities []trivyVulnerability `json:"Vulnerabilities"`
	} `json:"Results"`
}

type trivyVulnerability struct {
	VulnerabilityID  string   `json:"VulnerabilityID"`
	PkgName          string   `json:"PkgName"`
	InstalledVersion string   `json:"InstalledVersion"`
	Severity         string   `json:"Severity"`
	Title            string   `json:"Title"`
	Description      string   `json:"Description"`
	PrimaryURL       string   `json:"PrimaryURL"`
	References       []string `json:"References"`
}

func (trivyProvider) Name() string {
	return "trivy"
}

func (trivyProvider) ApplyFindings(ctx context.Context, state *State, opts ScanOptions, keys []string) error {
	return applyTrivyFindings(ctx, state, opts, keys)
}

func applyTrivyFindings(ctx context.Context, state *State, opts ScanOptions, keys []string) error {
	if _, err := exec.LookPath("trivy"); err != nil {
		return fmt.Errorf("trivy not available: %w", err)
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
			opts.Progress("trivy:skip reason=no-packages")
		}
		return nil
	}
	if opts.TrivyConcurrency > 1 && opts.TrivyChunkSize > 0 && len(records) > opts.TrivyChunkSize {
		if err := applyTrivyFindingsParallel(ctx, state, opts, records, minLevel, unknownLevel, exceptions); err != nil {
			return err
		}
	} else {
		parsed, err := runTrivy(ctx, opts, records, opts.TrivySkipDBUpdate, "")
		if err != nil {
			return err
		}
		applyTrivyOutput(state, opts, parsed, minLevel, unknownLevel, exceptions)
	}
	state.UpdatedAt = time.Now().UTC()
	if opts.Progress != nil {
		opts.Progress("trivy:done packages=%d provider=trivy", len(records))
	}
	return nil
}

func applyTrivyFindingsParallel(ctx context.Context, state *State, opts ScanOptions, records []osvScannerRecord, minLevel, unknownLevel severityLevel, exceptions []ScanException) error {
	chunks := chunkOSVScannerRecords(records, opts.TrivyChunkSize)
	if opts.Progress != nil {
		opts.Progress("trivy:parallel-start chunks=%d chunk_size=%d concurrency=%d packages=%d", len(chunks), opts.TrivyChunkSize, opts.TrivyConcurrency, len(records))
	}
	startIndex := 0
	if !opts.TrivySkipDBUpdate && len(chunks) > 0 {
		if opts.Progress != nil {
			opts.Progress("trivy:db-warmup packages=%d", len(chunks[0]))
		}
		parsed, err := runTrivy(ctx, opts, chunks[0], false, "warmup")
		if err != nil {
			return err
		}
		applyTrivyOutput(state, opts, parsed, minLevel, unknownLevel, exceptions)
		startIndex = 1
	}
	if startIndex >= len(chunks) {
		if opts.Progress != nil {
			opts.Progress("trivy:parallel-done chunks=%d completed=%d", len(chunks), len(chunks))
		}
		return nil
	}
	type chunkResult struct {
		index  int
		output trivyOutput
		err    error
	}
	jobs := make(chan int)
	results := make(chan chunkResult, len(chunks)-startIndex)
	workerCount := opts.TrivyConcurrency
	if workerCount > len(chunks)-startIndex {
		workerCount = len(chunks) - startIndex
	}
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				chunk := chunks[idx]
				label := fmt.Sprintf("%d/%d", idx+1, len(chunks))
				if opts.Progress != nil {
					opts.Progress("trivy:chunk:start chunk=%s packages=%d", label, len(chunk))
				}
				output, err := runTrivy(ctx, opts, chunk, true, label)
				results <- chunkResult{index: idx, output: output, err: err}
			}
		}()
	}
	for idx := startIndex; idx < len(chunks); idx++ {
		jobs <- idx
	}
	close(jobs)
	go func() {
		wg.Wait()
		close(results)
	}()
	completed := startIndex
	var firstErr error
	for result := range results {
		completed++
		label := fmt.Sprintf("%d/%d", result.index+1, len(chunks))
		if result.err != nil {
			if firstErr == nil {
				firstErr = result.err
			}
			if opts.Progress != nil {
				opts.Progress("trivy:chunk:fail chunk=%s error=%v", label, result.err)
			}
			continue
		}
		applyTrivyOutput(state, opts, result.output, minLevel, unknownLevel, exceptions)
		if opts.Progress != nil {
			opts.Progress("trivy:chunk:done chunk=%s completed=%d/%d", label, completed, len(chunks))
		}
	}
	if opts.Progress != nil {
		if firstErr != nil {
			opts.Progress("trivy:parallel-fail chunks=%d completed=%d error=%v", len(chunks), completed, firstErr)
		} else {
			opts.Progress("trivy:parallel-done chunks=%d completed=%d", len(chunks), completed)
		}
	}
	return firstErr
}

func runTrivy(ctx context.Context, opts ScanOptions, records []osvScannerRecord, skipDBUpdate bool, chunkLabel string) (trivyOutput, error) {
	tmpDir, err := os.MkdirTemp("", "golden-retriever-trivy-*")
	if err != nil {
		return trivyOutput{}, err
	}
	defer os.RemoveAll(tmpDir)

	lockfile, err := buildTrivyPackageLock(records)
	if err != nil {
		return trivyOutput{}, err
	}
	data, err := json.MarshalIndent(lockfile, "", "  ")
	if err != nil {
		return trivyOutput{}, err
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "package-lock.json"), data, 0o644); err != nil {
		return trivyOutput{}, err
	}

	args := []string{"fs", "--format", "json", "--scanners", "vuln", "--pkg-types", "library", "--quiet", "--exit-code", "0", "--skip-version-check"}
	if opts.TrivyOfflineScan {
		args = append(args, "--offline-scan")
	}
	if skipDBUpdate {
		args = append(args, "--skip-db-update")
	}
	args = append(args, ".")
	if opts.Progress != nil {
		if chunkLabel == "" {
			opts.Progress("trivy:start packages=%d offline_scan=%t skip_db_update=%t", len(records), opts.TrivyOfflineScan, skipDBUpdate)
		} else {
			opts.Progress("trivy:start chunk=%s packages=%d offline_scan=%t skip_db_update=%t", chunkLabel, len(records), opts.TrivyOfflineScan, skipDBUpdate)
		}
	}
	cmd := exec.CommandContext(ctx, "trivy", args...)
	cmd.Dir = tmpDir
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := runTrivyCommand(ctx, cmd, opts, len(records), chunkLabel)
	if runErr != nil {
		return trivyOutput{}, fmt.Errorf("trivy failed: %w: %s", runErr, strings.TrimSpace(stderr.String()))
	}
	var parsed trivyOutput
	if err := json.Unmarshal([]byte(stdout.String()), &parsed); err != nil {
		return trivyOutput{}, fmt.Errorf("parse trivy output: %w", err)
	}
	return parsed, nil
}

func buildTrivyPackageLock(records []osvScannerRecord) (trivyLockfile, error) {
	lockfile := trivyLockfile{
		Name:            "golden-retriever-scan",
		Version:         "0.0.0",
		LockfileVersion: 3,
		Requires:        true,
		Packages: map[string]trivyLockPackage{
			"": {Name: "golden-retriever-scan", Version: "0.0.0", Dependencies: map[string]string{}},
		},
		Dependencies: map[string]trivyLockPackage{},
	}
	for _, rec := range records {
		if rec.Name == "" || rec.Version == "" {
			continue
		}
		nodePath := "node_modules/" + rec.Name
		lockfile.Packages[nodePath] = trivyLockPackage{Name: rec.Name, Version: rec.Version}
		lockfile.Dependencies[rec.Name] = trivyLockPackage{Name: rec.Name, Version: rec.Version}
		root := lockfile.Packages[""]
		root.Dependencies[rec.Name] = rec.Version
		lockfile.Packages[""] = root
	}
	return lockfile, nil
}

func applyTrivyOutput(state *State, opts ScanOptions, parsed trivyOutput, minLevel, unknownLevel severityLevel, exceptions []ScanException) {
	for _, result := range parsed.Results {
		for _, vuln := range result.Vulnerabilities {
			id := strings.TrimSpace(vuln.VulnerabilityID)
			name := strings.TrimSpace(vuln.PkgName)
			version := strings.TrimSpace(vuln.InstalledVersion)
			if id == "" || name == "" || version == "" {
				continue
			}
			key := name + "@" + version
			rec, bucket := getStateRecord(state, key, opts.Source)
			if rec.Name == "" || rec.Version == "" {
				continue
			}
			if isExceptionMatch(exceptions, rec, id) {
				continue
			}
			level := parseScannerSeverity(vuln.Severity, unknownLevel)
			if level < minLevel {
				continue
			}
			rec.ScanStatus = "fail"
			rec.ScanReason = fmt.Sprintf("trivy vulnerabilities (%s+): %s", opts.MinSeverity, id)
			rec.ScanVulnURLs = appendUniqueStrings(rec.ScanVulnURLs, trivyVulnURLs(vuln)...)
			rec.ScanVulnDescriptions = appendVulnDescription(rec.ScanVulnDescriptions, id, firstNonEmptyString(vuln.Title, vuln.Description))
			rec.ScannedAt = time.Now().UTC()
			setStateRecord(state, key, bucket, rec)
			if opts.Progress != nil {
				opts.Progress("scan:vuln package=%s@%s severity=%s ids=%s urls=%s descriptions=%s provider=trivy", rec.Name, rec.Version, level.String(), id, strings.Join(rec.ScanVulnURLs, ","), strings.Join(rec.ScanVulnDescriptions, " | "))
			}
		}
	}
}

func trivyVulnURLs(vuln trivyVulnerability) []string {
	urls := []string{}
	if strings.TrimSpace(vuln.PrimaryURL) != "" {
		urls = append(urls, strings.TrimSpace(vuln.PrimaryURL))
	}
	for _, ref := range vuln.References {
		if strings.TrimSpace(ref) != "" {
			urls = append(urls, strings.TrimSpace(ref))
		}
	}
	if len(urls) == 0 && strings.TrimSpace(vuln.VulnerabilityID) != "" {
		urls = vulnURLs([]string{vuln.VulnerabilityID})
	}
	return urls
}

func runTrivyCommand(ctx context.Context, cmd *exec.Cmd, opts ScanOptions, packageCount int, chunkLabel string) error {
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
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	start := time.Now()
	for {
		select {
		case err := <-done:
			return err
		case <-ticker.C:
			if chunkLabel == "" {
				opts.Progress("trivy:progress elapsed=%s packages=%d", time.Since(start).Round(time.Second), packageCount)
			} else {
				opts.Progress("trivy:progress chunk=%s elapsed=%s packages=%d", chunkLabel, time.Since(start).Round(time.Second), packageCount)
			}
		case <-ctx.Done():
			<-done
			return ctx.Err()
		}
	}
}
