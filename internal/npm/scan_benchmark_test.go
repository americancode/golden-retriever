package npm

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func BenchmarkScanStateOSVOfflineSerial(b *testing.B) {
	benchmarkScanStateOSVOffline(b, 1, 1, 20)
}

func BenchmarkScanStateOSVOfflineParallel(b *testing.B) {
	benchmarkScanStateOSVOffline(b, 1, 4, 20)
}

func benchmarkScanStateOSVOffline(b *testing.B, batchSize, concurrency, packageCount int) {
	packages := make([]StateRecord, 0, packageCount)
	for i := 0; i < packageCount; i++ {
		packages = append(packages, StateRecord{
			Name:    fmt.Sprintf("pkg-%02d", i),
			Version: "1.0.0",
		})
	}
	statePath := writeScanTestStateWithPackages(b, packages)
	installFakeSlowOSVScanner(b, 100*time.Millisecond, `{"results":[]}`)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := saveState(statePath, &State{
			SchemaVersion: 1,
			Target:        recordsToStateMap(packages),
			Local:         map[string]StateRecord{},
		}); err != nil {
			b.Fatalf("saveState: %v", err)
		}
		report, err := ScanState(context.Background(), ScanOptions{
			StatePath:             statePath,
			Source:                "target",
			UseOSV:                true,
			OSVProvider:           "osv-offline",
			OSVOfflineChunkSize:   batchSize,
			OSVOfflineConcurrency: concurrency,
			MinSeverity:           "high",
			UnknownSeverity:       "high",
		})
		if err != nil {
			b.Fatalf("ScanState error: %v", err)
		}
		if report.Total != packageCount {
			b.Fatalf("report.Total = %d, want %d", report.Total, packageCount)
		}
	}
}

func recordsToStateMap(records []StateRecord) map[string]StateRecord {
	out := make(map[string]StateRecord, len(records))
	for _, rec := range records {
		out[rec.Name+"@"+rec.Version] = rec
	}
	return out
}
