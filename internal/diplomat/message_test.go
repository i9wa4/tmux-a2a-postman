package diplomat

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDiplomatRef_SharedDir(t *testing.T) {
	tests := []struct {
		name      string
		contextID string
		baseDir   string
		want      string
	}{
		{
			name:      "basic path",
			contextID: "ctx-001",
			baseDir:   "/home/user/.local/state/tmux-a2a-postman",
			want:      "/home/user/.local/state/tmux-a2a-postman/diplomat/ctx-001",
		},
		{
			name:      "relative base dir",
			contextID: "session-abc",
			baseDir:   "data",
			want:      filepath.Join("data", "diplomat", "session-abc"),
		},
		{
			name:      "empty context ID",
			contextID: "",
			baseDir:   "/tmp",
			want:      "/tmp/diplomat",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ref := DiplomatRef{ContextID: tt.contextID, BaseDir: tt.baseDir}
			got := ref.SharedDir()
			if got != tt.want {
				t.Errorf("SharedDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContextMessage_Fields(t *testing.T) {
	now := time.Now()
	msg := ContextMessage{
		From:      "orchestrator",
		To:        "worker",
		Timestamp: now,
		Body:      "hello",
	}
	if msg.From != "orchestrator" {
		t.Errorf("From = %q, want %q", msg.From, "orchestrator")
	}
	if msg.To != "worker" {
		t.Errorf("To = %q, want %q", msg.To, "worker")
	}
	if !msg.Timestamp.Equal(now) {
		t.Errorf("Timestamp = %v, want %v", msg.Timestamp, now)
	}
	if msg.Body != "hello" {
		t.Errorf("Body = %q, want %q", msg.Body, "hello")
	}
}
