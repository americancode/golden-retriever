package npm

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

type CachePruneOptions struct {
	Dir       string
	MaxAge    time.Duration
	RemoveAll bool
}

type CachePruneReport struct {
	Scanned int
	Removed int
	Failed  int
	Elapsed time.Duration
}

func PruneMetadataCache(opts CachePruneOptions) (CachePruneReport, error) {
	start := time.Now()
	var report CachePruneReport
	if opts.Dir == "" {
		report.Elapsed = time.Since(start)
		return report, nil
	}
	entries, err := os.ReadDir(opts.Dir)
	if os.IsNotExist(err) {
		report.Elapsed = time.Since(start)
		return report, nil
	}
	if err != nil {
		report.Elapsed = time.Since(start)
		return report, err
	}
	cutoff := time.Now().Add(-opts.MaxAge)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		report.Scanned++
		path := filepath.Join(opts.Dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			report.Failed++
			continue
		}
		if !opts.RemoveAll && opts.MaxAge > 0 && info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(path); err != nil {
			report.Failed++
			continue
		}
		report.Removed++
	}
	report.Elapsed = time.Since(start)
	return report, nil
}
