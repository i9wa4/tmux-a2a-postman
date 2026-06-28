package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSamplesPassesBoundedGrowth(t *testing.T) {
	samples := []statusSample{
		sample("2026-06-01T00:00:00Z", 1000, 2000, 20, 0, 0, 0, 0, 0),
		sample("2026-06-01T00:10:00Z", 1100, 2100, 22, 0, 0, 4, 0, 1),
		sample("2026-06-01T00:20:00Z", 1200, 2200, 25, 0, 0, 5, 0, 2),
	}

	failures := validateSamples(samples, thresholdConfig{
		minSamples:                 3,
		maxHeapAllocGrowthPercent:  25,
		maxMemorySysGrowthPercent:  25,
		maxGoroutineGrowth:         20,
		maxLateResponses:           0,
		maxOldestLateAgeSeconds:    0,
		maxOldestPendingAgeSeconds: 30,
		maxOldestClaimedAgeSeconds: 30,
	})
	if len(failures) != 0 {
		t.Fatalf("validateSamples failures = %#v, want none", failures)
	}
}

func TestValidateSamplesReportsPressureFailures(t *testing.T) {
	samples := []statusSample{
		sample("2026-06-01T00:00:00Z", 1000, 2000, 20, 0, 0, 0, 0, 0),
		sample("2026-06-01T00:10:00Z", 1400, 2700, 50, 2, 120, 90, 80, 0),
	}

	failures := validateSamples(samples, thresholdConfig{
		minSamples:                 2,
		maxHeapAllocGrowthPercent:  25,
		maxMemorySysGrowthPercent:  25,
		maxGoroutineGrowth:         20,
		maxLateResponses:           0,
		maxOldestLateAgeSeconds:    0,
		maxOldestPendingAgeSeconds: 30,
		maxOldestClaimedAgeSeconds: 30,
		requireSaturation:          true,
	})
	if len(failures) != 8 {
		t.Fatalf("validateSamples failure count = %d (%#v), want 8", len(failures), failures)
	}
}

func TestReadSamplesRequiresRuntimeDiagnostics(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "status.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":4}`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := readSamples([]string{path})
	if err == nil {
		t.Fatal("readSamples error = nil, want missing runtime_diagnostics error")
	}
}

func sample(observedAt string, heapAlloc, memorySys uint64, goroutines, lateResponses, oldestLate, oldestPending, oldestClaimed, saturation int) statusSample {
	return statusSample{
		RuntimeDiagnostics: runtimeDiagnostics{
			ObservedAt: observedAt,
			GoRuntime: goRuntimeDiagnostics{
				Memory: memoryDiagnostics{
					HeapAllocBytes: heapAlloc,
					MemorySysBytes: memorySys,
				},
				GoroutineCount: goroutines,
			},
			DaemonSubmit: daemonSubmitDiagnostics{
				LateResponseCount:            lateResponses,
				OldestLateResponseAgeSeconds: oldestLate,
				OldestPendingAgeSeconds:      oldestPending,
				OldestClaimedAgeSeconds:      oldestClaimed,
				SaturationCount:              saturation,
			},
		},
	}
}
