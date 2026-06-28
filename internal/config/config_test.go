package config

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
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

func TestCreateMultiSessionDirs(t *testing.T) {
	tmpDir := t.TempDir()
	contextDir := filepath.Join(tmpDir, "ctx-123")
	sessionName := "test-session"

	if err := CreateMultiSessionDirs(contextDir, sessionName); err != nil {
		t.Fatalf("CreateMultiSessionDirs failed: %v", err)
	}

	sessionDir := filepath.Join(contextDir, sessionName)
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
	t.Chdir(tmpDir)                     // Isolate from ambient workspace config
	t.Setenv("HOME", tmpDir)            // Isolate from ~/.config/
	t.Setenv("XDG_CONFIG_HOME", tmpDir) // Isolate from XDG postman.md
	configPath := filepath.Join(tmpDir, "config.toml")

	content := `
[postman]
scan_interval_seconds = 2.0
session_scan_interval_seconds = 0.25
enter_delay_seconds = 1.0
tmux_timeout_seconds = 10.0
base_dir = "/custom/base"
notification_template = "Custom notification: {{.From}}"
daemon_message_template = "Custom daemon"
draft_template = "Custom draft"
reply_command = "custom-reply"
edges = ["orchestrator --- worker", "worker --- observer"]

[orchestrator]
template = "orchestrator template"
role = "coordinator"

[worker]
template = "worker template"
role = "worker"

[observer]
template = "observer template"
role = "observer"
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
	if cfg.SessionScanInterval != 0.25 {
		t.Errorf("SessionScanInterval: got %v, want 0.25", cfg.SessionScanInterval)
	}
	if cfg.EnterDelay != 1.0 {
		t.Errorf("EnterDelay: got %v, want 1.0", cfg.EnterDelay)
	}
	if cfg.TmuxTimeout != 10.0 {
		t.Errorf("TmuxTimeout: got %v, want 10.0", cfg.TmuxTimeout)
	}
	if cfg.BaseDir != "/custom/base" {
		t.Errorf("BaseDir: got %q, want %q", cfg.BaseDir, "/custom/base")
	}
	if cfg.NotificationTemplate != "Custom notification: {{.From}}" {
		t.Errorf("NotificationTemplate: got %q, want %q", cfg.NotificationTemplate, "Custom notification: {{.From}}")
	}
	if cfg.DaemonMessageTemplate != "Custom daemon" {
		t.Errorf("DaemonMessageTemplate: got %q, want %q", cfg.DaemonMessageTemplate, "Custom daemon")
	}
	if cfg.DraftTemplate != "Custom draft" {
		t.Errorf("DraftTemplate: got %q, want %q", cfg.DraftTemplate, "Custom draft")
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

func TestLoadConfig_EdgesOnlyTOMLMaterializesNodes(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")

	content := `
[postman]
edges = [
  "messenger --- orchestrator",
  "orchestrator --- worker --- critic",
]
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	wantOrder := []string{"messenger", "orchestrator", "worker", "critic"}
	if got := cfg.OrderedNodeNames(); strings.Join(got, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("OrderedNodeNames = %v, want %v", got, wantOrder)
	}
	for _, name := range wantOrder {
		if _, ok := cfg.Nodes[name]; !ok {
			t.Fatalf("missing materialized node %q in %#v", name, cfg.Nodes)
		}
	}
}

func TestLoadConfig_CommandApprovalPolicies(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")

	content := `
[postman]
base_dir = "/tmp/postman-state"
edges = ["worker --- orchestrator"]

[[postman.command_approval]]
requester = "worker"
label = "nix-build"
category = "verification"
reviewer = "orchestrator"
mode = "blocking"
approval_ttl_seconds = 900
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.CommandApproval) != 1 {
		t.Fatalf("CommandApproval length = %d, want 1", len(cfg.CommandApproval))
	}
	got := cfg.CommandApproval[0]
	if got.Requester != "worker" || got.Label != "nix-build" || got.Category != "verification" {
		t.Fatalf("CommandApproval match fields = %#v", got)
	}
	if got.Reviewer != "orchestrator" || got.Mode != "blocking" || got.ApprovalTTLSeconds != 900 {
		t.Fatalf("CommandApproval policy fields = %#v", got)
	}
}

func TestLoadConfig_XDGMarkdownEdgesOnlyMaterializesNodes(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configDir := filepath.Join(tmpDir, "tmux-a2a-postman")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	content := `## ` + "`edges`" + `

` + "```mermaid" + `
graph LR
    messenger --- orchestrator
    orchestrator --- worker
    orchestrator --- worker-alt
    orchestrator --- critic
    orchestrator --- boss
    guardian --- critic
    orchestrator --- agent
` + "```" + `
`
	if err := os.WriteFile(filepath.Join(configDir, "postman.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	wantEdges := []string{
		"messenger --- orchestrator",
		"orchestrator --- worker",
		"orchestrator --- worker-alt",
		"orchestrator --- critic",
		"orchestrator --- boss",
		"guardian --- critic",
		"orchestrator --- agent",
	}
	if strings.Join(cfg.Edges, "\n") != strings.Join(wantEdges, "\n") {
		t.Fatalf("Edges = %v, want %v", cfg.Edges, wantEdges)
	}

	wantOrder := []string{"messenger", "orchestrator", "worker", "worker-alt", "critic", "boss", "guardian", "agent"}
	if got := cfg.OrderedNodeNames(); strings.Join(got, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("OrderedNodeNames = %v, want %v", got, wantOrder)
	}
	for _, name := range wantOrder {
		if _, ok := cfg.Nodes[name]; !ok {
			t.Fatalf("missing materialized node %q in %#v", name, cfg.Nodes)
		}
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
	if cfg.SessionScanInterval != 0.1 {
		t.Errorf("default SessionScanInterval: got %v, want 0.1", cfg.SessionScanInterval)
	}
	if cfg.PaneCaptureIntervalSeconds != 5.0 {
		t.Errorf("default PaneCaptureIntervalSeconds: got %v, want 5.0", cfg.PaneCaptureIntervalSeconds)
	}
	if cfg.PaneCaptureTailLines != 100 {
		t.Errorf("default PaneCaptureTailLines: got %v, want 100", cfg.PaneCaptureTailLines)
	}
	if !strings.HasPrefix(cfg.NotificationTemplate, "Hello, {node}!") {
		t.Errorf("default NotificationTemplate: got %q, want prefix Hello, {node}!", cfg.NotificationTemplate)
	}
	if cfg.UINode != "messenger" {
		t.Errorf("default UINode: got %q, want %q", cfg.UINode, "messenger")
	}
	if cfg.HasExplicitUINodeSetting() {
		t.Error("default UINode should not be treated as an explicit operator setting")
	}
	if cfg.BaseDir != "" {
		t.Errorf("default BaseDir: got %q, want empty", cfg.BaseDir)
	}
	if !strings.Contains(cfg.DaemonMessageTemplate, "You can talk to:\n{contacts_section}") {
		t.Errorf("default DaemonMessageTemplate missing role-aware contacts section: %q", cfg.DaemonMessageTemplate)
	}
	if !strings.HasPrefix(cfg.DraftTemplate, "---\n") {
		t.Errorf("default DraftTemplate: got %q, want YAML frontmatter prefix", cfg.DraftTemplate)
	}
	if cfg.RetentionPeriodDays != 30 {
		t.Errorf("default RetentionPeriodDays: got %d, want 30", cfg.RetentionPeriodDays)
	}
	if cfg.AutoPingDelaySeconds != 20.0 {
		t.Errorf("default AutoPingDelaySeconds: got %v, want 20.0", cfg.AutoPingDelaySeconds)
	}
	if cfg.DaemonSubmitWorkerLimit != DefaultDaemonSubmitWorkerLimit {
		t.Errorf("default DaemonSubmitWorkerLimit: got %d, want %d", cfg.DaemonSubmitWorkerLimit, DefaultDaemonSubmitWorkerLimit)
	}
	if cfg.AutoEnableNewSessions == nil || !*cfg.AutoEnableNewSessions {
		t.Errorf("default AutoEnableNewSessions: got %v, want true", cfg.AutoEnableNewSessions)
	}
	if cfg.NodeDefaults.EnterCount != 2 {
		t.Errorf("NodeDefaults.EnterCount: got %v, want 2", cfg.NodeDefaults.EnterCount)
	}
}

func TestLoadConfig_DaemonSubmitWorkerLimit(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	configPath := filepath.Join(tmpDir, "postman.toml")

	content := `
[postman]
daemon_submit_worker_limit = 12
edges = ["orchestrator --- worker"]

[orchestrator]
role = "coordinator"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.DaemonSubmitWorkerLimit != 12 {
		t.Fatalf("DaemonSubmitWorkerLimit = %d, want 12", cfg.DaemonSubmitWorkerLimit)
	}
}

func TestEffectiveDaemonSubmitWorkerLimit(t *testing.T) {
	tests := []struct {
		name       string
		configured int
		want       int
		wantWarn   bool
	}{
		{name: "default for zero", configured: 0, want: DefaultDaemonSubmitWorkerLimit, wantWarn: true},
		{name: "default for negative", configured: -1, want: DefaultDaemonSubmitWorkerLimit, wantWarn: true},
		{name: "configured", configured: 12, want: 12},
		{name: "clamped max", configured: 99, want: MaxDaemonSubmitWorkerLimit, wantWarn: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, warning := EffectiveDaemonSubmitWorkerLimit(tt.configured)
			if got != tt.want {
				t.Fatalf("EffectiveDaemonSubmitWorkerLimit(%d) = %d, want %d", tt.configured, got, tt.want)
			}
			if (warning != "") != tt.wantWarn {
				t.Fatalf("warning = %q, wantWarn %v", warning, tt.wantWarn)
			}
		})
	}
}

func TestDefaultConfigOnlyInitializesStructuralFields(t *testing.T) {
	got := DefaultConfig()
	want := &Config{
		Edges:                   []string{},
		Nodes:                   map[string]NodeConfig{},
		NodeOrder:               []string{},
		PingSkillCatalogs:       map[string]string{},
		CompactionSkillCatalogs: map[string]string{},
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultConfig() = %#v, want structural-only %#v", got, want)
	}
}

func TestEmbeddedNonZeroDefaultsAreDeclaredInTOML(t *testing.T) {
	var rootSections map[string]toml.Primitive
	md, err := toml.Decode(string(defaultConfigBytes), &rootSections)
	if err != nil {
		t.Fatalf("Decode embedded defaults: %v", err)
	}
	cfg, err := loadEmbeddedConfig()
	if err != nil {
		t.Fatalf("loadEmbeddedConfig: %v", err)
	}

	assertNonZeroTOMLTaggedFieldsDeclared(t, "postman", *cfg, md)
	assertNonZeroTOMLTaggedFieldsDeclared(t, "node_defaults", cfg.NodeDefaults, md)
}

func assertNonZeroTOMLTaggedFieldsDeclared(t *testing.T, section string, value any, md toml.MetaData) {
	t.Helper()
	rv := reflect.ValueOf(value)
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if field.PkgPath != "" {
			continue
		}
		tomlKey := field.Tag.Get("toml")
		if tomlKey == "" || tomlKey == "-" {
			continue
		}
		tomlKey, _, _ = strings.Cut(tomlKey, ",")
		if rv.Field(i).IsZero() {
			continue
		}
		if !tomlHasField(md, section, tomlKey) {
			t.Fatalf("%s.%s has non-zero embedded default but no [%s].%s key in postman.default.toml", section, field.Name, section, tomlKey)
		}
	}
}

func TestLoadConfig_ExplicitConfig_MarksUINodeAsExplicit(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, "xdg"))
	t.Setenv("HOME", filepath.Join(tmpDir, "home"))

	tests := []struct {
		name    string
		uiNode  string
		wantSet bool
	}{
		{
			name:    "explicit non-empty",
			uiNode:  `ui_node = "messenger"`,
			wantSet: true,
		},
		{
			name:    "explicit empty",
			uiNode:  `ui_node = ""`,
			wantSet: true,
		},
		{
			name:    "unset",
			uiNode:  "",
			wantSet: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			configPath := filepath.Join(tmpDir, tc.name+".toml")
			content := "[postman]\n"
			if tc.uiNode != "" {
				content += tc.uiNode + "\n"
			}
			content += "edges = [\"worker --- orchestrator\"]\n\n[worker]\n[orchestrator]\n"
			if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}

			cfg, err := LoadConfig(configPath)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}
			if got := cfg.HasExplicitUINodeSetting(); got != tc.wantSet {
				t.Fatalf("HasExplicitUINodeSetting() = %v, want %v", got, tc.wantSet)
			}
		})
	}
}

func TestLoadConfig_Partial(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")

	content := `
[postman]
scan_interval_seconds = 3.0
base_dir = "/partial/base"

edges = ["worker --- orchestrator"]

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

	// Default fields (not set in TOML) — now use embedded defaults
	if cfg.EnterDelay != 3.0 {
		t.Errorf("default EnterDelay: got %v, want 3.0", cfg.EnterDelay)
	}
	if !strings.HasPrefix(cfg.NotificationTemplate, "Hello, {node}!") {
		t.Errorf("default NotificationTemplate: got %q, want prefix Hello, {node}!", cfg.NotificationTemplate)
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
			edges: []string{"orchestrator --- worker"},
			want: map[string][]string{
				"orchestrator": {"worker"},
				"worker":       {"orchestrator"},
			},
		},
		{
			name:  "chain syntax (A --- B --- C)",
			edges: []string{"messenger --- orchestrator --- worker"},
			want: map[string][]string{
				"messenger":    {"orchestrator"},
				"orchestrator": {"messenger", "worker"},
				"worker":       {"orchestrator"},
			},
		},
		{
			name: "multiple edges",
			edges: []string{
				"orchestrator --- worker",
				"orchestrator --- observer",
			},
			want: map[string][]string{
				"orchestrator": {"worker", "observer"},
				"worker":       {"orchestrator"},
				"observer":     {"orchestrator"},
			},
		},
		{
			name:  "empty edge (skipped)",
			edges: []string{"", "  ", "orchestrator --- worker"},
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
			name:    "double dash rejected",
			edges:   []string{"orchestrator -- worker"},
			wantErr: true,
		},
		{
			name:    "arrow edge rejected",
			edges:   []string{"orchestrator --> worker"},
			wantErr: true,
		},
		{
			name:    "invalid format (empty node)",
			edges:   []string{"orchestrator --- "},
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

edges = ["worker --- orchestrator"]

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

edges = ["worker --- orchestrator"]

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
	t.Chdir(tmpDir)                     // Isolate from ambient workspace config
	t.Setenv("HOME", tmpDir)            // Isolate from ~/.config/
	t.Setenv("XDG_CONFIG_HOME", tmpDir) // Isolate from XDG postman.md
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
	t.Chdir(tmpDir)                     // Isolate from ambient workspace config
	t.Setenv("HOME", tmpDir)            // Isolate from ~/.config/
	t.Setenv("XDG_CONFIG_HOME", tmpDir) // Isolate from XDG postman.md
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
	t.Chdir(tmpDir)                     // Isolate from ambient workspace config
	t.Setenv("HOME", tmpDir)            // Isolate from ~/.config/
	t.Setenv("XDG_CONFIG_HOME", tmpDir) // Isolate from XDG postman.md
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
	t.Chdir(tmpDir)                     // Isolate from ambient workspace config
	t.Setenv("HOME", tmpDir)            // Isolate from ~/.config/
	t.Setenv("XDG_CONFIG_HOME", tmpDir) // Isolate from XDG postman.md
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
	t.Chdir(tmpDir)                     // Isolate from ambient workspace config
	t.Setenv("HOME", tmpDir)            // Isolate from ~/.config/
	t.Setenv("XDG_CONFIG_HOME", tmpDir) // Isolate from XDG postman.md
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

	// Verify main config works without nodes/ directory.
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

func TestMergeConfig_ScalarOverride(t *testing.T) {
	base := DefaultConfig()
	// DefaultConfig() returns zero values (SSOT is postman.default.toml).
	// Set explicit base values to test that mergeConfig preserves unset fields.
	base.ScanInterval = 1.0
	base.SessionScanInterval = 1.0
	base.EnterDelay = 0.5
	base.BaseDir = ""

	override := &Config{
		Nodes:               make(map[string]NodeConfig),
		ScanInterval:        5.0,
		SessionScanInterval: 0.5,
		BaseDir:             "/project/base",
	}

	mergeConfig(base, override)

	if base.ScanInterval != 5.0 {
		t.Errorf("ScanInterval: got %v, want 5.0", base.ScanInterval)
	}
	if base.SessionScanInterval != 0.5 {
		t.Errorf("SessionScanInterval: got %v, want 0.5", base.SessionScanInterval)
	}
	if base.BaseDir != "/project/base" {
		t.Errorf("BaseDir: got %q, want %q", base.BaseDir, "/project/base")
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

func TestLoadConfig_IgnoresProjectLocalWhenXDGExists(t *testing.T) {
	tmpDir := t.TempDir()
	fakeHome := filepath.Join(tmpDir, "home")
	xdgConfigHome := filepath.Join(tmpDir, "xdg")
	xdgConfigDir := filepath.Join(xdgConfigHome, "tmux-a2a-postman")
	if err := os.MkdirAll(xdgConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll xdgConfigDir failed: %v", err)
	}
	xdgConfig := `
[postman]
scan_interval_seconds = 2.0
edges = ["worker --- orchestrator"]

[worker]
role = "xdg-worker"

[orchestrator]
role = "xdg-orchestrator"
`
	if err := os.WriteFile(filepath.Join(xdgConfigDir, "postman.toml"), []byte(xdgConfig), 0o644); err != nil {
		t.Fatalf("WriteFile XDG config failed: %v", err)
	}

	projectDir := filepath.Join(fakeHome, "project")
	localConfigDir := filepath.Join(projectDir, ".tmux-a2a-postman")
	if err := os.MkdirAll(localConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll localConfigDir failed: %v", err)
	}
	localConfig := `
[postman]
scan_interval_seconds = 9.0

[worker]
role = "local-worker"
`
	if err := os.WriteFile(filepath.Join(localConfigDir, "postman.toml"), []byte(localConfig), 0o644); err != nil {
		t.Fatalf("WriteFile local config failed: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)
	t.Setenv("HOME", fakeHome)
	t.Chdir(projectDir)

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if cfg.ScanInterval != 2.0 {
		t.Fatalf("ScanInterval = %v, want XDG value 2.0", cfg.ScanInterval)
	}
	if cfg.Nodes["worker"].Role != "xdg-worker" {
		t.Fatalf("worker.Role = %q, want xdg-worker", cfg.Nodes["worker"].Role)
	}
}

func TestLoadConfig_ExplicitConfigIgnoresProjectLocalNodes(t *testing.T) {
	tmpDir := t.TempDir()
	fakeHome := filepath.Join(tmpDir, "home")
	explicitDir := filepath.Join(tmpDir, "explicit")
	if err := os.MkdirAll(explicitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll explicitDir failed: %v", err)
	}
	explicitConfigPath := filepath.Join(explicitDir, "postman.toml")
	explicitConfig := `
[postman]
edges = ["worker --- orchestrator"]

[worker]
role = "explicit-worker"

[orchestrator]
role = "explicit-orchestrator"
`
	if err := os.WriteFile(explicitConfigPath, []byte(explicitConfig), 0o644); err != nil {
		t.Fatalf("WriteFile explicit config failed: %v", err)
	}

	projectDir := filepath.Join(fakeHome, "project")
	localNodesDir := filepath.Join(projectDir, ".tmux-a2a-postman", "nodes")
	if err := os.MkdirAll(localNodesDir, 0o755); err != nil {
		t.Fatalf("MkdirAll localNodesDir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localNodesDir, "worker.toml"), []byte("[worker]\nrole = \"local-worker\"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile local node failed: %v", err)
	}

	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, "empty-xdg"))
	t.Setenv("HOME", fakeHome)
	t.Chdir(projectDir)

	cfg, err := LoadConfig(explicitConfigPath)
	if err != nil {
		t.Fatalf("LoadConfig(%q) failed: %v", explicitConfigPath, err)
	}
	if cfg.Nodes["worker"].Role != "explicit-worker" {
		t.Fatalf("worker.Role = %q, want explicit-worker", cfg.Nodes["worker"].Role)
	}
}

func TestLoadConfig_EmptyFile(t *testing.T) {
	// An empty config file has no nodes, which is a validation error.
	// This test documents the expected behavior.
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)                     // Isolate from ambient workspace config
	t.Setenv("HOME", tmpDir)            // Isolate from ~/.config/
	t.Setenv("XDG_CONFIG_HOME", tmpDir) // Isolate from XDG postman.md
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatal("expected validation error for empty config file (no nodes), got nil")
	}
}

func TestLoadConfig_MalformedTOML(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(configPath, []byte("[invalid toml syntax @@@ !!!"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	_, err := LoadConfig(configPath)
	if err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
}

// writeLivePID writes the current process PID to baseDir/contextName/sessionName/postman.pid.
func writeLivePID(t *testing.T, baseDir, contextName, sessionName string) {
	t.Helper()
	dir := filepath.Join(baseDir, contextName, sessionName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	pidPath := filepath.Join(dir, "postman.pid")
	if err := WriteSessionPIDFile(pidPath, os.Getpid()); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

// writeStalePID writes an invalid (0) PID to simulate a stale context.
func writeStalePID(t *testing.T, baseDir, contextName, sessionName string) {
	t.Helper()
	dir := filepath.Join(baseDir, contextName, sessionName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	pidPath := filepath.Join(dir, "postman.pid")
	if err := os.WriteFile(pidPath, []byte("0"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func pidFileOwnerUID(t *testing.T, baseDir, contextName, sessionName string) int {
	t.Helper()
	pidPath := filepath.Join(baseDir, contextName, sessionName, "postman.pid")
	info, err := os.Stat(pidPath)
	if err != nil {
		t.Fatalf("Stat postman.pid: %v", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("postman.pid stat does not expose Unix ownership")
	}
	return int(stat.Uid)
}

func withCurrentUID(t *testing.T, uid int) {
	t.Helper()
	orig := sessionPIDs
	sessionPIDs.currentUID = func() int { return uid }
	t.Cleanup(func() { sessionPIDs = orig })
}

func TestResolveContextIDFromSession(t *testing.T) {
	t.Run("exactly one live match", func(t *testing.T) {
		baseDir := t.TempDir()
		writeLivePID(t, baseDir, "session-abc", "my-session")
		got, err := ResolveContextIDFromSession(baseDir, "my-session")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "session-abc" {
			t.Errorf("got %q, want %q", got, "session-abc")
		}
	})

	t.Run("zero matches — dir exists but no pid file", func(t *testing.T) {
		baseDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(baseDir, "session-abc", "my-session"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		_, err := ResolveContextIDFromSession(baseDir, "my-session")
		if err == nil {
			t.Fatal("expected error for zero live matches, got nil")
		}
		if !strings.Contains(err.Error(), "no active postman found") {
			t.Errorf("error %q should contain 'no active postman found'", err.Error())
		}
	})

	t.Run("stale context skipped — dead pid", func(t *testing.T) {
		baseDir := t.TempDir()
		writeStalePID(t, baseDir, "session-stale", "my-session")
		_, err := ResolveContextIDFromSession(baseDir, "my-session")
		if err == nil {
			t.Fatal("expected error: stale context should be skipped, resulting in zero matches")
		}
		if !strings.Contains(err.Error(), "no active postman found") {
			t.Errorf("error %q should contain 'no active postman found'", err.Error())
		}
	})

	t.Run("stale skipped, live returned", func(t *testing.T) {
		baseDir := t.TempDir()
		writeStalePID(t, baseDir, "session-stale", "my-session")
		writeLivePID(t, baseDir, "session-live", "my-session")
		got, err := ResolveContextIDFromSession(baseDir, "my-session")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "session-live" {
			t.Errorf("got %q, want %q", got, "session-live")
		}
	})

	t.Run("multiple live matches — constraint violation", func(t *testing.T) {
		baseDir := t.TempDir()
		writeLivePID(t, baseDir, "session-abc", "my-session")
		writeLivePID(t, baseDir, "session-def", "my-session")
		_, err := ResolveContextIDFromSession(baseDir, "my-session")
		if err == nil {
			t.Fatal("expected error for multiple live matches, got nil")
		}
		if !strings.Contains(err.Error(), "constraint violation") {
			t.Errorf("error %q should contain 'constraint violation'", err.Error())
		}
	})

	t.Run("cross-session — enabled marker under daemon session, query from managed session", func(t *testing.T) {
		baseDir := t.TempDir()
		// Daemon runs in session "0", PID file is under "0"
		writeLivePID(t, baseDir, "session-ctx", "0")
		// Daemon manages "other-session" (directory exists, no PID there)
		otherDir := filepath.Join(baseDir, "session-ctx", "other-session", "inbox", "worker")
		if err := os.MkdirAll(otherDir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		installSessionOwnerTmux(t, map[string]string{
			"other-session": "session-ctx:12345",
		})
		// Query from "other-session" should find the context via the live PID in "0"
		got, err := ResolveContextIDFromSession(baseDir, "other-session")
		if err != nil {
			t.Fatalf("unexpected error: %v (cross-session resolution should work)", err)
		}
		if got != "session-ctx" {
			t.Errorf("got %q, want %q", got, "session-ctx")
		}
	})

	t.Run("empty baseDir", func(t *testing.T) {
		_, err := ResolveContextIDFromSession("", "my-session")
		if err == nil {
			t.Fatal("expected error for empty baseDir, got nil")
		}
	})

	t.Run("empty sessionName", func(t *testing.T) {
		_, err := ResolveContextIDFromSession("/tmp", "")
		if err == nil {
			t.Fatal("expected error for empty sessionName, got nil")
		}
	})
}

// TestIsSessionPIDAlive verifies liveness detection using postman.pid.
func TestIsSessionPIDAlive(t *testing.T) {
	t.Run("live pid returns true", func(t *testing.T) {
		baseDir := t.TempDir()
		writeLivePID(t, baseDir, "ctx", "sess")
		if !IsSessionPIDAlive(baseDir, "ctx", "sess") {
			t.Error("expected true for live PID, got false")
		}
	})

	t.Run("stale pid 0 returns false", func(t *testing.T) {
		baseDir := t.TempDir()
		writeStalePID(t, baseDir, "ctx", "sess")
		if IsSessionPIDAlive(baseDir, "ctx", "sess") {
			t.Error("expected false for stale PID 0, got true")
		}
	})

	t.Run("missing pid file returns false", func(t *testing.T) {
		baseDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(baseDir, "ctx", "sess"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if IsSessionPIDAlive(baseDir, "ctx", "sess") {
			t.Error("expected false for missing pid file, got true")
		}
	})

	t.Run("invalid pid content returns false", func(t *testing.T) {
		baseDir := t.TempDir()
		dir := filepath.Join(baseDir, "ctx", "sess")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "postman.pid"), []byte("not-a-pid"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if IsSessionPIDAlive(baseDir, "ctx", "sess") {
			t.Error("expected false for invalid pid content, got true")
		}
	})
}

func TestIsSessionPIDOwnedByCurrentUser(t *testing.T) {
	t.Run("live pid owned by current user returns true", func(t *testing.T) {
		baseDir := t.TempDir()
		writeLivePID(t, baseDir, "ctx", "sess")
		ownerUID := pidFileOwnerUID(t, baseDir, "ctx", "sess")
		withCurrentUID(t, ownerUID)

		if !IsSessionPIDOwnedByCurrentUser(baseDir, "ctx", "sess") {
			t.Fatal("expected true for current-user live PID, got false")
		}
	})

	t.Run("live pid owned by another user is alive but not owned", func(t *testing.T) {
		baseDir := t.TempDir()
		writeLivePID(t, baseDir, "ctx", "sess")
		ownerUID := pidFileOwnerUID(t, baseDir, "ctx", "sess")
		withCurrentUID(t, ownerUID+1)

		if !IsSessionPIDAlive(baseDir, "ctx", "sess") {
			t.Fatal("expected liveness check to keep treating the PID as alive")
		}
		if IsSessionPIDOwnedByCurrentUser(baseDir, "ctx", "sess") {
			t.Fatal("expected false for live PID owned by another user")
		}
	})
}

func TestFindCurrentUserDaemon(t *testing.T) {
	t.Run("returns current-user daemon", func(t *testing.T) {
		baseDir := t.TempDir()
		writeLivePID(t, baseDir, "ctx-owner", "main")
		ownerUID := pidFileOwnerUID(t, baseDir, "ctx-owner", "main")
		withCurrentUID(t, ownerUID)

		contextID, sessionName, ok := FindCurrentUserDaemon(baseDir)
		if !ok {
			t.Fatal("FindCurrentUserDaemon() ok = false, want true")
		}
		if contextID != "ctx-owner" || sessionName != "main" {
			t.Fatalf("FindCurrentUserDaemon() = (%q, %q), want (%q, %q)", contextID, sessionName, "ctx-owner", "main")
		}
	})

	t.Run("ignores different Unix user daemon", func(t *testing.T) {
		baseDir := t.TempDir()
		writeLivePID(t, baseDir, "ctx-owner", "main")
		ownerUID := pidFileOwnerUID(t, baseDir, "ctx-owner", "main")
		withCurrentUID(t, ownerUID+1)

		if contextID, sessionName, ok := FindCurrentUserDaemon(baseDir); ok {
			t.Fatalf("FindCurrentUserDaemon() = (%q, %q, true), want no current-user daemon", contextID, sessionName)
		}
	})
}

func TestContextOwnsSession(t *testing.T) {
	t.Run("live daemon session itself returns true without marker", func(t *testing.T) {
		baseDir := t.TempDir()
		writeLivePID(t, baseDir, "ctx-live", "daemon-session")
		installSessionOwnerTmux(t, map[string]string{})
		if !ContextOwnsSession(baseDir, "ctx-live", "daemon-session") {
			t.Fatal("expected true for live context owning its own daemon session, got false")
		}
	})

	t.Run("live daemon plus foreign session subdir returns false without enabled marker", func(t *testing.T) {
		baseDir := t.TempDir()
		writeLivePID(t, baseDir, "ctx-live", "0")
		installSessionOwnerTmux(t, map[string]string{})
		if err := os.MkdirAll(filepath.Join(baseDir, "ctx-live", "other-session"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if ContextOwnsSession(baseDir, "ctx-live", "other-session") {
			t.Fatal("expected false when enabled-session marker is absent, got true")
		}
	})

	t.Run("stale context with session subdir returns false", func(t *testing.T) {
		baseDir := t.TempDir()
		writeStalePID(t, baseDir, "ctx-stale", "0")
		installSessionOwnerTmux(t, map[string]string{
			"other-session": "ctx-stale:0",
		})
		if err := os.MkdirAll(filepath.Join(baseDir, "ctx-stale", "other-session"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if ContextOwnsSession(baseDir, "ctx-stale", "other-session") {
			t.Fatal("expected false for stale context ownership, got true")
		}
	})

	t.Run("different Unix user daemon session returns false but remains live", func(t *testing.T) {
		baseDir := t.TempDir()
		writeLivePID(t, baseDir, "ctx-other-user", "daemon-session")
		ownerUID := pidFileOwnerUID(t, baseDir, "ctx-other-user", "daemon-session")
		withCurrentUID(t, ownerUID+1)
		installSessionOwnerTmux(t, map[string]string{})

		if !ContextHasLiveDaemon(baseDir, "ctx-other-user") {
			t.Fatal("expected different-user daemon to remain live for cleanup safety")
		}
		if ContextOwnsSession(baseDir, "ctx-other-user", "daemon-session") {
			t.Fatal("expected false for daemon session owned by another Unix user")
		}
		if got := FindContextSessionName(baseDir, "ctx-other-user"); got != "" {
			t.Fatalf("FindContextSessionName() = %q, want empty for different Unix user", got)
		}
	})
}

func TestFindSessionOwner(t *testing.T) {
	t.Run("returns other live owner for managed session with enabled marker", func(t *testing.T) {
		baseDir := t.TempDir()
		writeLivePID(t, baseDir, "ctx-owner", "0")
		installSessionOwnerTmux(t, map[string]string{
			"other-session": "ctx-owner:12345",
		})
		if err := os.MkdirAll(filepath.Join(baseDir, "ctx-owner", "other-session"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		got := FindSessionOwner(baseDir, "other-session", "ctx-self")
		if got != "ctx-owner" {
			t.Fatalf("got %q, want %q", got, "ctx-owner")
		}
	})

	t.Run("skips own context", func(t *testing.T) {
		baseDir := t.TempDir()
		writeLivePID(t, baseDir, "ctx-owner", "0")
		installSessionOwnerTmux(t, map[string]string{
			"other-session": "ctx-owner:12345",
		})
		if err := os.MkdirAll(filepath.Join(baseDir, "ctx-owner", "other-session"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		got := FindSessionOwner(baseDir, "other-session", "ctx-owner")
		if got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})

	t.Run("live context without enabled marker is not owner", func(t *testing.T) {
		baseDir := t.TempDir()
		writeLivePID(t, baseDir, "ctx-live", "0")
		installSessionOwnerTmux(t, map[string]string{})
		if err := os.MkdirAll(filepath.Join(baseDir, "ctx-live", "other-session"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		got := FindSessionOwner(baseDir, "other-session", "ctx-self")
		if got != "" {
			t.Fatalf("got %q, want empty", got)
		}
	})
}

func TestIsSessionPIDAlive_FreshContextWithoutPIDIsNotAlive(_ *testing.T) {
	// A freshly created context with no pid file must not be considered alive.
	baseDir := fmt.Sprintf("%s/guard-test-%d", os.TempDir(), os.Getpid())
	_ = os.MkdirAll(filepath.Join(baseDir, "ctx", "sess"), 0o755)
	defer func() { _ = os.RemoveAll(baseDir) }()
	if IsSessionPIDAlive(baseDir, "ctx", "sess") {
		panic("fresh context with no pid file was considered alive")
	}
}

func TestResolveLocalConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".tmux-a2a-postman")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	configPath := filepath.Join(configDir, "postman.toml")
	if err := os.WriteFile(configPath, []byte(""), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := ResolveLocalConfigPath(tmpDir, "")
	if err != nil {
		t.Fatalf("ResolveLocalConfigPath: %v", err)
	}
	if got != "" {
		t.Errorf("ResolveLocalConfigPath = %q, want empty after project-local retirement", got)
	}

	// Returns "" when no sentinel.
	empty, err := ResolveLocalConfigPath(t.TempDir(), "")
	if err != nil {
		t.Fatalf("ResolveLocalConfigPath (absent): %v", err)
	}
	if empty != "" {
		t.Errorf("ResolveLocalConfigPath (absent) = %q, want empty", empty)
	}
}

func TestResolveContextID(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantResult string
		wantErrSub string
	}{
		{name: "valid", input: "ctx-123", wantResult: "ctx-123"},
		{name: "empty", input: "", wantErrSub: "--context-id is required"},
		{name: "path traversal single", input: "../bad", wantErrSub: "invalid value"},
		{name: "path traversal deep", input: "../../etc/passwd", wantErrSub: "invalid value"},
		{name: "64-char at limit", input: strings.Repeat("a", 64), wantResult: strings.Repeat("a", 64)},
		{name: "65-char exceeds limit", input: strings.Repeat("a", 65), wantErrSub: "invalid value"},
		{name: "embedded slash", input: "valid/sub", wantErrSub: "invalid value"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveContextID(tc.input)
			if tc.wantErrSub != "" {
				if err == nil {
					t.Fatalf("ResolveContextID(%q) = %q, want error containing %q", tc.input, got, tc.wantErrSub)
				}
				if !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("ResolveContextID(%q) error = %q, want containing %q", tc.input, err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveContextID(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.wantResult {
				t.Errorf("ResolveContextID(%q) = %q, want %q", tc.input, got, tc.wantResult)
			}
		})
	}
}

func TestValidateSessionName(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantResult string
		wantErrSub string
	}{
		{name: "valid plain name", input: "my-session", wantResult: "my-session"},
		{name: "empty string", input: "", wantErrSub: "invalid value"},
		{name: "whitespace only", input: "   ", wantErrSub: "invalid value"},
		{name: "null byte", input: "a\x00b", wantErrSub: "invalid value"},
		{name: "forward slash", input: "a/b", wantErrSub: "invalid value"},
		{name: "backslash", input: "a\\b", wantErrSub: "invalid value"},
		{name: "dot component", input: ".", wantErrSub: "invalid value"},
		{name: "dotdot component", input: "..", wantErrSub: "invalid value"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateSessionName(tc.input)
			if tc.wantErrSub != "" {
				if err == nil {
					t.Fatalf("ValidateSessionName(%q) = %q, want error containing %q", tc.input, got, tc.wantErrSub)
				}
				if !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("ValidateSessionName(%q) error = %q, want containing %q", tc.input, err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateSessionName(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.wantResult {
				t.Errorf("ValidateSessionName(%q) = %q, want %q", tc.input, got, tc.wantResult)
			}
		})
	}
}

func TestLoadConfig_ExplicitConfigTrustedBaseShellExpansionAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	fakeHome := filepath.Join(tmpDir, "home")
	projectDir := filepath.Join(fakeHome, "project")
	explicitConfigDir := filepath.Join(tmpDir, "explicit")
	explicitConfigPath := filepath.Join(explicitConfigDir, "postman.toml")

	writeFile(t, explicitConfigPath, `
[postman]
allow_shell_templates = true
draft_template = "trusted $(printf explicit-config-base)"

[worker]
role = "worker"
`)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("MkdirAll projectDir: %v", err)
	}

	t.Setenv("HOME", fakeHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, "xdg"))
	t.Chdir(projectDir)

	cfg, err := LoadConfig(explicitConfigPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.AllowShellTemplates {
		t.Fatal("AllowShellTemplates = false, want true from explicit trusted base")
	}

	got := template.ExpandTemplate(cfg.DraftTemplate, map[string]string{}, 5*time.Second, cfg.AllowShellForDraftTemplate())
	if got != "trusted explicit-config-base" {
		t.Fatalf("explicit trusted base draft template = %q, want %q", got, "trusted explicit-config-base")
	}
}

func TestWarnDeprecatedKeys(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		wantKey string
	}{
		{
			name:    "startup_delay_seconds triggers warning",
			raw:     "startup_delay_seconds = 10.0\nscan_interval_seconds = 1.0\n",
			wantKey: "startup_delay_seconds",
		},
		{
			name:    "auto_enable_new_agents triggers warning",
			raw:     "auto_enable_new_agents = true\n",
			wantKey: "auto_enable_new_agents",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log.SetOutput(&buf)
			t.Cleanup(func() { log.SetOutput(os.Stderr) })

			warnDeprecatedKeys([]byte(tc.raw), "/fake/config.toml")

			if !strings.Contains(buf.String(), tc.wantKey) {
				t.Errorf("warnDeprecatedKeys: expected warning containing %q, got: %q", tc.wantKey, buf.String())
			}
		})
	}

	t.Run("no warning for current keys", func(t *testing.T) {
		var buf bytes.Buffer
		log.SetOutput(&buf)
		t.Cleanup(func() { log.SetOutput(os.Stderr) })

		warnDeprecatedKeys([]byte("scan_interval_seconds = 1.0\nauto_enable_new_sessions = true\n"), "/fake/config.toml")

		if buf.Len() != 0 {
			t.Errorf("warnDeprecatedKeys: unexpected warning output: %q", buf.String())
		}
	})
}
