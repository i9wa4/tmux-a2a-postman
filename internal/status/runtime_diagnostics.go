package status

import (
	"runtime"
	"time"
)

func NewRuntimeDiagnostics(source string, cardinality DaemonRuntimeCardinality, daemonSubmit DaemonSubmitRuntimeDiagnostics, observedAt time.Time) RuntimeDiagnostics {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	lastPauseNS := uint64(0)
	if mem.NumGC > 0 {
		lastPauseNS = mem.PauseNs[(mem.NumGC+255)%256]
	}

	return RuntimeDiagnostics{
		Source:      source,
		PointInTime: true,
		ObservedAt:  observedAt.UTC().Format(time.RFC3339Nano),
		GoRuntime: GoRuntimeDiagnostics{
			Memory: RuntimeMemoryDiagnostics{
				HeapAllocBytes:   mem.HeapAlloc,
				HeapSysBytes:     mem.HeapSys,
				HeapObjects:      mem.HeapObjects,
				StackInuseBytes:  mem.StackInuse,
				TotalAllocBytes:  mem.TotalAlloc,
				MemorySysBytes:   mem.Sys,
				MemoryFreesCount: mem.Frees,
			},
			GC: RuntimeGCDiagnostics{
				Count:        mem.NumGC,
				NextGCBytes:  mem.NextGC,
				PauseTotalNS: mem.PauseTotalNs,
				LastPauseNS:  lastPauseNS,
			},
			GoroutineCount: runtime.NumGoroutine(),
		},
		Daemon:       cardinality,
		DaemonSubmit: daemonSubmit,
	}
}
