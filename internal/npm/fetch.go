package npm

import (
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type FetchOptions struct {
	OutDir             string
	StatePath          string
	Concurrency        int
	MaxRetries         int
	OutputNameStrategy string
	Progress           func(format string, args ...any)
}

type FetchReport struct {
	Downloaded      int
	Skipped         int
	TargetSkipped   int
	Failed          int
	DownloadedBytes int64
	Elapsed         time.Duration
}

type State struct {
	SchemaVersion int                      `json:"schemaVersion"`
	UpdatedAt     time.Time                `json:"updatedAt"`
	Target        map[string]StateRecord   `json:"target"`
	Local         map[string]StateRecord   `json:"local"`
	Failures      map[string]FailureRecord `json:"failures,omitempty"`
	Downloaded    map[string]StateRecord   `json:"downloaded,omitempty"`
}

type FailureRecord struct {
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	Attempts  int       `json:"attempts"`
	LastError string    `json:"lastError"`
	FailedAt  time.Time `json:"failedAt"`
	Source    string    `json:"source,omitempty"`
}

type StateRecord struct {
	Name         string    `json:"name"`
	Version      string    `json:"version"`
	Tarball      string    `json:"tarball"`
	Integrity    string    `json:"integrity,omitempty"`
	Shasum       string    `json:"shasum,omitempty"`
	Path         string    `json:"path,omitempty"`
	Size         int64     `json:"size,omitempty"`
	DownloadedAt time.Time `json:"downloadedAt,omitempty"`
	PresentAt    time.Time `json:"presentAt,omitempty"`
	Source       string    `json:"source,omitempty"`
	ScanStatus   string    `json:"scanStatus,omitempty"`
	ScanReason   string    `json:"scanReason,omitempty"`
	ScannedAt    time.Time `json:"scannedAt,omitempty"`
}

func FetchAll(ctx context.Context, client *Client, packages []Package, opts FetchOptions) (FetchReport, error) {
	start := time.Now()
	if opts.Concurrency <= 0 {
		opts.Concurrency = 16
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = 0
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return FetchReport{}, err
	}
	state, err := loadState(opts.StatePath)
	if err != nil {
		return FetchReport{}, err
	}
	ValidateStateFiles(state)
	if opts.Progress != nil {
		opts.Progress("fetch:start total=%d concurrency=%d out=%s state=%s", len(packages), opts.Concurrency, opts.OutDir, opts.StatePath)
	}

	jobs := make(chan Package)
	var mu sync.Mutex
	var stateMu sync.Mutex
	var report FetchReport
	var processed int
	var firstErr error
	var wg sync.WaitGroup

	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pkg := range jobs {
				result, bytes, err := fetchOne(ctx, client, pkg, opts.OutDir, opts.OutputNameStrategy, state, &stateMu, opts.MaxRetries)
				mu.Lock()
				processed++
				if err != nil {
					report.Failed++
					if firstErr == nil {
						firstErr = err
					}
					if opts.Progress != nil {
						opts.Progress("fetch:fail processed=%d/%d package=%s error=%v", processed, len(packages), pkg.Key(), err)
					}
				} else if result == fetchDownloaded {
					report.Downloaded++
					report.DownloadedBytes += bytes
				} else if result == fetchTargetPresent {
					report.TargetSkipped++
				} else if result == fetchLocalPresent {
					report.Skipped++
				}
				if opts.Progress != nil && processed%25 == 0 {
					opts.Progress("fetch:progress processed=%d/%d downloaded=%d local_skipped=%d target_skipped=%d failed=%d",
						processed, len(packages), report.Downloaded, report.Skipped, report.TargetSkipped, report.Failed)
				}
				mu.Unlock()
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

	report.Elapsed = time.Since(start)
	if err := saveState(opts.StatePath, state); err != nil {
		return report, err
	}
	if opts.Progress != nil {
		opts.Progress("fetch:done total=%d downloaded=%d local_skipped=%d target_skipped=%d failed=%d elapsed=%s",
			len(packages), report.Downloaded, report.Skipped, report.TargetSkipped, report.Failed, report.Elapsed)
	}
	return report, firstErr
}

type fetchResult int

const (
	fetchDownloaded fetchResult = iota
	fetchLocalPresent
	fetchTargetPresent
)

func fetchOne(ctx context.Context, client *Client, pkg Package, outDir, outputNameStrategy string, state *State, stateMu *sync.Mutex, maxRetries int) (fetchResult, int64, error) {
	if pkg.Tarball == "" {
		recordFailure(state, stateMu, pkg, "fetch", fmt.Errorf("%s missing tarball URL", pkg.Key()))
		return fetchLocalPresent, 0, fmt.Errorf("%s missing tarball URL", pkg.Key())
	}
	path, err := tarballOutputPath(outDir, pkg, outputNameStrategy)
	if err != nil {
		recordFailure(state, stateMu, pkg, "fetch", err)
		return fetchLocalPresent, 0, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		recordFailure(state, stateMu, pkg, "fetch", err)
		return fetchLocalPresent, 0, err
	}
	stateMu.Lock()
	if targetContainsPackage(state, pkg) {
		stateMu.Unlock()
		return fetchTargetPresent, 0, nil
	}
	if rec, ok := state.Local[pkg.Key()]; ok && rec.Tarball == pkg.Tarball && rec.Path == path {
		stateMu.Unlock()
		if verifyFile(path, pkg) == nil {
			return fetchLocalPresent, 0, nil
		}
	} else {
		stateMu.Unlock()
	}
	if verifyFile(path, pkg) == nil {
		info, err := os.Stat(path)
		if err != nil {
			return fetchLocalPresent, 0, err
		}
		stateMu.Lock()
		state.Local[pkg.Key()] = StateRecord{
			Name: pkg.Name, Version: pkg.Version, Tarball: pkg.Tarball,
			Integrity: pkg.Integrity, Shasum: pkg.Shasum, Path: path,
			Size: info.Size(), DownloadedAt: time.Now().UTC(),
		}
		delete(state.Failures, pkg.Key())
		state.UpdatedAt = time.Now().UTC()
		stateMu.Unlock()
		return fetchLocalPresent, 0, nil
	}
	if err := downloadWithRetries(ctx, client, pkg.Tarball, path, pkg, maxRetries); err != nil {
		recordFailure(state, stateMu, pkg, "fetch", err)
		return fetchLocalPresent, 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fetchLocalPresent, 0, err
	}
	stateMu.Lock()
	state.Local[pkg.Key()] = StateRecord{
		Name: pkg.Name, Version: pkg.Version, Tarball: pkg.Tarball,
		Integrity: pkg.Integrity, Shasum: pkg.Shasum, Path: path,
		Size: info.Size(), DownloadedAt: time.Now().UTC(),
	}
	delete(state.Failures, pkg.Key())
	state.UpdatedAt = time.Now().UTC()
	stateMu.Unlock()
	return fetchDownloaded, info.Size(), nil
}

func recordFailure(state *State, stateMu *sync.Mutex, pkg Package, source string, err error) {
	stateMu.Lock()
	defer stateMu.Unlock()
	normalizeState(state)
	rec := state.Failures[pkg.Key()]
	rec.Name = pkg.Name
	rec.Version = pkg.Version
	rec.Attempts++
	rec.LastError = err.Error()
	rec.FailedAt = time.Now().UTC()
	rec.Source = source
	state.Failures[pkg.Key()] = rec
	state.UpdatedAt = time.Now().UTC()
}

func targetContainsPackage(state *State, pkg Package) bool {
	rec, ok := state.Target[pkg.Key()]
	if !ok {
		return false
	}
	if rec.Name != pkg.Name || rec.Version != pkg.Version {
		return false
	}
	if rec.Integrity != "" && pkg.Integrity != "" && rec.Integrity != pkg.Integrity {
		return false
	}
	if rec.Shasum != "" && pkg.Shasum != "" && rec.Shasum != pkg.Shasum {
		return false
	}
	return true
}

func downloadWithRetries(ctx context.Context, client *Client, tarball, path string, pkg Package, maxRetries int) error {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		err := download(ctx, client, tarball, path, pkg)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryable(err) || attempt == maxRetries {
			break
		}
		backoff := retryDelay(lastErr, attempt)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	return lastErr
}

type httpStatusError struct {
	StatusCode int
	Status     string
	RetryAfter time.Duration
}

func (e httpStatusError) Error() string {
	return e.Status
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var statusErr httpStatusError
	if ok := errors.As(err, &statusErr); ok {
		return statusErr.StatusCode == http.StatusTooManyRequests || statusErr.StatusCode >= 500
	}
	return true
}

func retryDelay(err error, attempt int) time.Duration {
	var statusErr httpStatusError
	if errors.As(err, &statusErr) && statusErr.RetryAfter > 0 {
		return statusErr.RetryAfter
	}
	return time.Duration(100*(1<<attempt)) * time.Millisecond
}

func retryAfterDelay(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		delay := time.Until(when)
		if delay > 0 {
			return delay
		}
	}
	return 0
}

func download(ctx context.Context, client *Client, tarball, path string, pkg Package) error {
	tmp := path + ".tmp"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tarball, nil)
	if err != nil {
		return err
	}
	client.applyAuth(req)
	res, err := client.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("%s: tarball returned %w", pkg.Key(), httpStatusError{
			StatusCode: res.StatusCode,
			Status:     res.Status,
			RetryAfter: retryAfterDelay(res.Header.Get("Retry-After")),
		})
	}
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	sha512Hash := sha512.New()
	sha1Hash := sha1.New()
	writer := io.MultiWriter(f, sha512Hash, sha1Hash)
	_, copyErr := io.Copy(writer, res.Body)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := verifyHashes(sha512Hash.Sum(nil), sha1Hash.Sum(nil), pkg); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

func verifyFile(path string, pkg Package) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sha512Hash := sha512.New()
	sha1Hash := sha1.New()
	if _, err := io.Copy(io.MultiWriter(sha512Hash, sha1Hash), f); err != nil {
		return err
	}
	return verifyHashes(sha512Hash.Sum(nil), sha1Hash.Sum(nil), pkg)
}

func verifyHashes(sha512Sum, sha1Sum []byte, pkg Package) error {
	if pkg.Integrity != "" {
		return verifyIntegrity(sha512Sum, sha1Sum, pkg.Integrity)
	}
	if pkg.Shasum != "" {
		if hex.EncodeToString(sha1Sum) != pkg.Shasum {
			return fmt.Errorf("%s: sha1 mismatch", pkg.Key())
		}
	}
	return nil
}

func verifyIntegrity(sha512Sum, sha1Sum []byte, integrity string) error {
	for _, field := range strings.Fields(integrity) {
		parts := strings.SplitN(field, "-", 2)
		if len(parts) != 2 {
			continue
		}
		var got string
		switch parts[0] {
		case "sha512":
			got = base64.StdEncoding.EncodeToString(sha512Sum)
		case "sha1":
			got = base64.StdEncoding.EncodeToString(sha1Sum)
		default:
			continue
		}
		if got == parts[1] {
			return nil
		}
		return fmt.Errorf("%s integrity mismatch", parts[0])
	}
	return nil
}

func LoadState(path string) (*State, error) {
	state := NewState()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, state); err != nil {
		return nil, err
	}
	normalizeState(state)
	return state, nil
}

func NewState() *State {
	return &State{
		SchemaVersion: 1,
		Target:        map[string]StateRecord{},
		Local:         map[string]StateRecord{},
		Failures:      map[string]FailureRecord{},
	}
}

func normalizeState(state *State) {
	if state.SchemaVersion == 0 {
		state.SchemaVersion = 1
	}
	if state.Target == nil {
		state.Target = map[string]StateRecord{}
	}
	if state.Local == nil {
		state.Local = map[string]StateRecord{}
	}
	if state.Failures == nil {
		state.Failures = map[string]FailureRecord{}
	}
	if len(state.Local) == 0 && len(state.Downloaded) > 0 {
		for key, rec := range state.Downloaded {
			state.Local[key] = rec
		}
		state.Downloaded = nil
	}
}

type StateValidationReport struct {
	CheckedLocal int
	ValidLocal   int
	RemovedLocal int
}

type StateSummary struct {
	SchemaVersion int       `json:"schemaVersion"`
	UpdatedAt     time.Time `json:"updatedAt"`
	Target        int       `json:"target"`
	Local         int       `json:"local"`
	Failures      int       `json:"failures"`
}

func SummarizeState(state *State) StateSummary {
	normalizeState(state)
	return StateSummary{
		SchemaVersion: state.SchemaVersion,
		UpdatedAt:     state.UpdatedAt,
		Target:        len(state.Target),
		Local:         len(state.Local),
		Failures:      len(state.Failures),
	}
}

func ValidateStateFiles(state *State) StateValidationReport {
	normalizeState(state)
	var report StateValidationReport
	for key, rec := range state.Local {
		report.CheckedLocal++
		if rec.Path == "" {
			delete(state.Local, key)
			report.RemovedLocal++
			continue
		}
		pkg := Package{Name: rec.Name, Version: rec.Version, Tarball: rec.Tarball, Integrity: rec.Integrity, Shasum: rec.Shasum}
		if verifyFile(rec.Path, pkg) != nil {
			delete(state.Local, key)
			report.RemovedLocal++
			continue
		}
		report.ValidLocal++
	}
	if report.RemovedLocal > 0 {
		state.UpdatedAt = time.Now().UTC()
	}
	return report
}

func SaveState(path string, state *State) error {
	normalizeState(state)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func loadState(path string) (*State, error) {
	return LoadState(path)
}

func saveState(path string, state *State) error {
	return SaveState(path, state)
}

func MarkTargetPresent(state *State, pkg Package, source string) {
	normalizeState(state)
	state.Target[pkg.Key()] = StateRecord{
		Name: pkg.Name, Version: pkg.Version, Tarball: pkg.Tarball,
		Integrity: pkg.Integrity, Shasum: pkg.Shasum,
		PresentAt: time.Now().UTC(), Source: source,
	}
	delete(state.Failures, pkg.Key())
	state.UpdatedAt = time.Now().UTC()
}

func tarballFileName(pkg Package) string {
	name := strings.ReplaceAll(pkg.Name, "/", "+")
	return name + "-" + pkg.Version + ".tgz"
}

func tarballOutputPath(outDir string, pkg Package, strategy string) (string, error) {
	switch strategy {
	case "", "flat", "escaped":
		return filepath.Join(outDir, tarballFileName(pkg)), nil
	case "registry":
		name := pkg.Name
		baseName := name
		if strings.HasPrefix(name, "@") {
			if _, after, ok := strings.Cut(name, "/"); ok {
				baseName = after
			}
		}
		return filepath.Join(outDir, name, "-", baseName+"-"+pkg.Version+".tgz"), nil
	default:
		return "", fmt.Errorf("unsupported output naming strategy %q", strategy)
	}
}
