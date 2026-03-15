package alert

import (
	"sync"
	"testing"
	"time"
)

func TestAllow_NoPriorSend(t *testing.T) {
	r := NewAlertRateLimiter(5 * time.Minute)
	now := time.Now()
	if !r.Allow("nodeA", now) {
		t.Error("Allow should return true when no prior send")
	}
}

func TestAllow_WithinCooldown(t *testing.T) {
	r := NewAlertRateLimiter(5 * time.Minute)
	now := time.Now()
	r.Record("nodeA", now)
	if r.Allow("nodeA", now.Add(1*time.Minute)) {
		t.Error("Allow should return false within cooldown window")
	}
}

func TestAllow_AfterCooldownExpires(t *testing.T) {
	r := NewAlertRateLimiter(5 * time.Minute)
	now := time.Now()
	r.Record("nodeA", now)
	if !r.Allow("nodeA", now.Add(5*time.Minute)) {
		t.Error("Allow should return true after cooldown expires (at boundary)")
	}
	if !r.Allow("nodeA", now.Add(6*time.Minute)) {
		t.Error("Allow should return true after cooldown expires (past boundary)")
	}
}

func TestRecord_ThenAllow(t *testing.T) {
	r := NewAlertRateLimiter(10 * time.Minute)
	now := time.Now()
	r.Record("nodeB", now)
	if r.Allow("nodeB", now.Add(9*time.Minute)) {
		t.Error("Allow should return false within cooldown after Record")
	}
}

func TestIndependentNodes(t *testing.T) {
	r := NewAlertRateLimiter(5 * time.Minute)
	now := time.Now()
	r.Record("nodeA", now)
	// nodeB has no record; should be allowed
	if !r.Allow("nodeB", now.Add(1*time.Minute)) {
		t.Error("nodeB cooldown should be independent of nodeA")
	}
	// nodeA is within cooldown
	if r.Allow("nodeA", now.Add(1*time.Minute)) {
		t.Error("nodeA should be in cooldown")
	}
}

func TestConcurrentAccess(t *testing.T) {
	r := NewAlertRateLimiter(1 * time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			now := time.Now()
			r.Record("nodeC", now)
			_ = r.Allow("nodeC", now)
		}()
	}
	wg.Wait()
}
