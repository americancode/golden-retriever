package npm

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPruneMetadataCacheRemovesOldEntries(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.json")
	newPath := filepath.Join(dir, "new.json")
	otherPath := filepath.Join(dir, "other.txt")
	for _, path := range []string{oldPath, newPath, otherPath} {
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	report, err := PruneMetadataCache(CachePruneOptions{Dir: dir, MaxAge: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 2 || report.Removed != 1 || report.Failed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old cache entry should be removed, err=%v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new cache entry should remain: %v", err)
	}
	if _, err := os.Stat(otherPath); err != nil {
		t.Fatalf("non-json entry should remain: %v", err)
	}
}

func TestPruneMetadataCacheRemoveAll(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "entry.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := PruneMetadataCache(CachePruneOptions{Dir: dir, RemoveAll: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Removed != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cache entry should be removed, err=%v", err)
	}
}
