package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
)

type thresholdConfig struct {
	minSamples                 int
	maxHeapAllocGrowthPercent  float64
	maxMemorySysGrowthPercent  float64
	maxGoroutineGrowth         int
	maxLateResponses           int
	maxOldestLateAgeSeconds    int
	maxOldestPendingAgeSeconds int
	maxOldestClaimedAgeSeconds int
	requireSaturation          bool
}

type statusSample struct {
	RuntimeDiagnostics runtimeDiagnostics `json:"runtime_diagnostics"`
}

type runtimeDiagnostics struct {
	ObservedAt   string                  `json:"observed_at"`
	GoRuntime    goRuntimeDiagnostics    `json:"go_runtime"`
	DaemonSubmit daemonSubmitDiagnostics `json:"daemon_submit"`
}

type goRuntimeDiagnostics struct {
	Memory         memoryDiagnostics `json:"memory"`
	GoroutineCount int               `json:"goroutine_count"`
}

type memoryDiagnostics struct {
	HeapAllocBytes uint64 `json:"heap_alloc_bytes"`
	MemorySysBytes uint64 `json:"memory_sys_bytes"`
}

type daemonSubmitDiagnostics struct {
	OldestPendingAgeSeconds      int `json:"oldest_pending_age_seconds"`
	OldestClaimedAgeSeconds      int `json:"oldest_claimed_age_seconds"`
	LateResponseCount            int `json:"late_response_count"`
	OldestLateResponseAgeSeconds int `json:"oldest_late_response_age_seconds"`
	SaturationCount              int `json:"saturation_count"`
}

func main() {
	cfg := thresholdConfig{}
	flag.IntVar(&cfg.minSamples, "min-samples", 3, "minimum runtime_diagnostics samples")
	flag.Float64Var(&cfg.maxHeapAllocGrowthPercent, "max-heap-alloc-growth-percent", 25, "maximum first-to-last heap_alloc growth percentage")
	flag.Float64Var(&cfg.maxMemorySysGrowthPercent, "max-memory-sys-growth-percent", 25, "maximum first-to-last memory_sys growth percentage")
	flag.IntVar(&cfg.maxGoroutineGrowth, "max-goroutine-growth", 20, "maximum first-to-last goroutine growth")
	flag.IntVar(&cfg.maxLateResponses, "max-late-responses", 0, "maximum final late response count")
	flag.IntVar(&cfg.maxOldestLateAgeSeconds, "max-oldest-late-age-seconds", 0, "maximum final oldest late response age")
	flag.IntVar(&cfg.maxOldestPendingAgeSeconds, "max-oldest-pending-age-seconds", 30, "maximum final oldest pending request age")
	flag.IntVar(&cfg.maxOldestClaimedAgeSeconds, "max-oldest-claimed-age-seconds", 30, "maximum final oldest claimed request age")
	flag.BoolVar(&cfg.requireSaturation, "require-saturation", false, "require at least one daemon-submit saturation event")
	flag.Parse()

	if err := run(flag.Args(), cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(paths []string, cfg thresholdConfig) error {
	samples, err := readSamples(paths)
	if err != nil {
		return err
	}
	failures := validateSamples(samples, cfg)
	if len(failures) > 0 {
		for _, failure := range failures {
			fmt.Fprintf(os.Stderr, "FAIL: %s\n", failure)
		}
		return fmt.Errorf("daemon soak validation failed with %d failure(s)", len(failures))
	}

	first := samples[0].RuntimeDiagnostics
	final := samples[len(samples)-1].RuntimeDiagnostics
	fmt.Printf(
		"PASS samples=%d first_observed_at=%s final_observed_at=%s heap_alloc_growth_percent=%.2f memory_sys_growth_percent=%.2f goroutine_growth=%d late_responses=%d saturation_count=%d\n",
		len(samples),
		first.ObservedAt,
		final.ObservedAt,
		growthPercent(first.GoRuntime.Memory.HeapAllocBytes, final.GoRuntime.Memory.HeapAllocBytes),
		growthPercent(first.GoRuntime.Memory.MemorySysBytes, final.GoRuntime.Memory.MemorySysBytes),
		final.GoRuntime.GoroutineCount-first.GoRuntime.GoroutineCount,
		final.DaemonSubmit.LateResponseCount,
		final.DaemonSubmit.SaturationCount,
	)
	return nil
}

func readSamples(paths []string) ([]statusSample, error) {
	if len(paths) == 0 {
		return nil, errors.New("usage: daemon-soak-check [flags] <get-status-debug-json>")
	}
	sort.Strings(paths)
	samples := make([]statusSample, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var sample statusSample
		if err := json.Unmarshal(data, &sample); err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		if sample.RuntimeDiagnostics.ObservedAt == "" {
			return nil, fmt.Errorf("%s: missing runtime_diagnostics.observed_at; capture with get-status --debug", path)
		}
		samples = append(samples, sample)
	}
	return samples, nil
}

func validateSamples(samples []statusSample, cfg thresholdConfig) []string {
	var failures []string
	if len(samples) < cfg.minSamples {
		failures = append(failures, fmt.Sprintf("samples=%d below min_samples=%d", len(samples), cfg.minSamples))
		return failures
	}

	first := samples[0].RuntimeDiagnostics
	final := samples[len(samples)-1].RuntimeDiagnostics
	if got := growthPercent(first.GoRuntime.Memory.HeapAllocBytes, final.GoRuntime.Memory.HeapAllocBytes); got > cfg.maxHeapAllocGrowthPercent {
		failures = append(failures, fmt.Sprintf("heap_alloc_growth_percent=%.2f exceeds %.2f", got, cfg.maxHeapAllocGrowthPercent))
	}
	if got := growthPercent(first.GoRuntime.Memory.MemorySysBytes, final.GoRuntime.Memory.MemorySysBytes); got > cfg.maxMemorySysGrowthPercent {
		failures = append(failures, fmt.Sprintf("memory_sys_growth_percent=%.2f exceeds %.2f", got, cfg.maxMemorySysGrowthPercent))
	}
	if got := final.GoRuntime.GoroutineCount - first.GoRuntime.GoroutineCount; got > cfg.maxGoroutineGrowth {
		failures = append(failures, fmt.Sprintf("goroutine_growth=%d exceeds %d", got, cfg.maxGoroutineGrowth))
	}
	if got := final.DaemonSubmit.LateResponseCount; got > cfg.maxLateResponses {
		failures = append(failures, fmt.Sprintf("late_response_count=%d exceeds %d", got, cfg.maxLateResponses))
	}
	if got := final.DaemonSubmit.OldestLateResponseAgeSeconds; got > cfg.maxOldestLateAgeSeconds {
		failures = append(failures, fmt.Sprintf("oldest_late_response_age_seconds=%d exceeds %d", got, cfg.maxOldestLateAgeSeconds))
	}
	if got := final.DaemonSubmit.OldestPendingAgeSeconds; got > cfg.maxOldestPendingAgeSeconds {
		failures = append(failures, fmt.Sprintf("oldest_pending_age_seconds=%d exceeds %d", got, cfg.maxOldestPendingAgeSeconds))
	}
	if got := final.DaemonSubmit.OldestClaimedAgeSeconds; got > cfg.maxOldestClaimedAgeSeconds {
		failures = append(failures, fmt.Sprintf("oldest_claimed_age_seconds=%d exceeds %d", got, cfg.maxOldestClaimedAgeSeconds))
	}
	if cfg.requireSaturation && final.DaemonSubmit.SaturationCount == 0 {
		failures = append(failures, "saturation_count=0 but saturation is required")
	}
	return failures
}

func growthPercent(first, final uint64) float64 {
	if first == 0 {
		if final == 0 {
			return 0
		}
		return 100
	}
	if final <= first {
		return 0
	}
	return float64(final-first) * 100 / float64(first)
}
