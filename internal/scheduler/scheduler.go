package scheduler

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Schedule represents a single cron-like schedule entry.
type Schedule struct {
	Cron    string `toml:"cron"`
	To      string `toml:"to"`
	Message string `toml:"message"`
}

// Config represents the schedules.toml file format.
type Config struct {
	Schedules []Schedule `toml:"schedules"`
}

// StateEntry tracks when a schedule was last fired.
type StateEntry struct {
	LastFiredAt time.Time `json:"last_fired_at"`
}

// State tracks all schedule firing timestamps for catch-up on restart.
// Issue #169: persisted to scheduler-state.json for crash recovery.
type State struct {
	Schedules map[string]StateEntry `json:"schedules"`
}

// LoadConfig reads and parses a schedules.toml file.
func LoadConfig(path string) (*Config, error) {
	var cfg Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return nil, fmt.Errorf("parsing schedules config: %w", err)
	}
	return &cfg, nil
}

// LoadState reads the scheduler state file. Returns empty state if not found.
// Issue #169: catch-up logic requires persisted last-fired timestamps.
func LoadState(path string) *State {
	data, err := os.ReadFile(path)
	if err != nil {
		return &State{Schedules: make(map[string]StateEntry)}
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		log.Printf("scheduler: corrupt state file %s, resetting: %v", path, err)
		return &State{Schedules: make(map[string]StateEntry)}
	}
	if state.Schedules == nil {
		state.Schedules = make(map[string]StateEntry)
	}
	return &state
}

// SaveState atomically writes the scheduler state file.
// Uses write-to-tmp + rename pattern to prevent corruption on crash.
func SaveState(path string, state *State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling state: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("writing state tmp: %w", err)
	}
	return os.Rename(tmpPath, path)
}

// StateKey returns the unique key for a schedule in the state file.
func StateKey(s Schedule) string {
	return s.Cron + ":" + s.To
}

// ParseCron parses a 5-field cron expression and returns the next firing time
// after the given reference time. Supports: minute hour day-of-month month day-of-week.
// Wildcards (*) match all values.
func ParseCron(expr string, after time.Time) (time.Time, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return time.Time{}, fmt.Errorf("invalid cron expression %q: expected 5 fields", expr)
	}

	minuteSpec, hourSpec, domSpec, monthSpec, dowSpec := fields[0], fields[1], fields[2], fields[3], fields[4]

	// Start scanning from the next minute after 'after'
	t := after.Truncate(time.Minute).Add(time.Minute)

	// Scan up to 366 days to find the next match
	limit := t.Add(366 * 24 * time.Hour)
	for t.Before(limit) {
		if matchField(monthSpec, int(t.Month())) &&
			matchField(domSpec, t.Day()) &&
			matchField(dowSpec, int(t.Weekday())) &&
			matchField(hourSpec, t.Hour()) &&
			matchField(minuteSpec, t.Minute()) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("no next time found for cron %q within 366 days", expr)
}

// matchField checks if a value matches a cron field specification.
// Supports: "*" (wildcard), single value, comma-separated values.
func matchField(spec string, value int) bool {
	if spec == "*" {
		return true
	}
	for _, part := range strings.Split(spec, ",") {
		var v int
		if _, err := fmt.Sscanf(strings.TrimSpace(part), "%d", &v); err == nil && v == value {
			return true
		}
	}
	return false
}

// Run starts the scheduler loop. It fires missed schedules on startup (catch-up)
// and then ticks every minute to check for due schedules.
// fireFn is called for each schedule that needs to fire.
// Issue #166: v1 basic scheduler. Issue #169: v2 catch-up with state file.
func Run(cfg *Config, statePath string, fireFn func(Schedule) error) error {
	state := LoadState(statePath)
	now := time.Now()

	// Issue #169: Catch-up — fire any schedules that were missed during downtime
	for _, sched := range cfg.Schedules {
		key := StateKey(sched)
		entry, exists := state.Schedules[key]
		if !exists {
			entry = StateEntry{LastFiredAt: time.Time{}}
		}
		nextAfterLast, err := ParseCron(sched.Cron, entry.LastFiredAt)
		if err != nil {
			log.Printf("scheduler: invalid cron %q for %s: %v", sched.Cron, sched.To, err)
			continue
		}
		if !nextAfterLast.After(now) {
			log.Printf("scheduler: catch-up firing %s -> %s", sched.Cron, sched.To)
			if err := fireFn(sched); err != nil {
				log.Printf("scheduler: catch-up fire failed: %v", err)
				continue
			}
			state.Schedules[key] = StateEntry{LastFiredAt: now}
			if err := SaveState(statePath, state); err != nil {
				log.Printf("scheduler: save state failed: %v", err)
			}
		}
	}

	// Main loop: check every minute
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now = time.Now()
		for _, sched := range cfg.Schedules {
			key := StateKey(sched)
			entry := state.Schedules[key]
			nextFire, err := ParseCron(sched.Cron, entry.LastFiredAt)
			if err != nil {
				continue
			}
			if !nextFire.After(now) {
				log.Printf("scheduler: firing %s -> %s", sched.Cron, sched.To)
				if err := fireFn(sched); err != nil {
					log.Printf("scheduler: fire failed: %v", err)
					continue
				}
				state.Schedules[key] = StateEntry{LastFiredAt: now}
				if err := SaveState(statePath, state); err != nil {
					log.Printf("scheduler: save state failed: %v", err)
				}
			}
		}
	}
	return nil
}

// DraftPath returns the path where a scheduler-generated draft should be written.
func DraftPath(baseDir, contextID, sessionName, to string) string {
	ts := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s-from-scheduler-to-%s-%s.md", ts, to, sessionName)
	return filepath.Join(baseDir, contextID, sessionName, "draft", filename)
}
