package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
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

func RebuildTargetFromRegistry(ctx context.Context, target *Client, state *State, opts SyncTargetOptions) (SyncTargetReport, error) {
	start := time.Now()
	if opts.Source == "" {
		opts.Source = "target-registry"
	}
	normalizeState(state)
	if opts.Progress != nil {
		opts.Progress("target-rebuild:start total=unknown source=%s", opts.Source)
	}
	packages, err := listRegistryPackages(ctx, target, opts)
	if err != nil {
		return SyncTargetReport{}, err
	}
	rebuilt := make(map[string]StateRecord, len(packages))
	now := time.Now().UTC()
	for i, pkg := range packages {
		rebuilt[pkg.Key()] = StateRecord{
			Name:      pkg.Name,
			Version:   pkg.Version,
			Tarball:   pkg.Tarball,
			Integrity: pkg.Integrity,
			Shasum:    pkg.Shasum,
			PresentAt: now,
			Source:    opts.Source,
		}
		if opts.Progress != nil && (i+1)%25 == 0 {
			opts.Progress("target-rebuild:progress processed=%d/%d present=%d failed=0", i+1, len(packages), len(rebuilt))
		}
	}
	state.Target = rebuilt
	state.UpdatedAt = now
	report := SyncTargetReport{
		Present: len(rebuilt),
		Elapsed: time.Since(start),
	}
	if opts.Progress != nil {
		opts.Progress("target-rebuild:done total=%d present=%d failed=0 elapsed=%s", report.Present, report.Present, report.Elapsed)
	}
	return report, nil
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

type gitLabRegistryPackage struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	PackageType string `json:"package_type"`
}

func listRegistryPackages(ctx context.Context, target *Client, opts SyncTargetOptions) ([]Package, error) {
	endpoint, err := gitLabPackagesAPIEndpoint(target.Registry)
	if err != nil {
		return nil, err
	}
	const perPage = 100
	page := 1
	seen := make(map[string]Package)
	for {
		reqURL, err := url.Parse(endpoint)
		if err != nil {
			return nil, err
		}
		q := reqURL.Query()
		q.Set("package_type", "npm")
		q.Set("per_page", strconv.Itoa(perPage))
		q.Set("page", strconv.Itoa(page))
		reqURL.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		target.applyAuth(req)
		if req.Header.Get("Authorization") == "" && target.Config != nil {
			if auth := target.Config.AuthFor(strings.TrimRight(target.Registry, "/") + "/"); auth.Header != "" {
				req.Header.Set("Authorization", auth.Header)
			}
		}
		res, err := target.HTTPClient.Do(req)
		if err != nil {
			return nil, err
		}
		body, readErr := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		_ = res.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		if res.StatusCode < 200 || res.StatusCode > 299 {
			return nil, fmt.Errorf("target package listing returned %w", httpStatusError{
				StatusCode: res.StatusCode,
				Status:     res.Status + ": " + strings.TrimSpace(string(body)),
				RetryAfter: retryAfterDelay(res.Header.Get("Retry-After")),
			})
		}
		var pagePackages []gitLabRegistryPackage
		if err := json.Unmarshal(body, &pagePackages); err != nil {
			return nil, err
		}
		if opts.Progress != nil {
			opts.Progress("target-rebuild:discover page=%d count=%d", page, len(pagePackages))
		}
		for _, pkg := range pagePackages {
			if pkg.PackageType != "" && pkg.PackageType != "npm" {
				continue
			}
			if strings.TrimSpace(pkg.Name) == "" || strings.TrimSpace(pkg.Version) == "" {
				continue
			}
			p := Package{Name: pkg.Name, Version: pkg.Version}
			seen[p.Key()] = p
		}
		nextPage := strings.TrimSpace(res.Header.Get("X-Next-Page"))
		if nextPage == "" {
			if len(pagePackages) < perPage {
				break
			}
			page++
			continue
		}
		page, err = strconv.Atoi(nextPage)
		if err != nil || page <= 0 {
			break
		}
	}
	packages := make([]Package, 0, len(seen))
	for _, pkg := range seen {
		packages = append(packages, pkg)
	}
	sort.Slice(packages, func(i, j int) bool {
		return packages[i].Key() < packages[j].Key()
	})
	return packages, nil
}

func gitLabPackagesAPIEndpoint(registry string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(registry))
	if err != nil {
		return "", err
	}
	path := strings.TrimRight(u.Path, "/")
	switch {
	case strings.Contains(path, "/api/v4/projects/") && strings.HasSuffix(path, "/packages/npm"):
		u.Path = strings.TrimSuffix(path, "/packages/npm") + "/packages"
		u.RawQuery = ""
		return u.String(), nil
	case strings.Contains(path, "/api/v4/groups/") && strings.HasSuffix(path, "/-/packages/npm"):
		u.Path = strings.TrimSuffix(path, "/-/packages/npm") + "/packages"
		u.RawQuery = ""
		return u.String(), nil
	default:
		return "", fmt.Errorf("registry-wide target inventory rebuild is only supported for GitLab npm registry endpoints; got %s", registry)
	}
}

func isHTTPStatus(err error, code int) bool {
	var statusErr httpStatusError
	if errors.As(err, &statusErr) {
		return statusErr.StatusCode == code
	}
	return false
}
