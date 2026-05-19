package npm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type osvAPIProvider struct{}

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
		Vulns []osvVulnerability `json:"vulns"`
	} `json:"results"`
}

type osvVulnerability struct {
	ID      string `json:"id"`
	Summary string `json:"summary,omitempty"`
	Details string `json:"details,omitempty"`
}

type osvVulnDetail struct {
	Level       severityLevel
	Description string
}

func (osvAPIProvider) Name() string {
	return "osv-api"
}

func (osvAPIProvider) ApplyFindings(ctx context.Context, state *State, opts ScanOptions, keys []string) error {
	return applyOSVFindings(ctx, state, opts, keys)
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
	vulnCache := map[string]osvVulnDetail{}
	for i := 0; i < len(records); i += opts.OSVAPIBatchSize {
		end := i + opts.OSVAPIBatchSize
		if end > len(records) {
			end = len(records)
		}
		chunk := records[i:end]
		if opts.Progress != nil {
			opts.Progress("osv:batch:start endpoint=%s batch=%d queries=%d", opts.OSVEndpoint, (i/opts.OSVAPIBatchSize)+1, len(chunk))
		}
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
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.OSVEndpoint, strings.NewReader(string(payload)))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			if opts.Progress != nil {
				opts.Progress("osv:batch:error endpoint=%s batch=%d error=%v", opts.OSVEndpoint, (i/opts.OSVAPIBatchSize)+1, err)
			}
			return err
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			if opts.Progress != nil {
				opts.Progress("osv:batch:done endpoint=%s batch=%d status=%s", opts.OSVEndpoint, (i/opts.OSVAPIBatchSize)+1, resp.Status)
			}
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
		if opts.Progress != nil {
			opts.Progress("osv:batch:done endpoint=%s batch=%d status=%s vuln_ids=%d", opts.OSVEndpoint, (i/opts.OSVAPIBatchSize)+1, resp.Status, len(idsToResolve))
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
			descriptions := make([]string, 0, len(result.Vulns))
			block := false
			for _, v := range result.Vulns {
				if v.ID == "" {
					continue
				}
				if isExceptionMatch(exceptions, rec, v.ID) {
					continue
				}
				hitIDs = append(hitIDs, v.ID)
				if detail, ok := levels[v.ID]; ok {
					descriptions = appendVulnDescription(descriptions, v.ID, firstNonEmptyString(detail.Description, v.Summary, v.Details))
					if detail.Level >= minLevel {
						block = true
					}
					continue
				}
				descriptions = appendVulnDescription(descriptions, v.ID, firstNonEmptyString(v.Summary, v.Details))
				if unknownLevel >= minLevel {
					block = true
				}
			}
			if len(hitIDs) == 0 {
				continue
			}
			if block {
				rec.ScanStatus = "fail"
				rec.ScanReason = fmt.Sprintf("osv vulnerabilities (%s+): %s", opts.MinSeverity, strings.Join(hitIDs, ","))
				rec.ScanVulnURLs = vulnURLs(hitIDs)
				rec.ScanVulnDescriptions = appendUniqueStrings(rec.ScanVulnDescriptions, descriptions...)
				rec.ScannedAt = time.Now().UTC()
				setStateRecord(state, chunk[idx].Key, bucket, rec)
				if opts.Progress != nil {
					opts.Progress("scan:vuln package=%s@%s severity=%s ids=%s urls=%s descriptions=%s provider=osv-api", rec.Name, rec.Version, highestSeverityForIDs(hitIDs, levels, unknownLevel).String(), strings.Join(hitIDs, ","), strings.Join(rec.ScanVulnURLs, ","), strings.Join(rec.ScanVulnDescriptions, " | "))
				}
			}
		}
	}
	state.UpdatedAt = time.Now().UTC()
	return nil
}

func highestSeverityForIDs(ids []string, levels map[string]osvVulnDetail, unknown severityLevel) severityLevel {
	best := sevNone
	for _, id := range ids {
		detail, ok := levels[id]
		level := detail.Level
		if !ok {
			level = unknown
		}
		if level > best {
			best = level
		}
	}
	return best
}

func vulnURLs(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(ids))
	urls := make([]string, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		url := "https://osv.dev/vulnerability/" + id
		if _, ok := seen[url]; ok {
			continue
		}
		seen[url] = struct{}{}
		urls = append(urls, url)
	}
	return urls
}

func fetchOSVSeverityLevels(ctx context.Context, client *http.Client, opts ScanOptions, ids map[string]struct{}, unknown severityLevel, cache map[string]osvVulnDetail) (map[string]osvVulnDetail, error) {
	type out struct {
		id     string
		detail osvVulnDetail
		err    error
	}
	endpointBase := strings.TrimSuffix(opts.OSVEndpoint, "/querybatch")
	if opts.Progress != nil {
		opts.Progress("osv:detail:start endpoint=%s ids=%d concurrency=%d", endpointBase+"/vulns/{id}", len(ids), opts.OSVAPIConcurrency)
	}
	jobs := make(chan string)
	results := make(chan out, len(ids))
	var wg sync.WaitGroup
	for i := 0; i < opts.OSVAPIConcurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				if detail, ok := cache[id]; ok {
					if opts.Progress != nil {
						opts.Progress("osv:detail:cache id=%s", id)
					}
					results <- out{id: id, detail: detail}
					continue
				}
				detail, err := fetchOSVVulnDetail(ctx, client, endpointBase+"/vulns/"+id, unknown)
				if opts.Progress != nil {
					if err != nil {
						opts.Progress("osv:detail:error id=%s error=%v", id, err)
					} else {
						opts.Progress("osv:detail:done id=%s severity=%s description=%t", id, detail.Level.String(), detail.Description != "")
					}
				}
				results <- out{id: id, detail: detail, err: err}
			}
		}()
	}
	for id := range ids {
		jobs <- id
	}
	close(jobs)
	wg.Wait()
	close(results)
	outMap := map[string]osvVulnDetail{}
	for r := range results {
		if r.err != nil {
			return nil, r.err
		}
		outMap[r.id] = r.detail
	}
	if opts.Progress != nil {
		opts.Progress("osv:detail:complete ids=%d", len(outMap))
	}
	return outMap, nil
}

func fetchOSVVulnDetail(ctx context.Context, client *http.Client, url string, unknown severityLevel) (osvVulnDetail, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return osvVulnDetail{Level: unknown}, err
	}
	res, err := client.Do(req)
	if err != nil {
		return osvVulnDetail{Level: unknown}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return osvVulnDetail{Level: unknown}, fmt.Errorf("osv vuln lookup failed: %s", res.Status)
	}
	var body struct {
		Summary          string `json:"summary"`
		Details          string `json:"details"`
		DatabaseSpecific struct {
			Severity string `json:"severity"`
		} `json:"database_specific"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return osvVulnDetail{Level: unknown}, err
	}
	detail := osvVulnDetail{
		Level:       unknown,
		Description: firstNonEmptyString(body.Summary, body.Details),
	}
	switch strings.ToLower(strings.TrimSpace(body.DatabaseSpecific.Severity)) {
	case "low":
		detail.Level = sevLow
	case "medium":
		detail.Level = sevMedium
	case "high":
		detail.Level = sevHigh
	case "critical":
		detail.Level = sevCritical
	}
	return detail, nil
}
