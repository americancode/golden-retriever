package npm

import (
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
				downloaded, err := fetchOne(ctx, client, pkg, opts.OutDir, state, &stateMu)
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

func fetchOne(ctx context.Context, client *Client, pkg Package, outDir string, state *State, stateMu *sync.Mutex) (bool, error) {
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
	if err := download(ctx, client.HTTPClient, pkg.Tarball, path, pkg); err != nil {
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

func download(ctx context.Context, httpClient *http.Client, tarball, path string, pkg Package) error {
	tmp := path + ".tmp"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tarball, nil)
	if err != nil {
		return err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("%s: tarball returned %s", pkg.Key(), res.Status)
	}
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, res.Body)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := verifyFile(tmp, pkg); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func verifyFile(path string, pkg Package) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if pkg.Integrity != "" {
		return verifyIntegrity(data, pkg.Integrity)
	}
	if pkg.Shasum != "" {
		sum := sha1.Sum(data)
		if hex.EncodeToString(sum[:]) != pkg.Shasum {
			return fmt.Errorf("%s: sha1 mismatch", pkg.Key())
		}
	}
	return nil
}

func verifyIntegrity(data []byte, integrity string) error {
	for _, field := range strings.Fields(integrity) {
		parts := strings.SplitN(field, "-", 2)
		if len(parts) != 2 {
			continue
		}
		if parts[0] != "sha512" {
			continue
		}
		sum := sha512.Sum512(data)
		got := base64.StdEncoding.EncodeToString(sum[:])
		if got == parts[1] {
			return nil
		}
		return fmt.Errorf("sha512 integrity mismatch")
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
