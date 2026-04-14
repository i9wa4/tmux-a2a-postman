package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
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

func TestRunStartWithFlags_RejectsInvalidJournalCutoverConfig(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "postman.toml")
	configContent := "[postman]\n" +
		"edges = [\"boss -- worker\"]\n" +
		"journal_compatibility_cutover_enabled = true\n\n" +
		"[boss]\nrole = \"boss\"\ntemplate = \"boss\"\n\n" +
		"[worker]\nrole = \"worker\"\ntemplate = \"worker\"\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	err := RunStartWithFlags("ctx-invalid-cutover", configPath, "", true)
	if err == nil {
		t.Fatal("RunStartWithFlags() error = nil, want invalid cutover rejection")
	}
	if !strings.Contains(err.Error(), "journal cutover") {
		t.Fatalf("RunStartWithFlags() error = %q, want journal cutover wording", err)
	}
	if !strings.Contains(err.Error(), "journal_compatibility_cutover_enabled requires journal_health_cutover_enabled") {
		t.Fatalf("RunStartWithFlags() error = %q, want cutover dependency wording", err)
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

func TestPingTargetsForSession_BroadcastsAllNodesInSession(t *testing.T) {
	nodes := map[string]discovery.NodeInfo{
		"review:messenger":  {SessionName: "review"},
		"review:worker":     {SessionName: "review"},
		"main:orchestrator": {SessionName: "main"},
	}

	targets := pingTargetsForSession(nodes, "review")
	if len(targets) != 2 {
		t.Fatalf("pingTargetsForSession() returned %d nodes, want 2", len(targets))
	}
	if _, ok := targets["review:messenger"]; !ok {
		t.Fatal("pingTargetsForSession() missing review:messenger")
	}
	if _, ok := targets["review:worker"]; !ok {
		t.Fatal("pingTargetsForSession() missing review:worker")
	}
	if _, ok := targets["main:orchestrator"]; ok {
		t.Fatal("pingTargetsForSession() included a node from a different session")
	}
}

func TestSendCompactionPings_DeliversPingToDetectedNode(t *testing.T) {
	tracker := idle.NewIdleTracker()
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}
	cfg := &config.Config{
		DaemonMessageTemplate: "{message}",
		TmuxTimeout:           1.0,
	}

	sendCompactionPings("ctx-compaction", cfg, tracker, nodes, []string{"review:worker"})

	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		t.Fatalf("ReadDir(inbox): %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 compaction-triggered PING, got %d", len(entries))
	}

	body, err := os.ReadFile(filepath.Join(inboxDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile(inbox message): %v", err)
	}
	if !strings.Contains(string(body), "PING from postman daemon") {
		t.Fatalf("compaction-triggered PING body = %q, want daemon PING message", string(body))
	}
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

func TestCleanupExpiredRuntimeState_PreservesLiveAndDurablePaths(t *testing.T) {
	baseDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 1, 46, 0, 0, time.UTC)
	staleWhen := now.AddDate(0, 0, -31)

	lockDir := filepath.Join(baseDir, "lock")
	if err := os.MkdirAll(lockDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(lockDir): %v", err)
	}
	markOldRuntimePath(t, lockDir, staleWhen)

	liveContext := "ctx-live"
	liveSession := "20240101-review"
	liveContextDir := writeRuntimeSessionFixture(t, baseDir, liveContext, liveSession, true)
	liveLog := filepath.Join(liveContextDir, "postman.log")
	writeRuntimeFileFixture(t, liveLog, "live log")
	markOldRuntimePath(t, filepath.Join(liveContextDir, liveSession), staleWhen)
	markOldRuntimePath(t, liveLog, staleWhen)

	staleContext := "ctx-stale"
	staleSession := "main"
	staleContextDir := writeRuntimeSessionFixture(t, baseDir, staleContext, staleSession, false)
	staleSessionDir := filepath.Join(staleContextDir, staleSession)
	staleLog := filepath.Join(staleContextDir, "postman.log")
	stalePaneActivity := filepath.Join(staleContextDir, "pane-activity.json")
	stalePhony := filepath.Join(staleContextDir, "phony")
	staleMemory := filepath.Join(staleContextDir, "supervisor-memory")
	staleUnknown := filepath.Join(staleContextDir, "scratch-cache")

	writeRuntimeFileFixture(t, staleLog, "old log")
	writeRuntimeFileFixture(t, stalePaneActivity, "{}")
	writeRuntimeFileFixture(t, filepath.Join(stalePhony, "channel", "inbox", "message.json"), "{}")
	writeRuntimeFileFixture(t, filepath.Join(staleMemory, "note.yaml"), "summary: preserve")
	if err := os.MkdirAll(staleUnknown, 0o700); err != nil {
		t.Fatalf("MkdirAll(scratch-cache): %v", err)
	}

	for _, path := range []string{staleSessionDir, staleLog, stalePaneActivity, stalePhony, staleMemory, staleUnknown} {
		markOldRuntimePath(t, path, staleWhen)
	}

	removed, err := cleanupExpiredRuntimeState(baseDir, "ctx-current", 30, now)
	if err != nil {
		t.Fatalf("cleanupExpiredRuntimeState: %v", err)
	}
	if removed < 3 {
		t.Fatalf("cleanupExpiredRuntimeState removed %d entries, want at least stale session + log + pane activity", removed)
	}

	assertPathExists(t, lockDir)
	assertPathExists(t, filepath.Join(liveContextDir, liveSession))
	assertPathExists(t, liveLog)
	assertPathMissing(t, staleSessionDir)
	assertPathMissing(t, staleLog)
	assertPathMissing(t, stalePaneActivity)
	assertPathExists(t, stalePhony)
	assertPathExists(t, staleMemory)
	assertPathExists(t, staleUnknown)
}

func TestCleanupExpiredRuntimeState_ZeroRetentionDisablesCleanup(t *testing.T) {
	baseDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 1, 46, 0, 0, time.UTC)
	staleWhen := now.AddDate(0, 0, -60)

	contextDir := writeRuntimeSessionFixture(t, baseDir, "ctx-stale", "main", false)
	sessionDir := filepath.Join(contextDir, "main")
	markOldRuntimePath(t, sessionDir, staleWhen)

	removed, err := cleanupExpiredRuntimeState(baseDir, "ctx-current", 0, now)
	if err != nil {
		t.Fatalf("cleanupExpiredRuntimeState: %v", err)
	}
	if removed != 0 {
		t.Fatalf("cleanupExpiredRuntimeState removed %d entries, want 0 when retention is disabled", removed)
	}
	assertPathExists(t, sessionDir)
}

func TestRunStartWithFlags_SourceContractUsesSharedEdgeFilter(t *testing.T) {
	source := readRepoFile(t, "internal/cli/start.go")

	if strings.Count(source, "filterDiscoveredEdgeNodes(") < 3 {
		t.Fatal("start.go no longer routes startup discovery through the shared exact-or-raw edge filter")
	}
}

func writeRuntimeSessionFixture(t *testing.T, baseDir, contextID, sessionName string, livePID bool) string {
	t.Helper()

	contextDir := filepath.Join(baseDir, contextID)
	if err := config.CreateMultiSessionDirs(contextDir, sessionName); err != nil {
		t.Fatalf("CreateMultiSessionDirs(%q, %q): %v", contextDir, sessionName, err)
	}
	if livePID {
		pidPath := filepath.Join(contextDir, sessionName, "postman.pid")
		if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatalf("WriteFile(postman.pid): %v", err)
		}
	}
	return contextDir
}

func writeRuntimeFileFixture(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func markOldRuntimePath(t *testing.T, path string, when time.Time) {
	t.Helper()

	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("Chtimes(%q): %v", path, err)
	}
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("Stat(%q) error = %v, want not exists", path, err)
	}
}
