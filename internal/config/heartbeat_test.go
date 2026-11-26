package config

import "testing"

func TestParseHeartbeatDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, source, every, body string
		fail                      bool
	}{
		{"absent", "---\nname: team\n---\n", "", "", false},
		{"other daemon default", "---\ndaemon:\n  defaults:\n    github-sync:\n      every: 5s\n---\n", "", "", false},
		{"valid", "---\ndaemon:\n  defaults:\n    heartbeat:\n      every: 30m\n      body: |\n        first\n        second\n---\n", "30m", "first\nsecond\n", false},
		{"minimum", "---\ndaemon:\n  defaults:\n    heartbeat:\n      every: 1s\n      body: hello\n---\n", "1s", "hello", false},
		{"bad interval", "---\ndaemon:\n  defaults:\n    heartbeat:\n      every: 0s\n      body: hello\n---\n", "", "", true},
		{"subsecond interval", "---\ndaemon:\n  defaults:\n    heartbeat:\n      every: 1ms\n      body: hello\n---\n", "", "", true},
		{"missing body", "---\ndaemon:\n  defaults:\n    heartbeat:\n      every: 1m\n---\n", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			every, body, err := parseHeartbeatDefaults(tc.source)
			if (err != nil) != tc.fail || (!tc.fail && (every != tc.every || body != tc.body)) {
				t.Fatalf("every=%q body=%q err=%v", every, body, err)
			}
		})
	}
}
