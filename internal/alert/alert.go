package alert

import (
	"sync"
	"time"
)

// AlertRateLimiter limits alert and warning sends per recipient node.
// Keyed by recipientNode only — any send to a node resets the cooldown
// for that node regardless of alert/warning type.
type AlertRateLimiter struct {
	mu         sync.Mutex
	lastSentAt map[string]time.Time
	cooldown   time.Duration
}

// NewAlertRateLimiter creates a new AlertRateLimiter with the given cooldown.
func NewAlertRateLimiter(cooldown time.Duration) *AlertRateLimiter {
	return &AlertRateLimiter{
		lastSentAt: make(map[string]time.Time),
		cooldown:   cooldown,
	}
}

// Allow returns true if enough time has passed since the last send to recipientNode.
func (r *AlertRateLimiter) Allow(recipientNode string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	last, ok := r.lastSentAt[recipientNode]
	if !ok {
		return true
	}
	return now.Sub(last) >= r.cooldown
}

// Record records a successful send to recipientNode.
func (r *AlertRateLimiter) Record(recipientNode string, now time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastSentAt[recipientNode] = now
}
