package daemon

import (
	"sync"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

const nonDaemonDeliveryRetryDelay = 100 * time.Millisecond

// nonDaemonDeliveryBudget bounds concurrency for the post/auto-PING/
// manual-PING delivery paths (Issue #572). Despite the "shared" name, it is
// three INDEPENDENT per-path pools of workerLimit() each (postSem/
// autoPingSem/manualPingSem below) — not one pool shared across paths, and
// not coupled to config.DaemonSubmitWorkerLimit (the unrelated daemon-submit
// CLI worker pool). At most 3*workerLimit() non-daemon-submit deliveries can
// be in flight concurrently, split evenly across the three paths.
type nonDaemonDeliveryPath string

const (
	nonDaemonDeliveryPathPost       nonDaemonDeliveryPath = "post"
	nonDaemonDeliveryPathAutoPing   nonDaemonDeliveryPath = "auto_ping"
	nonDaemonDeliveryPathManualPing nonDaemonDeliveryPath = "manual_ping"
)

type nonDaemonDeliveryBudget struct {
	mu sync.Mutex

	// manualFanoutMu serializes manual-PING fanout rounds (Issue #572 B2):
	// beginManualFanout locks it and finishManualFanout unlocks it, so a
	// second overlapping "p" keypress blocks until the prior round's
	// pending/saturation bookkeeping has been finalized, instead of both
	// rounds sharing manualPingSem and corrupting each other's counters.
	manualFanoutMu sync.Mutex

	postSem       chan struct{}
	autoPingSem   chan struct{}
	manualPingSem chan struct{}

	pendingPost       int
	pendingAutoPing   int
	pendingManualPing int

	saturationCount    int
	lastSaturatedAt    time.Time
	manualSaturatedHit bool

	clock func() time.Time
}

func newNonDaemonDeliveryBudget(clock func() time.Time) *nonDaemonDeliveryBudget {
	if clock == nil {
		clock = time.Now
	}
	limit := config.DefaultDaemonSubmitWorkerLimit
	return &nonDaemonDeliveryBudget{
		postSem:       make(chan struct{}, limit),
		autoPingSem:   make(chan struct{}, limit),
		manualPingSem: make(chan struct{}, limit),
		clock:         clock,
	}
}

func (b *nonDaemonDeliveryBudget) workerLimit() int {
	if b == nil {
		return config.DefaultDaemonSubmitWorkerLimit
	}
	return cap(b.postSem)
}

func (b *nonDaemonDeliveryBudget) tryStart(path nonDaemonDeliveryPath) bool {
	if b == nil {
		return true
	}
	sem := b.sem(path)
	if sem == nil {
		return true
	}
	select {
	case sem <- struct{}{}:
		return true
	default:
		b.recordSaturation()
		return false
	}
}

func (b *nonDaemonDeliveryBudget) finish(path nonDaemonDeliveryPath) {
	if b == nil {
		return
	}
	sem := b.sem(path)
	if sem == nil {
		return
	}
	select {
	case <-sem:
	default:
	}
}

func (b *nonDaemonDeliveryBudget) queue(path nonDaemonDeliveryPath) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch path {
	case nonDaemonDeliveryPathPost:
		b.pendingPost++
	case nonDaemonDeliveryPathAutoPing:
		b.pendingAutoPing++
	case nonDaemonDeliveryPathManualPing:
		b.pendingManualPing++
	}
}

func (b *nonDaemonDeliveryBudget) unqueue(path nonDaemonDeliveryPath) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch path {
	case nonDaemonDeliveryPathPost:
		if b.pendingPost > 0 {
			b.pendingPost--
		}
	case nonDaemonDeliveryPathAutoPing:
		if b.pendingAutoPing > 0 {
			b.pendingAutoPing--
		}
	case nonDaemonDeliveryPathManualPing:
		if b.pendingManualPing > 0 {
			b.pendingManualPing--
		}
	}
}

// beginManualFanout starts a manual-PING fanout round, serialized against
// any other round via manualFanoutMu (Issue #572 B2). Every call that
// returns must be paired with a later call to finishManualFanout, including
// the total<=0 case, to release the lock.
func (b *nonDaemonDeliveryBudget) beginManualFanout(total int) int {
	if b == nil {
		return 0
	}
	b.manualFanoutMu.Lock()
	if total <= 0 {
		return 0
	}
	limit := b.workerLimit()
	b.queueManual(total)
	if total > limit {
		b.recordSaturationOnceForManualFanout()
		return limit
	}
	return total
}

func (b *nonDaemonDeliveryBudget) finishManualFanout() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.manualSaturatedHit = false
	b.pendingManualPing = 0
	b.mu.Unlock()
	b.manualFanoutMu.Unlock()
}

func (b *nonDaemonDeliveryBudget) queueManual(count int) {
	if count <= 0 {
		return
	}
	b.mu.Lock()
	b.pendingManualPing += count
	b.mu.Unlock()
}

func (b *nonDaemonDeliveryBudget) recordSaturationOnceForManualFanout() {
	b.mu.Lock()
	if !b.manualSaturatedHit {
		b.saturationCount++
		b.lastSaturatedAt = b.now()
		b.manualSaturatedHit = true
	}
	b.mu.Unlock()
}

func (b *nonDaemonDeliveryBudget) recordSaturation() {
	b.mu.Lock()
	b.saturationCount++
	b.lastSaturatedAt = b.now()
	b.mu.Unlock()
}

func (b *nonDaemonDeliveryBudget) snapshot() status.NonDaemonDeliveryRuntimeDiagnostics {
	if b == nil {
		return status.NonDaemonDeliveryRuntimeDiagnostics{
			WorkerLimit: config.DefaultDaemonSubmitWorkerLimit,
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	diag := status.NonDaemonDeliveryRuntimeDiagnostics{
		WorkerLimit:            b.workerLimit(),
		ActivePostCount:        len(b.postSem),
		PendingPostCount:       b.pendingPost,
		ActiveAutoPingCount:    len(b.autoPingSem),
		PendingAutoPingCount:   b.pendingAutoPing,
		ActiveManualPingCount:  len(b.manualPingSem),
		PendingManualPingCount: b.pendingManualPing,
		SaturationCount:        b.saturationCount,
	}
	if !b.lastSaturatedAt.IsZero() {
		diag.LastSaturatedAt = b.lastSaturatedAt.UTC().Format(time.RFC3339Nano)
	}
	return diag
}

func (b *nonDaemonDeliveryBudget) sem(path nonDaemonDeliveryPath) chan struct{} {
	switch path {
	case nonDaemonDeliveryPathPost:
		return b.postSem
	case nonDaemonDeliveryPathAutoPing:
		return b.autoPingSem
	case nonDaemonDeliveryPathManualPing:
		return b.manualPingSem
	default:
		return nil
	}
}

func (b *nonDaemonDeliveryBudget) now() time.Time {
	if b.clock == nil {
		return time.Now()
	}
	return b.clock()
}
