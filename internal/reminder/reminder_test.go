package reminder

import (
	"sync"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func TestNewReminderState(t *testing.T) {
	state := NewReminderState()
	if state == nil {
		t.Fatal("NewReminderState() returned nil")
	}
	if state.counters == nil {
		t.Error("counters map should be initialized")
	}
}

func TestReminderIncrement(t *testing.T) {
	state := NewReminderState()
	nodes := map[string]discovery.NodeInfo{
		"worker": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout:      5.0,
		ReminderInterval: 5.0,
		ReminderMessage:  "REMINDER: You have {count} pending messages",
		Nodes:            map[string]config.NodeConfig{},
	}

	// Increment counter below threshold
	for i := 1; i <= 3; i++ {
		state.Increment("worker", nodes, cfg)
		state.mu.Lock()
		count := state.counters["worker"]
		state.mu.Unlock()
		if count != i {
			t.Errorf("After %d increments, counter = %d, want %d", i, count, i)
		}
	}
}

func TestReminderThreshold(t *testing.T) {
	state := NewReminderState()
	nodes := map[string]discovery.NodeInfo{
		"worker": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout:      5.0,
		ReminderInterval: 3.0, // Threshold = 3
		ReminderMessage:  "REMINDER: You have {count} pending messages",
		Nodes:            map[string]config.NodeConfig{},
	}

	// Increment to threshold
	for i := 1; i <= 3; i++ {
		state.Increment("worker", nodes, cfg)
	}

	// After reaching threshold, counter should be reset to 0
	state.mu.Lock()
	count := state.counters["worker"]
	state.mu.Unlock()

	if count != 0 {
		t.Errorf("After reaching threshold, counter = %d, want 0 (should be reset)", count)
	}

	// Increment again - should start from 0
	state.Increment("worker", nodes, cfg)
	state.mu.Lock()
	count = state.counters["worker"]
	state.mu.Unlock()

	if count != 1 {
		t.Errorf("After reset and increment, counter = %d, want 1", count)
	}
}

func TestReminderNodeSpecificConfig(t *testing.T) {
	state := NewReminderState()
	nodes := map[string]discovery.NodeInfo{
		"worker": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout:      5.0,
		ReminderInterval: 2.0, // Global threshold (set to match node-specific)
		ReminderMessage:  "GLOBAL REMINDER",
		Nodes: map[string]config.NodeConfig{
			"worker": {
				ReminderInterval: 2.0, // Node-specific threshold
				ReminderMessage:  "WORKER REMINDER: {count} messages",
			},
		},
	}

	// Increment to node-specific threshold (2)
	state.Increment("worker", nodes, cfg)
	state.Increment("worker", nodes, cfg)

	// After reaching node-specific threshold, counter should be reset
	state.mu.Lock()
	count := state.counters["worker"]
	state.mu.Unlock()

	if count != 0 {
		t.Errorf("After reaching node-specific threshold, counter = %d, want 0", count)
	}
}

func TestReminderThreadSafety(t *testing.T) {
	state := NewReminderState()
	nodes := map[string]discovery.NodeInfo{
		"worker": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout:      5.0,
		ReminderInterval: 101.0, // High threshold to avoid reset during test (> 100)
		ReminderMessage:  "REMINDER",
		Nodes:            map[string]config.NodeConfig{},
	}

	// Concurrent increments
	var wg sync.WaitGroup
	numGoroutines := 10
	incrementsPerGoroutine := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < incrementsPerGoroutine; j++ {
				state.Increment("worker", nodes, cfg)
			}
		}()
	}

	wg.Wait()

	// Verify final count
	state.mu.Lock()
	count := state.counters["worker"]
	state.mu.Unlock()

	expected := numGoroutines * incrementsPerGoroutine
	if count != expected {
		t.Errorf("After concurrent increments, counter = %d, want %d", count, expected)
	}
}

func TestReminderDisabled(t *testing.T) {
	state := NewReminderState()
	nodes := map[string]discovery.NodeInfo{
		"worker": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout:      5.0,
		ReminderInterval: 0.0, // Disabled
		ReminderMessage:  "",
		Nodes:            map[string]config.NodeConfig{},
	}

	// Increment many times
	for i := 0; i < 10; i++ {
		state.Increment("worker", nodes, cfg)
	}

	// Counter should keep incrementing (no reset since reminder is disabled)
	state.mu.Lock()
	count := state.counters["worker"]
	state.mu.Unlock()

	if count != 10 {
		t.Errorf("With reminder disabled, counter = %d, want 10", count)
	}
}
