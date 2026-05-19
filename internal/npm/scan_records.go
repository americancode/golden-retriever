package npm

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

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

func scanFindingJSON(rec StateRecord) string {
	finding := ScanFinding{
		Package:          rec.Name + "@" + rec.Version,
		Status:           "fail",
		Reason:           rec.ScanReason,
		VulnURLs:         append([]string(nil), rec.ScanVulnURLs...),
		VulnDescriptions: append([]string(nil), rec.ScanVulnDescriptions...),
		ScannedAt:        rec.ScannedAt,
	}
	data, err := json.Marshal(finding)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func matchingException(ex []ScanException, rec StateRecord, vulnID string) (ScanException, bool) {
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
		return item, true
	}
	return ScanException{}, false
}

func isExceptionMatch(ex []ScanException, rec StateRecord, vulnID string) bool {
	_, ok := matchingException(ex, rec, vulnID)
	return ok
}
