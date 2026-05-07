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
	"strings"
	"sync"
	"time"
)

type FetchOptions struct {
	OutDir      string
	StatePath   string
	Concurrency int
	MaxRetries  int
}

type FetchReport struct {
	Downloaded int
	Skipped    int
	Failed     int
}

type State struct {
	UpdatedAt  time.Time              `json:"updatedAt"`
	Downloaded map[string]StateRecord `json:"downloaded"`
}

type StateRecord struct {
	Name         string    `json:"name"`
	Version      string    `json:"version"`
	Tarball      string    `json:"tarball"`
	Integrity    string    `json:"integrity,omitempty"`
	Shasum       string    `json:"shasum,omitempty"`
	Path         string    `json:"path"`
	Size         int64     `json:"size"`
	DownloadedAt time.Time `json:"downloadedAt"`
}

func FetchAll(ctx context.Context, client *Client, packages []Package, opts FetchOptions) (FetchReport, error) {
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

	jobs := make(chan Package)
	var mu sync.Mutex
	var stateMu sync.Mutex
	var report FetchReport
	var firstErr error
	var wg sync.WaitGroup

	for i := 0; i < opts.Concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pkg := range jobs {
				downloaded, err := fetchOne(ctx, client, pkg, opts.OutDir, state, &stateMu, opts.MaxRetries)
				mu.Lock()
				if err != nil {
					report.Failed++
					if firstErr == nil {
						firstErr = err
					}
				} else if downloaded {
					report.Downloaded++
				} else {
					report.Skipped++
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
			return report, ctx.Err()
		case jobs <- pkg:
		}
	}
	close(jobs)
	wg.Wait()

	if err := saveState(opts.StatePath, state); err != nil {
		return report, err
	}
	return report, firstErr
}

func fetchOne(ctx context.Context, client *Client, pkg Package, outDir string, state *State, stateMu *sync.Mutex, maxRetries int) (bool, error) {
	if pkg.Tarball == "" {
		return false, fmt.Errorf("%s missing tarball URL", pkg.Key())
	}
	path := filepath.Join(outDir, tarballFileName(pkg))
	stateMu.Lock()
	if rec, ok := state.Downloaded[pkg.Key()]; ok && rec.Tarball == pkg.Tarball && rec.Path == path {
		stateMu.Unlock()
		if verifyFile(path, pkg) == nil {
			return false, nil
		}
	} else {
		stateMu.Unlock()
	}
	if err := downloadWithRetries(ctx, client, pkg.Tarball, path, pkg, maxRetries); err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	stateMu.Lock()
	state.Downloaded[pkg.Key()] = StateRecord{
		Name: pkg.Name, Version: pkg.Version, Tarball: pkg.Tarball,
		Integrity: pkg.Integrity, Shasum: pkg.Shasum, Path: path,
		Size: info.Size(), DownloadedAt: time.Now().UTC(),
	}
	state.UpdatedAt = time.Now().UTC()
	stateMu.Unlock()
	return true, nil
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
		backoff := time.Duration(100*(1<<attempt)) * time.Millisecond
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
		return fmt.Errorf("%s: tarball returned %w", pkg.Key(), httpStatusError{StatusCode: res.StatusCode, Status: res.Status})
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

func loadState(path string) (*State, error) {
	state := &State{Downloaded: map[string]StateRecord{}}
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
	if state.Downloaded == nil {
		state.Downloaded = map[string]StateRecord{}
	}
	return state, nil
}

func saveState(path string, state *State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func tarballFileName(pkg Package) string {
	name := strings.ReplaceAll(pkg.Name, "/", "+")
	return name + "-" + pkg.Version + ".tgz"
}
