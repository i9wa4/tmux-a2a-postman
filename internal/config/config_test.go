package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveBaseDir(t *testing.T) {
	t.Run("POSTMAN_HOME priority", func(t *testing.T) {
		t.Setenv("POSTMAN_HOME", "/tmp/custom-postman")
		t.Setenv("XDG_STATE_HOME", "")
		if got := ResolveBaseDir(""); got != "/tmp/custom-postman" {
			t.Errorf("POSTMAN_HOME: got %q, want %q", got, "/tmp/custom-postman")
		}
	})

	t.Run("configBaseDir priority", func(t *testing.T) {
		t.Setenv("POSTMAN_HOME", "")
		t.Setenv("XDG_STATE_HOME", "")
		if got := ResolveBaseDir("/tmp/from-config"); got != "/tmp/from-config" {
			t.Errorf("configBaseDir: got %q, want %q", got, "/tmp/from-config")
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

		if got := ResolveBaseDir(""); got != "/tmp/xdg-state/tmux-a2a-postman" {
			t.Errorf("XDG_STATE_HOME: got %q, want %q", got, "/tmp/xdg-state/tmux-a2a-postman")
		}
	})

	t.Run("fallback to postman (when HOME unavailable)", func(t *testing.T) {
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
		// NOTE: HOME is empty, so UserHomeDir() fails

		if got := ResolveBaseDir(""); got != "tmux-a2a-postman" {
			t.Errorf("fallback: got %q, want %q", got, "tmux-a2a-postman")
		}
	})
}

func TestCreateSessionDirs(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")

	if err := CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs failed: %v", err)
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
[postman]
a2a_version = "1.0"
scan_interval_seconds = 2.0
enter_delay_seconds = 1.0
tmux_timeout_seconds = 10.0
startup_delay_seconds = 3.0
new_node_ping_delay_seconds = 5.0
reminder_interval_seconds = 60.0
base_dir = "/custom/base"
notification_template = "Custom notification: {{.From}}"
ping_template = "Custom ping"
draft_template = "Custom draft"
reminder_message = "Custom reminder"
reply_command = "custom-reply"
edges = ["orchestrator --> worker", "worker --> observer"]

[orchestrator]
template = "orchestrator template"
role = "coordinator"
on_join = ""

[worker]
template = "worker template"
role = "worker"
on_join = ""

[observer]
template = "observer template"
role = "observer"
on_join = ""
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
	if len(cfg.Nodes) != 3 {
		t.Errorf("Nodes length: got %d, want 3", len(cfg.Nodes))
	}
	if cfg.Nodes["orchestrator"].Role != "coordinator" {
		t.Errorf("Node orchestrator role: got %q, want %q", cfg.Nodes["orchestrator"].Role, "coordinator")
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
	if cfg.DraftTemplate != "" {
		t.Errorf("default DraftTemplate: got %q, want empty", cfg.DraftTemplate)
	}
}

func TestLoadConfig_Partial(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := `
[postman]
scan_interval_seconds = 3.0
base_dir = "/partial/base"

edges = ["worker -- orchestrator"]

[worker]
[orchestrator]
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
			name:  "simple bidirectional edge",
			edges: []string{"orchestrator -- worker"},
			want: map[string][]string{
				"orchestrator": {"worker"},
				"worker":       {"orchestrator"},
			},
		},
		{
			name:  "chain syntax (A -- B -- C)",
			edges: []string{"messenger -- orchestrator -- worker"},
			want: map[string][]string{
				"messenger":    {"orchestrator"},
				"orchestrator": {"messenger", "worker"},
				"worker":       {"orchestrator"},
			},
		},
		{
			name: "multiple edges",
			edges: []string{
				"orchestrator -- worker",
				"orchestrator -- observer",
			},
			want: map[string][]string{
				"orchestrator": {"worker", "observer"},
				"worker":       {"orchestrator"},
				"observer":     {"orchestrator"},
			},
		},
		{
			name:  "empty edge (skipped)",
			edges: []string{"", "  ", "orchestrator -- worker"},
			want: map[string][]string{
				"orchestrator": {"worker"},
				"worker":       {"orchestrator"},
			},
		},
		{
			name:    "invalid format (no separator)",
			edges:   []string{"orchestrator worker"},
			wantErr: true,
		},
		{
			name:    "invalid format (empty node)",
			edges:   []string{"orchestrator -- "},
			wantErr: true,
		},
		{
			name:    "invalid format (single node)",
			edges:   []string{"orchestrator"},
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
	configDir := filepath.Join(xdgConfigHome, "tmux-a2a-postman")
	configPath := filepath.Join(configDir, "postman.toml")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	content := `
[postman]
scan_interval_seconds = 5.0

edges = ["worker -- orchestrator"]

[worker]
[orchestrator]
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
[postman]
base_dir = "/custom/postman"

edges = ["worker -- orchestrator"]

[worker]
[orchestrator]
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

	baseDir := ResolveBaseDir(cfg.BaseDir)
	if baseDir != "/custom/postman" {
		t.Errorf("ResolveBaseDir with config.BaseDir: got %q, want %q", baseDir, "/custom/postman")
	}
}

func TestGetTalksTo(t *testing.T) {
	adjacency := map[string][]string{
		"orchestrator": {"worker", "observer"},
		"worker":       {"orchestrator"},
		"observer":     {"orchestrator"},
	}

	tests := []struct {
		name     string
		nodeName string
		want     []string
	}{
		{
			name:     "orchestrator talks to worker and observer",
			nodeName: "orchestrator",
			want:     []string{"worker", "observer"},
		},
		{
			name:     "worker talks to orchestrator",
			nodeName: "worker",
			want:     []string{"orchestrator"},
		},
		{
			name:     "unknown node returns empty",
			nodeName: "unknown",
			want:     []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetTalksTo(adjacency, tt.nodeName)
			if len(got) != len(tt.want) {
				t.Errorf("GetTalksTo() length = %d, want %d", len(got), len(tt.want))
			}
			for i, node := range tt.want {
				if got[i] != node {
					t.Errorf("GetTalksTo()[%d] = %q, want %q", i, got[i], node)
				}
			}
		})
	}
}

func TestLoadConfig_SplitNodes(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "postman.toml")
	nodesDir := filepath.Join(tmpDir, "nodes")

	// Create postman.toml with [postman] section
	mainContent := `
[postman]
scan_interval_seconds = 1.0
`
	if err := os.WriteFile(configPath, []byte(mainContent), 0o644); err != nil {
		t.Fatalf("WriteFile postman.toml failed: %v", err)
	}

	// Create nodes/ directory
	if err := os.MkdirAll(nodesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll nodes failed: %v", err)
	}

	// Create nodes/worker.toml (table header format)
	workerContent := `[worker]
template = "worker template from nodes"
role = "worker"
`
	if err := os.WriteFile(filepath.Join(nodesDir, "worker.toml"), []byte(workerContent), 0o644); err != nil {
		t.Fatalf("WriteFile worker.toml failed: %v", err)
	}

	// Create nodes/orchestrator.toml (table header format)
	orchestratorContent := `[orchestrator]
template = "orchestrator template from nodes"
role = "orchestrator"
`
	if err := os.WriteFile(filepath.Join(nodesDir, "orchestrator.toml"), []byte(orchestratorContent), 0o644); err != nil {
		t.Fatalf("WriteFile orchestrator.toml failed: %v", err)
	}

	// Load config
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Verify nodes from directory are loaded
	if len(cfg.Nodes) != 2 {
		t.Errorf("Nodes length: got %d, want 2", len(cfg.Nodes))
	}
	if cfg.Nodes["worker"].Template != "worker template from nodes" {
		t.Errorf("worker template: got %q, want %q", cfg.Nodes["worker"].Template, "worker template from nodes")
	}
	if cfg.Nodes["orchestrator"].Template != "orchestrator template from nodes" {
		t.Errorf("orchestrator template: got %q, want %q", cfg.Nodes["orchestrator"].Template, "orchestrator template from nodes")
	}
}

func TestLoadConfig_SplitOverride(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "postman.toml")
	nodesDir := filepath.Join(tmpDir, "nodes")

	// Create postman.toml with [worker] section
	mainContent := `
[postman]
scan_interval_seconds = 1.0

[worker]
template = "worker template from main"
role = "worker-main"
`
	if err := os.WriteFile(configPath, []byte(mainContent), 0o644); err != nil {
		t.Fatalf("WriteFile postman.toml failed: %v", err)
	}

	// Create nodes/ directory
	if err := os.MkdirAll(nodesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll nodes failed: %v", err)
	}

	// Create nodes/worker.toml with different values (table header format)
	workerContent := `[worker]
template = "worker template from nodes (override)"
role = "worker-override"
`
	if err := os.WriteFile(filepath.Join(nodesDir, "worker.toml"), []byte(workerContent), 0o644); err != nil {
		t.Fatalf("WriteFile worker.toml failed: %v", err)
	}

	// Load config
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Verify node file overrides main config
	if cfg.Nodes["worker"].Template != "worker template from nodes (override)" {
		t.Errorf("worker template: got %q, want %q", cfg.Nodes["worker"].Template, "worker template from nodes (override)")
	}
	if cfg.Nodes["worker"].Role != "worker-override" {
		t.Errorf("worker role: got %q, want %q", cfg.Nodes["worker"].Role, "worker-override")
	}
}

func TestLoadConfig_SplitReservedSkip(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "postman.toml")
	nodesDir := filepath.Join(tmpDir, "nodes")

	// Create postman.toml with [postman] section
	mainContent := `
[postman]
scan_interval_seconds = 2.0
`
	if err := os.WriteFile(configPath, []byte(mainContent), 0o644); err != nil {
		t.Fatalf("WriteFile postman.toml failed: %v", err)
	}

	// Create nodes/ directory
	if err := os.MkdirAll(nodesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll nodes failed: %v", err)
	}

	// Create nodes/reserved.toml with [postman] and [worker] sections
	reservedContent := `[postman]
scan_interval_seconds = 999.0

[worker]
template = "worker template"
role = "worker"
`
	if err := os.WriteFile(filepath.Join(nodesDir, "reserved.toml"), []byte(reservedContent), 0o644); err != nil {
		t.Fatalf("WriteFile reserved.toml failed: %v", err)
	}

	// Load config
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Verify [postman] section from nodes/ is skipped (reserved)
	if cfg.ScanInterval != 2.0 {
		t.Errorf("ScanInterval: got %v, want 2.0 (postman section should be skipped)", cfg.ScanInterval)
	}
	// Verify [worker] section from nodes/ is loaded
	if cfg.Nodes["worker"].Template != "worker template" {
		t.Errorf("worker template: got %q, want %q", cfg.Nodes["worker"].Template, "worker template")
	}
}

func TestLoadConfig_SplitInvalidFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "postman.toml")
	nodesDir := filepath.Join(tmpDir, "nodes")

	// Create postman.toml
	mainContent := `
[postman]
scan_interval_seconds = 1.0
`
	if err := os.WriteFile(configPath, []byte(mainContent), 0o644); err != nil {
		t.Fatalf("WriteFile postman.toml failed: %v", err)
	}

	// Create nodes/ directory
	if err := os.MkdirAll(nodesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll nodes failed: %v", err)
	}

	// Create nodes/worker.toml (valid, table header format)
	workerContent := `[worker]
template = "worker template"
role = "worker"
`
	if err := os.WriteFile(filepath.Join(nodesDir, "worker.toml"), []byte(workerContent), 0o644); err != nil {
		t.Fatalf("WriteFile worker.toml failed: %v", err)
	}

	// Create nodes/bad.toml (invalid TOML)
	badContent := `invalid toml content [[[`
	if err := os.WriteFile(filepath.Join(nodesDir, "bad.toml"), []byte(badContent), 0o644); err != nil {
		t.Fatalf("WriteFile bad.toml failed: %v", err)
	}

	// Load config (should succeed with warning for bad.toml)
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v (should gracefully handle invalid files)", err)
	}

	// Verify valid worker.toml is loaded
	if len(cfg.Nodes) != 1 {
		t.Errorf("Nodes length: got %d, want 1 (bad.toml should be skipped)", len(cfg.Nodes))
	}
	if cfg.Nodes["worker"].Template != "worker template" {
		t.Errorf("worker template: got %q, want %q", cfg.Nodes["worker"].Template, "worker template")
	}
}

func TestLoadConfig_NoNodesDir(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "postman.toml")

	// Create postman.toml with [worker] section (no nodes/ directory)
	content := `
[postman]
scan_interval_seconds = 1.0

[worker]
template = "worker template from main"
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Load config (should work without nodes/ directory)
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// Verify backward compatibility (main config works)
	if cfg.Nodes["worker"].Template != "worker template from main" {
		t.Errorf("worker template: got %q, want %q", cfg.Nodes["worker"].Template, "worker template from main")
	}
}

func TestResolveNodesDir(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T) string // Returns configPath
		wantExists bool
	}{
		{
			name: "existing nodes directory",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				configPath := filepath.Join(tmpDir, "postman.toml")
				nodesDir := filepath.Join(tmpDir, "nodes")
				if err := os.WriteFile(configPath, []byte("[postman]"), 0o644); err != nil {
					t.Fatalf("WriteFile failed: %v", err)
				}
				if err := os.MkdirAll(nodesDir, 0o755); err != nil {
					t.Fatalf("MkdirAll failed: %v", err)
				}
				return configPath
			},
			wantExists: true,
		},
		{
			name: "non-existing nodes directory",
			setup: func(t *testing.T) string {
				tmpDir := t.TempDir()
				configPath := filepath.Join(tmpDir, "postman.toml")
				if err := os.WriteFile(configPath, []byte("[postman]"), 0o644); err != nil {
					t.Fatalf("WriteFile failed: %v", err)
				}
				// Do not create nodes/ directory
				return configPath
			},
			wantExists: false,
		},
		{
			name: "empty config path",
			setup: func(t *testing.T) string {
				return ""
			},
			wantExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configPath := tt.setup(t)
			nodesDir := ResolveNodesDir(configPath)
			if tt.wantExists {
				if nodesDir == "" {
					t.Errorf("ResolveNodesDir() returned empty, want non-empty path")
				}
			} else {
				if nodesDir != "" {
					t.Errorf("ResolveNodesDir() = %q, want empty", nodesDir)
				}
			}
		})
	}
}

func TestGetTmuxPaneName(t *testing.T) {
	t.Run("TMUX_PANE set uses targeted lookup", func(t *testing.T) {
		tmpDir := t.TempDir()
		argsFile := filepath.Join(tmpDir, "args.txt")
		fakeTmux := filepath.Join(tmpDir, "tmux")
		script := "#!/bin/sh\necho \"$@\" >> " + argsFile + "\necho 'test-pane-title'\n"
		if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		origPath := os.Getenv("PATH")
		t.Setenv("PATH", tmpDir+":"+origPath)
		t.Setenv("TMUX_PANE", "%42")

		got := GetTmuxPaneName()
		if got != "test-pane-title" {
			t.Errorf("GetTmuxPaneName() = %q, want %q", got, "test-pane-title")
		}
		argsData, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("ReadFile args failed: %v", err)
		}
		args := strings.TrimSpace(string(argsData))
		if !strings.Contains(args, "-t") {
			t.Errorf("tmux args %q: want '-t' for targeted path", args)
		}
		if !strings.Contains(args, "%42") {
			t.Errorf("tmux args %q: want '%%42' for targeted path", args)
		}
	})

	t.Run("TMUX_PANE unset uses untargeted fallback", func(t *testing.T) {
		tmpDir := t.TempDir()
		argsFile := filepath.Join(tmpDir, "args.txt")
		fakeTmux := filepath.Join(tmpDir, "tmux")
		script := "#!/bin/sh\necho \"$@\" >> " + argsFile + "\necho 'test-pane-title'\n"
		if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		origPath := os.Getenv("PATH")
		t.Setenv("PATH", tmpDir+":"+origPath)
		t.Setenv("TMUX_PANE", "")

		got := GetTmuxPaneName()
		if got != "test-pane-title" {
			t.Errorf("GetTmuxPaneName() = %q, want %q", got, "test-pane-title")
		}
		argsData, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("ReadFile args failed: %v", err)
		}
		args := strings.TrimSpace(string(argsData))
		if strings.Contains(args, "-t") {
			t.Errorf("tmux args %q: should NOT contain '-t' for untargeted path", args)
		}
	})
}

func TestGetTmuxSessionName(t *testing.T) {
	t.Run("TMUX_PANE set uses targeted lookup", func(t *testing.T) {
		tmpDir := t.TempDir()
		argsFile := filepath.Join(tmpDir, "args.txt")
		fakeTmux := filepath.Join(tmpDir, "tmux")
		script := "#!/bin/sh\necho \"$@\" >> " + argsFile + "\necho 'test-session'\n"
		if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		origPath := os.Getenv("PATH")
		t.Setenv("PATH", tmpDir+":"+origPath)
		t.Setenv("TMUX_PANE", "%7")

		got := GetTmuxSessionName()
		if got != "test-session" {
			t.Errorf("GetTmuxSessionName() = %q, want %q", got, "test-session")
		}
		argsData, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("ReadFile args failed: %v", err)
		}
		args := strings.TrimSpace(string(argsData))
		if !strings.Contains(args, "-t") {
			t.Errorf("tmux args %q: want '-t' for targeted path", args)
		}
		if !strings.Contains(args, "%7") {
			t.Errorf("tmux args %q: want '%%7' for targeted path", args)
		}
	})

	t.Run("TMUX_PANE unset uses untargeted fallback", func(t *testing.T) {
		tmpDir := t.TempDir()
		argsFile := filepath.Join(tmpDir, "args.txt")
		fakeTmux := filepath.Join(tmpDir, "tmux")
		script := "#!/bin/sh\necho \"$@\" >> " + argsFile + "\necho 'test-session'\n"
		if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		origPath := os.Getenv("PATH")
		t.Setenv("PATH", tmpDir+":"+origPath)
		t.Setenv("TMUX_PANE", "")

		got := GetTmuxSessionName()
		if got != "test-session" {
			t.Errorf("GetTmuxSessionName() = %q, want %q", got, "test-session")
		}
		argsData, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("ReadFile args failed: %v", err)
		}
		args := strings.TrimSpace(string(argsData))
		if strings.Contains(args, "-t") {
			t.Errorf("tmux args %q: should NOT contain '-t' for untargeted path", args)
		}
	})
}

func TestResolveProjectLocalConfig_FoundInCWD(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".tmux-a2a-postman")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	configPath := filepath.Join(configDir, "postman.toml")
	if err := os.WriteFile(configPath, []byte("[postman]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	got, err := resolveProjectLocalConfig(tmpDir, "")
	if err != nil {
		t.Fatalf("resolveProjectLocalConfig failed: %v", err)
	}
	if got != configPath {
		t.Errorf("resolveProjectLocalConfig = %q, want %q", got, configPath)
	}
}

func TestResolveProjectLocalConfig_FoundInParent(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "sub", "nested")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("MkdirAll subDir failed: %v", err)
	}
	configDir := filepath.Join(tmpDir, ".tmux-a2a-postman")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll configDir failed: %v", err)
	}
	configPath := filepath.Join(configDir, "postman.toml")
	if err := os.WriteFile(configPath, []byte("[postman]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	got, err := resolveProjectLocalConfig(subDir, "")
	if err != nil {
		t.Fatalf("resolveProjectLocalConfig failed: %v", err)
	}
	if got != configPath {
		t.Errorf("resolveProjectLocalConfig = %q, want %q", got, configPath)
	}
}

func TestResolveProjectLocalConfig_StopsBeforeHome(t *testing.T) {
	tmpDir := t.TempDir()
	fakeHome := filepath.Join(tmpDir, "home")
	subDir := filepath.Join(fakeHome, "project")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("MkdirAll subDir failed: %v", err)
	}
	// Config is inside home — walk should stop before reaching it
	configDir := filepath.Join(fakeHome, ".tmux-a2a-postman")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll configDir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "postman.toml"), []byte("[postman]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	t.Setenv("HOME", fakeHome)

	got, err := resolveProjectLocalConfig(subDir, "")
	if err != nil {
		t.Fatalf("resolveProjectLocalConfig failed: %v", err)
	}
	if got != "" {
		t.Errorf("resolveProjectLocalConfig = %q, want empty (should stop before home)", got)
	}
}

func TestResolveProjectLocalConfig_SkipsXDGDuplicate(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".tmux-a2a-postman")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	configPath := filepath.Join(configDir, "postman.toml")
	if err := os.WriteFile(configPath, []byte("[postman]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Same path passed as xdgPath — should be skipped
	got, err := resolveProjectLocalConfig(tmpDir, configPath)
	if err != nil {
		t.Fatalf("resolveProjectLocalConfig failed: %v", err)
	}
	if got != "" {
		t.Errorf("resolveProjectLocalConfig = %q, want empty (XDG duplicate should be skipped)", got)
	}
}

func TestResolveProjectLocalConfig_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir) // walk stops before tmpDir (== home)

	subDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	got, err := resolveProjectLocalConfig(subDir, "")
	if err != nil {
		t.Fatalf("resolveProjectLocalConfig failed: %v", err)
	}
	if got != "" {
		t.Errorf("resolveProjectLocalConfig = %q, want empty", got)
	}
}

func TestMergeConfig_ScalarOverride(t *testing.T) {
	base := DefaultConfig()
	base.ScanInterval = 1.0
	base.BaseDir = ""
	base.PingMode = "all"

	override := &Config{
		Nodes:        make(map[string]NodeConfig),
		ScanInterval: 5.0,
		BaseDir:      "/project/base",
		PingMode:     "disabled",
	}

	mergeConfig(base, override)

	if base.ScanInterval != 5.0 {
		t.Errorf("ScanInterval: got %v, want 5.0", base.ScanInterval)
	}
	if base.BaseDir != "/project/base" {
		t.Errorf("BaseDir: got %q, want %q", base.BaseDir, "/project/base")
	}
	if base.PingMode != "disabled" {
		t.Errorf("PingMode: got %q, want %q", base.PingMode, "disabled")
	}
	// Unset override field should not change base
	if base.EnterDelay != 0.5 {
		t.Errorf("EnterDelay: got %v, want 0.5 (unset override should not change base)", base.EnterDelay)
	}
}

func TestMergeConfig_NodeMerge(t *testing.T) {
	base := DefaultConfig()
	base.Nodes = map[string]NodeConfig{
		"worker": {Template: "base template", Role: "worker"},
	}

	override := &Config{
		Nodes: map[string]NodeConfig{
			"worker": {Role: "worker-override"},
			"new":    {Template: "new template", Role: "new-role"},
		},
	}

	mergeConfig(base, override)

	// worker.Template unchanged (override field is zero)
	if base.Nodes["worker"].Template != "base template" {
		t.Errorf("worker.Template: got %q, want %q", base.Nodes["worker"].Template, "base template")
	}
	// worker.Role overridden
	if base.Nodes["worker"].Role != "worker-override" {
		t.Errorf("worker.Role: got %q, want %q", base.Nodes["worker"].Role, "worker-override")
	}
	// New node added
	if base.Nodes["new"].Template != "new template" {
		t.Errorf("new.Template: got %q, want %q", base.Nodes["new"].Template, "new template")
	}
}

func TestLoadConfig_ProjectLocal_Only(t *testing.T) {
	tmpDir := t.TempDir()
	fakeHome := filepath.Join(tmpDir, "home")
	subDir := filepath.Join(fakeHome, "project")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("MkdirAll subDir failed: %v", err)
	}

	localConfigDir := filepath.Join(subDir, ".tmux-a2a-postman")
	if err := os.MkdirAll(localConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll localConfigDir failed: %v", err)
	}
	localConfig := `
[postman]
scan_interval_seconds = 7.0
base_dir = "/project/data"
edges = ["worker -- orchestrator"]

[worker]
role = "worker"

[orchestrator]
role = "orchestrator"
`
	if err := os.WriteFile(filepath.Join(localConfigDir, "postman.toml"), []byte(localConfig), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", "/nonexistent")
	t.Setenv("HOME", fakeHome)

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(subDir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	if cfg.ScanInterval != 7.0 {
		t.Errorf("ScanInterval: got %v, want 7.0", cfg.ScanInterval)
	}
	if cfg.BaseDir != "/project/data" {
		t.Errorf("BaseDir: got %q, want %q", cfg.BaseDir, "/project/data")
	}
	// Default fields should come from embedded defaults
	if cfg.EnterDelay != 0.5 {
		t.Errorf("EnterDelay: got %v, want 0.5 (from embedded defaults)", cfg.EnterDelay)
	}
	if len(cfg.Nodes) != 2 {
		t.Errorf("Nodes length: got %d, want 2", len(cfg.Nodes))
	}
}

func TestLoadConfig_ProjectLocal_Overrides_XDG(t *testing.T) {
	tmpDir := t.TempDir()
	fakeHome := filepath.Join(tmpDir, "home")

	// Create XDG config
	xdgConfigHome := filepath.Join(tmpDir, "xdg")
	xdgConfigDir := filepath.Join(xdgConfigHome, "tmux-a2a-postman")
	if err := os.MkdirAll(xdgConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll xdgConfigDir failed: %v", err)
	}
	xdgConfig := `
[postman]
scan_interval_seconds = 2.0
enter_delay_seconds = 1.0
edges = ["worker -- orchestrator"]

[worker]
role = "worker"

[orchestrator]
role = "orchestrator"
`
	if err := os.WriteFile(filepath.Join(xdgConfigDir, "postman.toml"), []byte(xdgConfig), 0o644); err != nil {
		t.Fatalf("WriteFile XDG config failed: %v", err)
	}

	// Create project-local config in subDir
	subDir := filepath.Join(fakeHome, "project")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("MkdirAll subDir failed: %v", err)
	}
	localConfigDir := filepath.Join(subDir, ".tmux-a2a-postman")
	if err := os.MkdirAll(localConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll localConfigDir failed: %v", err)
	}
	localConfig := `
[postman]
scan_interval_seconds = 9.0
`
	if err := os.WriteFile(filepath.Join(localConfigDir, "postman.toml"), []byte(localConfig), 0o644); err != nil {
		t.Fatalf("WriteFile local config failed: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)
	t.Setenv("HOME", fakeHome)

	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(subDir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	// project-local overrides XDG scan_interval
	if cfg.ScanInterval != 9.0 {
		t.Errorf("ScanInterval: got %v, want 9.0 (project-local override)", cfg.ScanInterval)
	}
	// XDG enter_delay not overridden by local
	if cfg.EnterDelay != 1.0 {
		t.Errorf("EnterDelay: got %v, want 1.0 (from XDG, not overridden)", cfg.EnterDelay)
	}
	// XDG nodes still present
	if len(cfg.Nodes) != 2 {
		t.Errorf("Nodes length: got %d, want 2", len(cfg.Nodes))
	}
}
