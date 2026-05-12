package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type ScanOptions struct {
	StatePath         string
	Concurrency       int
	Source            string
	BlocklistPath     string
	DenyPackagePrefix []string
	DenyScriptKeys    []string
	UseOSV            bool
	OSVEndpoint       string
	OSVBatchSize      int
	MinSeverity       string
	UnknownSeverity   string
	ExceptionsPath    string
	OSVConcurrency    int
}

type ScanReport struct {
	Total    int           `json:"total"`
	Passed   int           `json:"passed"`
	Failed   int           `json:"failed"`
	Errors   int           `json:"errors"`
	Findings []ScanFinding `json:"findings,omitempty"`
	Elapsed  time.Duration `json:"elapsed"`
}

type ScanFinding struct {
	Package   string    `json:"package"`
	Status    string    `json:"status"`
	Reason    string    `json:"reason"`
	ScannedAt time.Time `json:"scannedAt,omitempty"`
}

type ScanExceptionFile struct {
	Exceptions []ScanException `json:"exceptions"`
}

type ScanBlocklistFile struct {
	Packages        []string `json:"packages"`
	PackageVersions []string `json:"packageVersions"`
	PackagePrefixes []string `json:"packagePrefixes"`
	ScriptKeys      []string `json:"scriptKeys"`
}

type ScanException struct {
	Package   string `json:"package,omitempty"` // name or name@version
	VulnID    string `json:"vulnId,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

func ScanState(ctx context.Context, opts ScanOptions) (ScanReport, error) {
	start := time.Now()
	if opts.Concurrency <= 0 {
		opts.Concurrency = 8
	}
	if len(opts.DenyScriptKeys) == 0 {
		opts.DenyScriptKeys = []string{"preinstall", "install", "postinstall"}
	}
	if opts.OSVEndpoint == "" {
		opts.OSVEndpoint = "https://api.osv.dev/v1/querybatch"
	}
	if opts.OSVBatchSize <= 0 {
		opts.OSVBatchSize = 200
	}
	if opts.Source == "" {
		opts.Source = "local"
	}
	if opts.OSVConcurrency <= 0 {
		opts.OSVConcurrency = maxInt(4, opts.Concurrency/2)
	}
	if opts.MinSeverity == "" {
		opts.MinSeverity = "high"
	}
	if opts.UnknownSeverity == "" {
		opts.UnknownSeverity = "high"
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
	if len(blocklist.ScriptKeys) > 0 {
		opts.DenyScriptKeys = blocklist.ScriptKeys
	}

	jobs := make(chan string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	keys := selectedScanKeys(state, opts.Source)
	report := ScanReport{Total: len(keys)}
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
				rec, bucket := getStateRecord(state, key, opts.Source)
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
		if err := applyOSVFindings(ctx, state, opts, keys); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
		report = recomputeScanReport(state, keys, opts.Source)
	}
	if err := saveState(opts.StatePath, state); err != nil {
		return report, err
	}
	report.Elapsed = time.Since(start)
	return report, firstErr
}

func recomputeScanReport(state *State, keys []string, source string) ScanReport {
	report := ScanReport{Total: len(keys)}
	for _, key := range keys {
		rec, _ := getStateRecord(state, key, source)
		switch rec.ScanStatus {
		case "pass":
			report.Passed++
		case "fail":
			report.Failed++
			report.Findings = append(report.Findings, ScanFinding{
				Package:   rec.Name + "@" + rec.Version,
				Status:    "fail",
				Reason:    rec.ScanReason,
				ScannedAt: rec.ScannedAt,
			})
		default:
			report.Errors++
		}
	}
	return report
}

func scanRecord(rec StateRecord, opts ScanOptions, blocklist ScanBlocklistFile, requireTarball bool) (string, string, error) {
	name := rec.Name
	if requireTarball {
		if rec.Path == "" {
			return "fail", "missing local tarball path", nil
		}
		data, err := os.ReadFile(rec.Path)
		if err != nil {
			return "fail", "", err
		}
		manifest, err := extractRootManifest(data)
		if err != nil {
			return "fail", "", err
		}
		manifestName, _ := manifest["name"].(string)
		if manifestName != "" {
			name = manifestName
		}
		if scripts, ok := manifest["scripts"].(map[string]any); ok {
			for _, key := range opts.DenyScriptKeys {
				if _, exists := scripts[key]; exists {
					return "fail", fmt.Sprintf("denied lifecycle script: %s", key), nil
				}
			}
		}
	}
	for _, denied := range blocklist.Packages {
		if denied != "" && name == denied {
			return "fail", fmt.Sprintf("package blocked by deny list: %s", denied), nil
		}
	}
	for _, denied := range blocklist.PackageVersions {
		if denied != "" && (name+"@"+rec.Version) == denied {
			return "fail", fmt.Sprintf("package version blocked by deny list: %s", denied), nil
		}
	}
	for _, pref := range opts.DenyPackagePrefix {
		if pref != "" && strings.HasPrefix(name, pref) {
			return "fail", fmt.Sprintf("package name denied by prefix %q", pref), nil
		}
	}
	return "pass", "policy checks passed", nil
}

func loadBlocklist(path string) (ScanBlocklistFile, error) {
	if strings.TrimSpace(path) == "" {
		return ScanBlocklistFile{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ScanBlocklistFile{}, nil
		}
		return ScanBlocklistFile{}, err
	}
	var file ScanBlocklistFile
	if err := json.Unmarshal(data, &file); err != nil {
		return ScanBlocklistFile{}, err
	}
	return file, nil
}

func extractRootManifest(tarball []byte) (map[string]any, error) {
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	bestScore := -1
	var best map[string]any
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		clean := path.Clean(h.Name)
		if filepath.Base(clean) != "package.json" {
			continue
		}
		raw, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		doc := map[string]any{}
		if err := json.Unmarshal(raw, &doc); err != nil {
			continue
		}
		if _, ok := doc["name"].(string); !ok {
			continue
		}
		if _, ok := doc["version"].(string); !ok {
			continue
		}
		score := 0
		if clean == "package/package.json" {
			score = 10_000
		} else {
			score = 1_000 - strings.Count(clean, "/")
		}
		if score > bestScore {
			bestScore = score
			best = doc
		}
	}
	if best == nil {
		return nil, fmt.Errorf("package.json not found in tarball")
	}
	return best, nil
}

type osvBatchRequest struct {
	Queries []osvQuery `json:"queries"`
}

type osvQuery struct {
	Package osvPackage `json:"package"`
	Version string     `json:"version,omitempty"`
}

type osvPackage struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem"`
}

type osvBatchResponse struct {
	Results []struct {
		Vulns []struct {
			ID string `json:"id"`
		} `json:"vulns"`
	} `json:"results"`
}

func applyOSVFindings(ctx context.Context, state *State, opts ScanOptions, keys []string) error {
	type indexedRec struct {
		Key string
		Rec StateRecord
	}
	records := make([]indexedRec, 0, len(keys))
	for _, key := range keys {
		rec, _ := getStateRecord(state, key, opts.Source)
		if rec.Name == "" || rec.Version == "" {
			continue
		}
		records = append(records, indexedRec{Key: key, Rec: rec})
	}
	client := &http.Client{Timeout: 30 * time.Second}
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
	vulnCache := map[string]severityLevel{}
	for i := 0; i < len(records); i += opts.OSVBatchSize {
		end := i + opts.OSVBatchSize
		if end > len(records) {
			end = len(records)
		}
		chunk := records[i:end]
		reqBody := osvBatchRequest{Queries: make([]osvQuery, 0, len(chunk))}
		for _, item := range chunk {
			reqBody.Queries = append(reqBody.Queries, osvQuery{
				Package: osvPackage{Name: item.Rec.Name, Ecosystem: "npm"},
				Version: item.Rec.Version,
			})
		}
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.OSVEndpoint, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return fmt.Errorf("osv query failed: %s", resp.Status)
		}
		var parsed osvBatchResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return err
		}
		if len(parsed.Results) != len(chunk) {
			return fmt.Errorf("osv result mismatch: got=%d want=%d", len(parsed.Results), len(chunk))
		}
		idsToResolve := make(map[string]struct{})
		for _, result := range parsed.Results {
			for _, v := range result.Vulns {
				if v.ID != "" {
					idsToResolve[v.ID] = struct{}{}
				}
			}
		}
		levels, err := fetchOSVSeverityLevels(ctx, client, opts, idsToResolve, unknownLevel, vulnCache)
		if err != nil {
			return err
		}
		for k, v := range levels {
			vulnCache[k] = v
		}
		for idx, result := range parsed.Results {
			rec, bucket := getStateRecord(state, chunk[idx].Key, opts.Source)
			if len(result.Vulns) == 0 {
				continue
			}
			hitIDs := make([]string, 0, len(result.Vulns))
			block := false
			for _, v := range result.Vulns {
				if v.ID == "" {
					continue
				}
				if isExceptionMatch(exceptions, rec, v.ID) {
					continue
				}
				hitIDs = append(hitIDs, v.ID)
				if sev, ok := levels[v.ID]; ok && sev >= minLevel {
					block = true
				}
			}
			if len(hitIDs) == 0 {
				continue
			}
			if block {
				rec.ScanStatus = "fail"
				rec.ScanReason = fmt.Sprintf("osv vulnerabilities (%s+): %s", opts.MinSeverity, strings.Join(hitIDs, ","))
				rec.ScannedAt = time.Now().UTC()
				setStateRecord(state, chunk[idx].Key, bucket, rec)
			}
		}
	}
	state.UpdatedAt = time.Now().UTC()
	return nil
}

func selectedScanKeys(state *State, source string) []string {
	keys := make([]string, 0)
	switch strings.ToLower(source) {
	case "target":
		for k := range state.Target {
			keys = append(keys, k)
		}
	case "both":
		seen := map[string]struct{}{}
		for k := range state.Local {
			seen[k] = struct{}{}
			keys = append(keys, k)
		}
		for k := range state.Target {
			if _, ok := seen[k]; !ok {
				keys = append(keys, k)
			}
		}
	default:
		for k := range state.Local {
			keys = append(keys, k)
		}
	}
	return keys
}

func getStateRecord(state *State, key, source string) (StateRecord, string) {
	if strings.EqualFold(source, "target") {
		return state.Target[key], "target"
	}
	if rec, ok := state.Local[key]; ok {
		return rec, "local"
	}
	if strings.EqualFold(source, "both") {
		if rec, ok := state.Target[key]; ok {
			return rec, "target"
		}
	}
	return state.Local[key], "local"
}

func setStateRecord(state *State, key, bucket string, rec StateRecord) {
	if bucket == "target" {
		state.Target[key] = rec
		return
	}
	state.Local[key] = rec
}

type severityLevel int

const (
	sevNone severityLevel = iota
	sevLow
	sevMedium
	sevHigh
	sevCritical
)

func parseSeverityLevel(raw string) (severityLevel, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "none":
		return sevNone, nil
	case "low":
		return sevLow, nil
	case "medium":
		return sevMedium, nil
	case "high":
		return sevHigh, nil
	case "critical":
		return sevCritical, nil
	default:
		return sevNone, fmt.Errorf("unsupported severity %q", raw)
	}
}

func loadExceptions(path string) ([]ScanException, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var file ScanExceptionFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.Exceptions, nil
}

func isExceptionMatch(ex []ScanException, rec StateRecord, vulnID string) bool {
	now := time.Now().UTC()
	pkg := rec.Name
	key := rec.Name + "@" + rec.Version
	for _, item := range ex {
		if item.VulnID != "" && !strings.EqualFold(item.VulnID, vulnID) {
			continue
		}
		if item.Package != "" && item.Package != pkg && item.Package != key {
			continue
		}
		if item.ExpiresAt != "" {
			t, err := time.Parse(time.RFC3339, item.ExpiresAt)
			if err != nil || now.After(t) {
				continue
			}
		}
		return true
	}
	return false
}

func fetchOSVSeverityLevels(ctx context.Context, client *http.Client, opts ScanOptions, ids map[string]struct{}, unknown severityLevel, cache map[string]severityLevel) (map[string]severityLevel, error) {
	type out struct {
		id    string
		level severityLevel
		err   error
	}
	endpointBase := strings.TrimSuffix(opts.OSVEndpoint, "/querybatch")
	jobs := make(chan string)
	results := make(chan out, len(ids))
	var wg sync.WaitGroup
	for i := 0; i < opts.OSVConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				if level, ok := cache[id]; ok {
					results <- out{id: id, level: level}
					continue
				}
				level, err := fetchOSVSeverityLevel(ctx, client, endpointBase+"/vulns/"+id, unknown)
				results <- out{id: id, level: level, err: err}
			}
		}()
	}
	for id := range ids {
		jobs <- id
	}
	close(jobs)
	wg.Wait()
	close(results)
	outMap := map[string]severityLevel{}
	for r := range results {
		if r.err != nil {
			return nil, r.err
		}
		outMap[r.id] = r.level
	}
	return outMap, nil
}

func fetchOSVSeverityLevel(ctx context.Context, client *http.Client, url string, unknown severityLevel) (severityLevel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return unknown, err
	}
	res, err := client.Do(req)
	if err != nil {
		return unknown, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return unknown, fmt.Errorf("osv vuln lookup failed: %s", res.Status)
	}
	var body struct {
		DatabaseSpecific struct {
			Severity string `json:"severity"`
		} `json:"database_specific"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return unknown, err
	}
	switch strings.ToLower(strings.TrimSpace(body.DatabaseSpecific.Severity)) {
	case "low":
		return sevLow, nil
	case "medium":
		return sevMedium, nil
	case "high":
		return sevHigh, nil
	case "critical":
		return sevCritical, nil
	default:
		return unknown, nil
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
