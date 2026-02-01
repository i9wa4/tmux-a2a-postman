package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveBaseDir(t *testing.T) {
	t.Run("POSTMAN_HOME priority", func(t *testing.T) {
		t.Setenv("POSTMAN_HOME", "/tmp/custom-postman")
		t.Setenv("XDG_STATE_HOME", "")
		if got := resolveBaseDir(""); got != "/tmp/custom-postman" {
			t.Errorf("POSTMAN_HOME: got %q, want %q", got, "/tmp/custom-postman")
		}
	})

	t.Run("configBaseDir priority", func(t *testing.T) {
		t.Setenv("POSTMAN_HOME", "")
		t.Setenv("XDG_STATE_HOME", "")
		if got := resolveBaseDir("/tmp/from-config"); got != "/tmp/from-config" {
			t.Errorf("configBaseDir: got %q, want %q", got, "/tmp/from-config")
		}
	})

	t.Run("backward compat: .postman exists", func(t *testing.T) {
		t.Setenv("POSTMAN_HOME", "")
		t.Setenv("XDG_STATE_HOME", "")
		tmpDir := t.TempDir()
		origWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd failed: %v", err)
		}
		defer func() { _ = os.Chdir(origWd) }()

		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}
		if err := os.Mkdir(".postman", 0o755); err != nil {
			t.Fatalf("Mkdir failed: %v", err)
		}

		if got := resolveBaseDir(""); got != ".postman" {
			t.Errorf(".postman exists: got %q, want %q", got, ".postman")
		}
	})

	t.Run("XDG_STATE_HOME", func(t *testing.T) {
		t.Setenv("POSTMAN_HOME", "")
		t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state")
		tmpDir := t.TempDir()
		origWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd failed: %v", err)
		}
		defer func() { _ = os.Chdir(origWd) }()

		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}
		// NOTE: .postman does NOT exist in CWD

		if got := resolveBaseDir(""); got != "/tmp/xdg-state/postman" {
			t.Errorf("XDG_STATE_HOME: got %q, want %q", got, "/tmp/xdg-state/postman")
		}
	})

	t.Run("fallback to .postman", func(t *testing.T) {
		t.Setenv("POSTMAN_HOME", "")
		t.Setenv("XDG_STATE_HOME", "")
		t.Setenv("HOME", "")
		tmpDir := t.TempDir()
		origWd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Getwd failed: %v", err)
		}
		defer func() { _ = os.Chdir(origWd) }()

		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("Chdir failed: %v", err)
		}
		// NOTE: .postman does NOT exist in CWD, HOME is empty

		if got := resolveBaseDir(""); got != ".postman" {
			t.Errorf("fallback: got %q, want %q", got, ".postman")
		}
	})
}

func TestCreateSessionDirs(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")

	if err := createSessionDirs(sessionDir); err != nil {
		t.Fatalf("createSessionDirs failed: %v", err)
	}

	expectedDirs := []string{"inbox", "post", "draft", "read", "dead-letter"}
	for _, d := range expectedDirs {
		path := filepath.Join(sessionDir, d)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("directory %q not created: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", d)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := `
scan_interval_seconds = 2.0
enter_delay_seconds = 1.0
tmux_timeout_seconds = 10.0
startup_delay_seconds = 3.0
new_node_ping_delay_seconds = 5.0
reminder_interval_seconds = 60.0
base_dir = "/custom/base"
notification_template = "Custom notification: {{.From}}"
ping_template = "Custom ping"
digest_template = "Custom digest"
draft_template = "Custom draft"
reminder_message = "Custom reminder"
reply_command = "custom-reply"
edges = ["orchestrator -> worker", "worker <-> observer"]

[node.orchestrator]
template = "orchestrator template"
role = "coordinator"

[node.worker]
template = "worker template"
subscribe_digest = true
`

	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.ScanInterval != 2.0 {
		t.Errorf("ScanInterval: got %v, want 2.0", cfg.ScanInterval)
	}
	if cfg.EnterDelay != 1.0 {
		t.Errorf("EnterDelay: got %v, want 1.0", cfg.EnterDelay)
	}
	if cfg.TmuxTimeout != 10.0 {
		t.Errorf("TmuxTimeout: got %v, want 10.0", cfg.TmuxTimeout)
	}
	if cfg.StartupDelay != 3.0 {
		t.Errorf("StartupDelay: got %v, want 3.0", cfg.StartupDelay)
	}
	if cfg.NewNodePingDelay != 5.0 {
		t.Errorf("NewNodePingDelay: got %v, want 5.0", cfg.NewNodePingDelay)
	}
	if cfg.ReminderInterval != 60.0 {
		t.Errorf("ReminderInterval: got %v, want 60.0", cfg.ReminderInterval)
	}
	if cfg.BaseDir != "/custom/base" {
		t.Errorf("BaseDir: got %q, want %q", cfg.BaseDir, "/custom/base")
	}
	if cfg.NotificationTemplate != "Custom notification: {{.From}}" {
		t.Errorf("NotificationTemplate: got %q, want %q", cfg.NotificationTemplate, "Custom notification: {{.From}}")
	}
	if cfg.PingTemplate != "Custom ping" {
		t.Errorf("PingTemplate: got %q, want %q", cfg.PingTemplate, "Custom ping")
	}
	if cfg.DigestTemplate != "Custom digest" {
		t.Errorf("DigestTemplate: got %q, want %q", cfg.DigestTemplate, "Custom digest")
	}
	if cfg.DraftTemplate != "Custom draft" {
		t.Errorf("DraftTemplate: got %q, want %q", cfg.DraftTemplate, "Custom draft")
	}
	if cfg.ReminderMessage != "Custom reminder" {
		t.Errorf("ReminderMessage: got %q, want %q", cfg.ReminderMessage, "Custom reminder")
	}
	if cfg.ReplyCommand != "custom-reply" {
		t.Errorf("ReplyCommand: got %q, want %q", cfg.ReplyCommand, "custom-reply")
	}
	if len(cfg.Edges) != 2 {
		t.Errorf("Edges length: got %d, want 2", len(cfg.Edges))
	}
	if len(cfg.Nodes) != 2 {
		t.Errorf("Nodes length: got %d, want 2", len(cfg.Nodes))
	}
	if cfg.Nodes["orchestrator"].Role != "coordinator" {
		t.Errorf("Node orchestrator role: got %q, want %q", cfg.Nodes["orchestrator"].Role, "coordinator")
	}
	if !cfg.Nodes["worker"].SubscribeDigest {
		t.Errorf("Node worker subscribe_digest: got false, want true")
	}
}

func TestLoadConfig_Default(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.toml")
	if err == nil {
		t.Fatal("expected error for explicit non-existent path, got nil")
	}

	// Empty path should return defaults if no fallback file exists
	t.Setenv("XDG_CONFIG_HOME", "/nonexistent")
	t.Setenv("HOME", "/nonexistent")
	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig with empty path failed: %v", err)
	}

	if cfg.ScanInterval != 1.0 {
		t.Errorf("default ScanInterval: got %v, want 1.0", cfg.ScanInterval)
	}
	if cfg.NotificationTemplate != "Message from {sender}" {
		t.Errorf("default NotificationTemplate: got %q, want %q", cfg.NotificationTemplate, "Message from {sender}")
	}
	if cfg.BaseDir != "" {
		t.Errorf("default BaseDir: got %q, want empty", cfg.BaseDir)
	}
	if cfg.DigestTemplate != "" {
		t.Errorf("default DigestTemplate: got %q, want empty", cfg.DigestTemplate)
	}
	if cfg.DraftTemplate != "" {
		t.Errorf("default DraftTemplate: got %q, want empty", cfg.DraftTemplate)
	}
}

func TestLoadConfig_Partial(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := `
scan_interval_seconds = 3.0
base_dir = "/partial/base"
`

	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Explicitly set fields
	if cfg.ScanInterval != 3.0 {
		t.Errorf("ScanInterval: got %v, want 3.0", cfg.ScanInterval)
	}
	if cfg.BaseDir != "/partial/base" {
		t.Errorf("BaseDir: got %q, want %q", cfg.BaseDir, "/partial/base")
	}

	// Default fields (not set in TOML)
	if cfg.EnterDelay != 0.5 {
		t.Errorf("default EnterDelay: got %v, want 0.5", cfg.EnterDelay)
	}
	if cfg.NotificationTemplate != "Message from {sender}" {
		t.Errorf("default NotificationTemplate: got %q, want %q", cfg.NotificationTemplate, "Message from {sender}")
	}
}

func TestParseEdges(t *testing.T) {
	tests := []struct {
		name    string
		edges   []string
		want    map[string][]string
		wantErr bool
	}{
		{
			name:  "one-way edge",
			edges: []string{"orchestrator -> worker"},
			want: map[string][]string{
				"orchestrator": {"worker"},
			},
		},
		{
			name:  "bidirectional edge",
			edges: []string{"worker <-> observer"},
			want: map[string][]string{
				"worker":   {"observer"},
				"observer": {"worker"},
			},
		},
		{
			name:  "mixed edges",
			edges: []string{"orchestrator -> worker", "worker <-> observer"},
			want: map[string][]string{
				"orchestrator": {"worker"},
				"worker":       {"observer"},
				"observer":     {"worker"},
			},
		},
		{
			name:  "empty edge (skipped)",
			edges: []string{"", "  ", "orchestrator -> worker"},
			want: map[string][]string{
				"orchestrator": {"worker"},
			},
		},
		{
			name:    "invalid format (no arrow)",
			edges:   []string{"orchestrator worker"},
			wantErr: true,
		},
		{
			name:    "invalid format (empty node)",
			edges:   []string{"orchestrator -> "},
			wantErr: true,
		},
		{
			name:    "invalid format (multiple arrows)",
			edges:   []string{"a -> b -> c"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEdges(tt.edges)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseEdges() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if len(got) != len(tt.want) {
				t.Errorf("ParseEdges() result length = %d, want %d", len(got), len(tt.want))
			}
			for k, v := range tt.want {
				if len(got[k]) != len(v) {
					t.Errorf("ParseEdges() result[%q] length = %d, want %d", k, len(got[k]), len(v))
				}
				for i, node := range v {
					if got[k][i] != node {
						t.Errorf("ParseEdges() result[%q][%d] = %q, want %q", k, i, got[k][i], node)
					}
				}
			}
		})
	}
}

func TestConfig_Fallback(t *testing.T) {
	tmpDir := t.TempDir()
	xdgConfigHome := filepath.Join(tmpDir, "xdg-config")
	configDir := filepath.Join(xdgConfigHome, "postman")
	configPath := filepath.Join(configDir, "config.toml")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	content := `
scan_interval_seconds = 5.0
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)
	t.Setenv("HOME", "/nonexistent")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.ScanInterval != 5.0 {
		t.Errorf("ScanInterval: got %v, want 5.0", cfg.ScanInterval)
	}
}

func TestLoadConfig_BaseDir(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := `
base_dir = "/custom/postman"
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	t.Setenv("POSTMAN_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")

	baseDir := resolveBaseDir(cfg.BaseDir)
	if baseDir != "/custom/postman" {
		t.Errorf("resolveBaseDir with config.BaseDir: got %q, want %q", baseDir, "/custom/postman")
	}
}
