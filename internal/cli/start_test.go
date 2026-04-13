package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/lock"
)

func TestResolveWatchedConfigPath(t *testing.T) {
	tests := []struct {
		name       string
		withXDG    bool
		withLocal  bool
		withCustom bool
		want       []string
		wantNodes  []string
	}{
		{
			name:       "explicit path wins",
			withXDG:    true,
			withLocal:  true,
			withCustom: true,
			want:       []string{"custom", "local"},
			wantNodes:  []string{"custom", "local"},
		},
		{
			name:      "project local overrides xdg",
			withXDG:   true,
			withLocal: true,
			want:      []string{"xdg", "local"},
			wantNodes: []string{"xdg", "local"},
		},
		{
			name:      "xdg used when local missing",
			withXDG:   true,
			withLocal: false,
			want:      []string{"xdg"},
			wantNodes: []string{"xdg"},
		},
		{
			name:      "project local used without xdg",
			withLocal: true,
			want:      []string{"local"},
			wantNodes: []string{"local"},
		},
		{
			name: "empty when nothing exists",
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			homeDir := filepath.Join(root, "home")
			repoDir := filepath.Join(homeDir, "work", "repo")
			xdgDir := filepath.Join(root, "xdg")
			if err := os.MkdirAll(repoDir, 0o755); err != nil {
				t.Fatalf("MkdirAll repo: %v", err)
			}
			if err := os.MkdirAll(homeDir, 0o755); err != nil {
				t.Fatalf("MkdirAll home: %v", err)
			}

			t.Chdir(repoDir)
			t.Setenv("HOME", homeDir)
			t.Setenv("XDG_CONFIG_HOME", xdgDir)

			paths := make(map[string]string)
			if tc.withXDG {
				paths["xdg"] = filepath.Join(xdgDir, "tmux-a2a-postman", "postman.toml")
				writeWatcherConfigFixture(t, paths["xdg"])
			}
			if tc.withLocal {
				paths["local"] = filepath.Join(repoDir, ".tmux-a2a-postman", "postman.toml")
				writeWatcherConfigFixture(t, paths["local"])
			}
			explicitPath := ""
			if tc.withCustom {
				explicitPath = filepath.Join(root, "custom", "postman.toml")
				writeWatcherConfigFixture(t, explicitPath)
				paths["custom"] = explicitPath
			}

			got := resolveWatchedConfigPaths(explicitPath)
			want := make([]string, 0, len(tc.want))
			for _, label := range tc.want {
				want = append(want, paths[label])
			}
			if len(got) != len(want) {
				t.Fatalf("resolveWatchedConfigPaths(%q) len = %d, want %d; got=%v want=%v", explicitPath, len(got), len(want), got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("resolveWatchedConfigPaths(%q)[%d] = %q, want %q; full got=%v want=%v", explicitPath, i, got[i], want[i], got, want)
				}
			}

			gotNodes := resolveWatchedNodesDirs(got)
			wantNodes := make([]string, 0, len(tc.wantNodes))
			for _, label := range tc.wantNodes {
				wantNodes = append(wantNodes, filepath.Join(filepath.Dir(paths[label]), "nodes"))
			}
			if len(gotNodes) != len(wantNodes) {
				t.Fatalf("resolveWatchedNodesDirs(%q) len = %d, want %d; got=%v want=%v", explicitPath, len(gotNodes), len(wantNodes), gotNodes, wantNodes)
			}
			for i := range wantNodes {
				if gotNodes[i] != wantNodes[i] {
					t.Fatalf("resolveWatchedNodesDirs(%q)[%d] = %q, want %q; full got=%v want=%v", explicitPath, i, gotNodes[i], wantNodes[i], gotNodes, wantNodes)
				}
			}
		})
	}
}

func writeWatcherConfigFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll fixture dir: %v", err)
	}
	if err := os.WriteFile(path, []byte("[postman]\nui_node = \"messenger\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(filepath.Dir(path), "nodes"), 0o755); err != nil {
		t.Fatalf("MkdirAll nodes dir: %v", err)
	}
}

func TestRunStartWithFlags_RejectsDuplicateDaemonForSameSession(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "state")
	contextID := "20260404-ctx"
	sessionName := "main"

	configPath := filepath.Join(root, "postman.toml")
	configContent := "[postman]\nedges = [\"boss -- worker\"]\n\n" +
		"[boss]\nrole = \"boss\"\ntemplate = \"boss\"\n\n" +
		"[worker]\nrole = \"worker\"\ntemplate = \"worker\"\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	pidDir := filepath.Join(baseDir, contextID, sessionName)
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(pidDir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "postman.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile(postman.pid): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1 $2 $3 $4 $5\" = \"display-message -t %11 -p #{session_name}\" ]; then\n" +
		"  printf '%s\\n' '" + sessionName + "'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}

	t.Setenv("POSTMAN_HOME", baseDir)
	t.Setenv("TMUX_PANE", "%11")
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := RunStartWithFlags(contextID, configPath, "", true)
	if err == nil {
		t.Fatal("RunStartWithFlags() error = nil, want duplicate-daemon rejection")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Fatalf("RunStartWithFlags() error = %q, want duplicate-daemon wording", err)
	}
}

func TestRunStartWithFlags_RejectsCrossContextDaemonForSameSessionLock(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "state")
	contextID := "20260405-ctx-b"
	sessionName := "main"

	configPath := filepath.Join(root, "postman.toml")
	configContent := "[postman]\nedges = [\"boss -- worker\"]\n\n" +
		"[boss]\nrole = \"boss\"\ntemplate = \"boss\"\n\n" +
		"[worker]\nrole = \"worker\"\ntemplate = \"worker\"\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	lockDir := filepath.Join(baseDir, "lock")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(lockDir): %v", err)
	}
	lockObj, err := lock.NewSessionLock(filepath.Join(lockDir, sessionName+".lock"))
	if err != nil {
		t.Fatalf("NewSessionLock(pre-acquire): %v", err)
	}
	defer func() { _ = lockObj.Release() }()

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1 $2 $3 $4 $5\" = \"display-message -t %11 -p #{session_name}\" ]; then\n" +
		"  printf '%s\\n' '" + sessionName + "'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}

	t.Setenv("POSTMAN_HOME", baseDir)
	t.Setenv("TMUX_PANE", "%11")
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err = RunStartWithFlags(contextID, configPath, "", true)
	if err == nil {
		t.Fatal("RunStartWithFlags() error = nil, want same-session lock rejection")
	}
	if !strings.Contains(err.Error(), "acquiring lock") {
		t.Fatalf("RunStartWithFlags() error = %q, want acquiring lock wording", err)
	}
	if !strings.Contains(err.Error(), "lock already held") {
		t.Fatalf("RunStartWithFlags() error = %q, want lock already held wording", err)
	}
}

func TestRestrictPingTargetsToConfiguredUINode(t *testing.T) {
	nodes := map[string]discovery.NodeInfo{
		"review:messenger": {},
		"review:worker":    {},
	}

	t.Run("embedded default does not narrow", func(t *testing.T) {
		tmpDir := t.TempDir()
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, "xdg"))
		t.Setenv("HOME", filepath.Join(tmpDir, "home"))
		t.Chdir(tmpDir)

		cfg, err := config.LoadConfig("")
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}

		filtered, ok := restrictPingTargetsToConfiguredUINode(nodes, cfg)
		if !ok {
			t.Fatal("embedded default should not report a missing ui_node target")
		}
		if len(filtered) != len(nodes) {
			t.Fatalf("embedded default filtered %d nodes, want %d", len(filtered), len(nodes))
		}
	})

	t.Run("explicit ui_node narrows to that node", func(t *testing.T) {
		root := t.TempDir()
		envRoot := t.TempDir()
		configPath := filepath.Join(root, "postman.toml")
		t.Chdir(root)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(envRoot, "xdg"))
		t.Setenv("HOME", filepath.Join(envRoot, "home"))
		content := "[postman]\nui_node = \"messenger\"\nedges = [\"messenger -- worker\"]\n\n[messenger]\n[worker]\n"
		if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}

		filtered, ok := restrictPingTargetsToConfiguredUINode(nodes, cfg)
		if !ok {
			t.Fatal("explicit ui_node should be discoverable in the target set")
		}
		if len(filtered) != 1 {
			t.Fatalf("explicit ui_node filtered %d nodes, want 1", len(filtered))
		}
		if _, exists := filtered["review:messenger"]; !exists {
			t.Fatal("explicit ui_node filter did not keep messenger")
		}
	})

	t.Run("explicit missing ui_node blocks narrowing", func(t *testing.T) {
		root := t.TempDir()
		envRoot := t.TempDir()
		configPath := filepath.Join(root, "postman.toml")
		t.Chdir(root)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(envRoot, "xdg"))
		t.Setenv("HOME", filepath.Join(envRoot, "home"))
		content := "[postman]\nui_node = \"critic\"\nedges = [\"messenger -- worker\"]\n\n[messenger]\n[worker]\n"
		if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}

		filtered, ok := restrictPingTargetsToConfiguredUINode(nodes, cfg)
		if ok {
			t.Fatal("missing explicit ui_node should report failure")
		}
		if len(filtered) != 0 {
			t.Fatalf("missing explicit ui_node filtered %d nodes, want 0", len(filtered))
		}
	})

	t.Run("explicit empty ui_node keeps fanout", func(t *testing.T) {
		root := t.TempDir()
		envRoot := t.TempDir()
		configPath := filepath.Join(root, "postman.toml")
		t.Chdir(root)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(envRoot, "xdg"))
		t.Setenv("HOME", filepath.Join(envRoot, "home"))
		content := "[postman]\nui_node = \"\"\nedges = [\"messenger -- worker\"]\n\n[messenger]\n[worker]\n"
		if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}

		cfg, err := config.LoadConfig(configPath)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}

		filtered, ok := restrictPingTargetsToConfiguredUINode(nodes, cfg)
		if !ok {
			t.Fatal("explicit empty ui_node should not report a missing ui_node target")
		}
		if len(filtered) != len(nodes) {
			t.Fatalf("explicit empty ui_node filtered %d nodes, want %d", len(filtered), len(nodes))
		}
	})
}

func TestRunStartWithFlags_SourceContractKeepsUnreadInboxAndOwnershipGuard(t *testing.T) {
	source := readRepoFile(t, "internal/cli/start.go")

	if strings.Contains(source, "if err := cleanupStaleInbox(inboxDir, readDir); err != nil") {
		t.Fatal("start.go still archives unread inbox messages during startup")
	}
	if !strings.Contains(source, "config.ContextOwnsSession(baseDir, claimedContext, paneSessionName)") {
		t.Fatal("start.go no longer uses the session ownership contract when reclaiming pane claims")
	}
	if strings.Contains(source, "config.IsSessionPIDAlive(baseDir, claimedContext, paneSessionName)") {
		t.Fatal("start.go still clears foreign pane claims from a raw PID check")
	}
	markerIndex := strings.Index(source, `config.SetSessionEnabledMarker(contextID, sessionName, true)`)
	reclaimIndex := strings.Index(source, "// Reclaim panes from dead daemon contexts (#272)")
	discoveryIndex := strings.Index(source, "// Discover nodes at startup (before watching, edge-filtered)")
	if markerIndex == -1 {
		t.Fatal("start.go no longer publishes the enabled-session marker during cold start")
	}
	if reclaimIndex == -1 || discoveryIndex == -1 {
		t.Fatal("start.go startup ordering markers changed; update the source contract test")
	}
	if markerIndex > reclaimIndex {
		t.Fatal("start.go still publishes the enabled-session marker after pane-claim reclaim begins")
	}
	if markerIndex > discoveryIndex {
		t.Fatal("start.go still publishes the enabled-session marker after startup discovery begins")
	}
}

func TestRunStartWithFlags_SourceContractUsesSharedEdgeFilter(t *testing.T) {
	source := readRepoFile(t, "internal/cli/start.go")

	if strings.Count(source, "filterDiscoveredEdgeNodes(") < 3 {
		t.Fatal("start.go no longer routes startup discovery through the shared exact-or-raw edge filter")
	}
}
