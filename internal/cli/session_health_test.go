package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

func TestRunGetSessionStatus_UsesTMUXSessionWhenSessionFlagMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)

	t.Setenv("TMUX_PANE", "%77")

	scriptDir := t.TempDir()
	scriptPath := scriptDir + string(os.PathSeparator) + "tmux"
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *\"#{session_name}\"*) printf '%s\\n' \"tmux-session\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fake tmux: %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := RunGetSessionStatus(nil)
	if err != nil && (strings.Contains(err.Error(), "flag provided but not defined") ||
		strings.Contains(err.Error(), "session name required")) {
		t.Fatalf("RunGetSessionStatus should use tmux session fallback, got: %v", err)
	}
}

func TestSessionHealth_NoActivePostmanReturnsEmptyPayload(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForCLI(t, tmpDir, "review", "worker")

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = oldStdout
	}()

	runErr := RunGetSessionStatus(nil)
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	if runErr != nil {
		t.Fatalf("RunGetSessionStatus: %v", runErr)
	}

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(stdout): %v", err)
	}

	var payload status.SessionHealth
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", string(out), err)
	}

	if payload.ContextID != "" {
		t.Fatalf("ContextID = %q, want empty", payload.ContextID)
	}
	if payload.SessionName != "review" {
		t.Fatalf("SessionName = %q, want %q", payload.SessionName, "review")
	}
	if payload.NodeCount != 0 {
		t.Fatalf("NodeCount = %d, want 0", payload.NodeCount)
	}
	if payload.VisibleState != "" {
		t.Fatalf("VisibleState = %q, want empty", payload.VisibleState)
	}
	if payload.Compact != "" {
		t.Fatalf("Compact = %q, want empty", payload.Compact)
	}
	if len(payload.Nodes) != 0 {
		t.Fatalf("len(Nodes) = %d, want 0", len(payload.Nodes))
	}
	if len(payload.Windows) != 0 {
		t.Fatalf("len(Windows) = %d, want 0", len(payload.Windows))
	}
}

func TestRunGetSessionStatus_IncludesVisibleStateAndTopology(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "20260404-ctx"
	sessionName := "review"
	sessionDir := filepath.Join(tmpDir, contextID, sessionName)

	t.Setenv("POSTMAN_HOME", tmpDir)

	configPath := filepath.Join(tmpDir, "postman.toml")
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"worker --- critic\"]\n\n[worker]\ntemplate = \"worker\"\nrole = \"worker\"\n\n[critic]\ntemplate = \"critic\"\nrole = \"critic\"\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	for _, dir := range []string{
		filepath.Join(sessionDir, "inbox", "worker"),
		filepath.Join(sessionDir, "inbox", "critic"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	if err := os.WriteFile(
		filepath.Join(tmpDir, contextID, "pane-activity.json"),
		[]byte(`{
  "%11": {"status":"active","lastChangeAt":"2026-04-04T00:00:00Z"},
  "%12": {"status":"idle","lastChangeAt":"2026-04-04T00:00:00Z"}
}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(pane-activity.json): %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(sessionDir, "inbox", "worker", "20260404-000000-s0000-from-boss-to-worker.md"),
		[]byte("body"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(worker inbox): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  \"display-message \"*\"#{session_name}\"*) printf '%s\\n' \"" + sessionName + "\" ;;\n" +
		"  \"list-panes -a -F\"*)\n" +
		"    printf '%s\\n' '%11\t" + contextID + "\t" + sessionName + "\tworker' '%12\t" + contextID + "\t" + sessionName + "\tcritic'\n" +
		"    ;;\n" +
		"  \"list-windows -t " + sessionName + "\"*)\n" +
		"    printf '%s\\n' '0'\n" +
		"    ;;\n" +
		"  \"list-panes -t " + sessionName + ":0\"*)\n" +
		"    printf '%s\\n' '0\t0\t%11\tworker\tclaude' '0\t1\t%12\tcritic\tclaude'\n" +
		"    ;;\n" +
		"  *)\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	legacy, err := collectSessionHealthLegacy(tmpDir, contextID, sessionName, &config.Config{
		Edges: []string{"worker --- critic"},
	})
	if err != nil {
		t.Fatalf("collectSessionHealthLegacy: %v", err)
	}
	appendSessionHealthSnapshot(t, sessionHealthProjectionFixture{
		baseDir:     tmpDir,
		contextID:   contextID,
		sessionName: sessionName,
	}, legacy)

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = oldStdout
	}()

	runErr := RunGetSessionStatus([]string{
		"--config", configPath,
		"--context-id", contextID,
	})
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	if runErr != nil {
		t.Fatalf("RunGetSessionStatus: %v", runErr)
	}

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(stdout): %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", string(out), err)
	}

	if got := payload["visible_state"]; got != "pending" {
		t.Fatalf("visible_state = %#v, want %q", got, "pending")
	}
	if got := payload["compact"]; got != "🔷🟢" {
		t.Fatalf("compact = %#v, want %q", got, "🔷🟢")
	}

	windows, ok := payload["windows"].([]any)
	if !ok || len(windows) != 1 {
		t.Fatalf("windows = %#v, want single window entry", payload["windows"])
	}

	nodes, ok := payload["nodes"].([]any)
	if !ok || len(nodes) != 2 {
		t.Fatalf("nodes = %#v, want 2 nodes", payload["nodes"])
	}

	nodeByName := make(map[string]map[string]any)
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("node entry = %#v, want object", raw)
		}
		name, _ := node["name"].(string)
		nodeByName[name] = node
	}

	if got := nodeByName["worker"]["visible_state"]; got != "pending" {
		t.Fatalf("worker visible_state = %#v, want %q", got, "pending")
	}
	if got := nodeByName["critic"]["visible_state"]; got != "ready" {
		t.Fatalf("critic visible_state = %#v, want %q", got, "ready")
	}
}

func TestCollectSessionHealth_ExpectedAIPaneWithoutPositiveEvidenceStaysInitial(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "20260407-ctx"
	sessionName := "review"
	sessionDir := filepath.Join(tmpDir, contextID, sessionName)

	t.Setenv("POSTMAN_HOME", tmpDir)

	if err := os.MkdirAll(filepath.Join(sessionDir, "inbox", "worker"), 0o755); err != nil {
		t.Fatalf("MkdirAll(worker inbox): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  \"list-panes -a -F\"*)\n" +
		"    printf '%s\\n' '%11\t" + contextID + "\t" + sessionName + "\tworker'\n" +
		"    ;;\n" +
		"  \"list-windows -t " + sessionName + "\"*)\n" +
		"    printf '%s\\n' '0'\n" +
		"    ;;\n" +
		"  \"list-panes -t " + sessionName + ":0\"*)\n" +
		"    printf '%s\\n' '0\t0\t%11\tworker\tclaude'\n" +
		"    ;;\n" +
		"  *)\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	health, err := collectSessionHealthLegacy(tmpDir, contextID, sessionName, &config.Config{
		Edges: []string{"worker --- critic"},
	})
	if err != nil {
		t.Fatalf("collectSessionHealthLegacy: %v", err)
	}

	if health.VisibleState != "initial" {
		t.Fatalf("health.VisibleState = %q, want initial for expected AI pane without evidence", health.VisibleState)
	}
	if health.Compact != "🔘" {
		t.Fatalf("health.Compact = %q, want neutral initial mark", health.Compact)
	}
	if len(health.Nodes) != 1 {
		t.Fatalf("nodes = %#v, want one worker node", health.Nodes)
	}
	node := health.Nodes[0]
	if node.Name != "worker" || node.CurrentCommand != "claude" {
		t.Fatalf("node = %#v, want worker claude pane", node)
	}
	if node.PaneState != "" || node.VisibleState != "initial" {
		t.Fatalf("node evidence = pane_state %q visible_state %q, want no pane evidence and initial", node.PaneState, node.VisibleState)
	}
}

func TestRunGetSessionStatus_UsesConfigEdgeOrderForNodesAndTMUXOrderForWindows(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "20260406-ctx"
	sessionName := "review"
	sessionDir := filepath.Join(tmpDir, contextID, sessionName)

	t.Setenv("POSTMAN_HOME", tmpDir)

	configPath := filepath.Join(tmpDir, "postman.toml")
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"worker --- critic\"]\n\n[worker]\ntemplate = \"worker\"\nrole = \"worker\"\n\n[critic]\ntemplate = \"critic\"\nrole = \"critic\"\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	for _, dir := range []string{
		filepath.Join(sessionDir, "inbox", "worker"),
		filepath.Join(sessionDir, "inbox", "critic"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	if err := os.WriteFile(
		filepath.Join(tmpDir, contextID, "pane-activity.json"),
		[]byte(`{
  "%11": {"status":"active","lastChangeAt":"2026-04-06T00:00:00Z"},
  "%12": {"status":"active","lastChangeAt":"2026-04-06T00:00:00Z"}
}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(pane-activity.json): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  \"display-message \"*\"#{session_name}\"*) printf '%s\\n' \"" + sessionName + "\" ;;\n" +
		"  \"list-panes -a -F\"*)\n" +
		"    printf '%s\\n' '%11\t" + contextID + "\t" + sessionName + "\tworker' '%12\t" + contextID + "\t" + sessionName + "\tcritic'\n" +
		"    ;;\n" +
		"  \"list-windows -t " + sessionName + "\"*)\n" +
		"    printf '%s\\n' '0' '1'\n" +
		"    ;;\n" +
		"  \"list-panes -t " + sessionName + ":0\"*)\n" +
		"    printf '%s\\n' '0\t0\t%12\tcritic\tclaude'\n" +
		"    ;;\n" +
		"  \"list-panes -t " + sessionName + ":1\"*)\n" +
		"    printf '%s\\n' '1\t0\t%11\tworker\tclaude'\n" +
		"    ;;\n" +
		"  *)\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	legacy, err := collectSessionHealthLegacy(tmpDir, contextID, sessionName, &config.Config{
		Edges: []string{"worker --- critic"},
	})
	if err != nil {
		t.Fatalf("collectSessionHealthLegacy: %v", err)
	}
	appendSessionHealthSnapshot(t, sessionHealthProjectionFixture{
		baseDir:     tmpDir,
		contextID:   contextID,
		sessionName: sessionName,
	}, legacy)

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = oldStdout
	}()

	runErr := RunGetSessionStatus([]string{
		"--config", configPath,
		"--context-id", contextID,
	})
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	if runErr != nil {
		t.Fatalf("RunGetSessionStatus: %v", runErr)
	}

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(stdout): %v", err)
	}

	var payload struct {
		Compact string `json:"compact"`
		Nodes   []struct {
			Name string `json:"name"`
		} `json:"nodes"`
		Windows []struct {
			Index string `json:"index"`
			Nodes []struct {
				Name string `json:"name"`
			} `json:"nodes"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", string(out), err)
	}

	if len(payload.Nodes) != 2 {
		t.Fatalf("nodes = %#v, want 2 nodes", payload.Nodes)
	}
	if payload.Nodes[0].Name != "worker" || payload.Nodes[1].Name != "critic" {
		t.Fatalf("nodes order = %#v, want worker then critic", payload.Nodes)
	}
	if payload.Compact != "🟢:🟢" {
		t.Fatalf("compact = %q, want %q", payload.Compact, "🟢:🟢")
	}

	if len(payload.Windows) != 2 {
		t.Fatalf("windows = %#v, want 2 windows", payload.Windows)
	}
	if payload.Windows[0].Index != "0" || len(payload.Windows[0].Nodes) != 1 || payload.Windows[0].Nodes[0].Name != "critic" {
		t.Fatalf("first window = %#v, want window 0 with critic", payload.Windows[0])
	}
	if payload.Windows[1].Index != "1" || len(payload.Windows[1].Nodes) != 1 || payload.Windows[1].Nodes[0].Name != "worker" {
		t.Fatalf("second window = %#v, want window 1 with worker", payload.Windows[1])
	}
}

func TestCollectAllSessionHealth_ReturnsAggregateCanonicalPayloadInSessionIDOrder(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "20260406-ctx"
	mainSessionDir := filepath.Join(tmpDir, contextID, "main")
	reviewSessionDir := filepath.Join(tmpDir, contextID, "review")

	t.Setenv("POSTMAN_HOME", tmpDir)

	configPath := filepath.Join(tmpDir, "postman.toml")
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"messenger --- critic --- worker\"]\n\n[messenger]\ntemplate = \"messenger\"\nrole = \"messenger\"\n\n[critic]\ntemplate = \"critic\"\nrole = \"critic\"\n\n[worker]\ntemplate = \"worker\"\nrole = \"worker\"\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	for _, dir := range []string{
		filepath.Join(mainSessionDir, "inbox", "messenger"),
		filepath.Join(mainSessionDir, "inbox", "critic"),
		filepath.Join(mainSessionDir, "inbox", "worker"),
		filepath.Join(reviewSessionDir, "inbox", "messenger"),
		filepath.Join(reviewSessionDir, "inbox", "critic"),
		filepath.Join(reviewSessionDir, "inbox", "worker"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	if err := os.WriteFile(
		filepath.Join(tmpDir, contextID, "pane-activity.json"),
		[]byte(`{
  "%11": {"status":"idle","lastChangeAt":"2026-04-06T00:00:00Z"},
  "%12": {"status":"active","lastChangeAt":"2026-04-06T00:00:00Z"},
  "%13": {"status":"active","lastChangeAt":"2026-04-06T00:00:00Z"},
  "%21": {"status":"active","lastChangeAt":"2026-04-06T00:00:00Z"},
  "%22": {"status":"idle","lastChangeAt":"2026-04-06T00:00:00Z"}
}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(pane-activity.json): %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(mainSessionDir, "inbox", "worker", "20260406-000000-s0000-from-boss-to-worker.md"),
		[]byte("body"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(main worker inbox): %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(reviewSessionDir, "inbox", "critic", "20260406-000002-s0000-from-boss-to-critic.md"),
		[]byte("body"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(review critic inbox): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$1 $2 $3\" in\n" +
		"  \"list-sessions -F \"*)\n" +
		"    printf '%s\\n' 'review\t$210' 'main\t$173'\n" +
		"    ;;\n" +
		"  \"list-panes -a -F\")\n" +
		"    printf '%s\\n' '%12\t" + contextID + "\tmain\tworker' '%11\t" + contextID + "\tmain\tcritic' '%13\t" + contextID + "\tmain\tmessenger' '%21\t" + contextID + "\treview\tcritic' '%22\t" + contextID + "\treview\tworker'\n" +
		"    ;;\n" +
		"  \"list-windows -t main\")\n" +
		"    printf '%s\\n' '0' '1'\n" +
		"    ;;\n" +
		"  \"list-panes -t main:0\")\n" +
		"    printf '%s\\n' '0\t0\t%12\tworker\tclaude' '0\t1\t%11\tcritic\tclaude'\n" +
		"    ;;\n" +
		"  \"list-panes -t main:1\")\n" +
		"    printf '%s\\n' '1\t0\t%13\tmessenger\tclaude'\n" +
		"    ;;\n" +
		"  \"list-windows -t review\")\n" +
		"    printf '%s\\n' '0'\n" +
		"    ;;\n" +
		"  \"list-panes -t review:0\")\n" +
		"    printf '%s\\n' '0\t0\t%22\tworker\tclaude' '0\t1\t%21\tcritic\tclaude'\n" +
		"    ;;\n" +
		"  *)\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	legacy, _, ok, err := collectAllSessionHealthLegacy(contextID, "", configPath)
	if err != nil {
		t.Fatalf("collectAllSessionHealthLegacy: %v", err)
	}
	if !ok {
		t.Fatal("collectAllSessionHealthLegacy reported no active context")
	}
	appendAllSessionHealthSnapshots(t, tmpDir, contextID, legacy.Sessions)

	payload, _, ok, err := collectAllSessionHealth(contextID, "", configPath)
	if err != nil {
		t.Fatalf("collectAllSessionHealth: %v", err)
	}
	if !ok {
		t.Fatal("collectAllSessionHealth reported no active context")
	}
	if payload.ContextID != contextID {
		t.Fatalf("context_id = %q, want %q", payload.ContextID, contextID)
	}
	if len(payload.Sessions) != 2 {
		t.Fatalf("sessions = %#v, want 2 sessions", payload.Sessions)
	}
	if payload.Sessions[0].SessionName != "main" || payload.Sessions[1].SessionName != "review" {
		t.Fatalf("session order = %#v, want main then review to match numeric tmux session_id order", payload.Sessions)
	}
	if payload.Sessions[0].Compact != "🔷🟢:🟢" {
		t.Fatalf("main compact = %q, want %q", payload.Sessions[0].Compact, "🔷🟢:🟢")
	}
	if payload.Sessions[1].Compact != "🟢🔷" {
		t.Fatalf("review compact = %q, want %q", payload.Sessions[1].Compact, "🟢🔷")
	}
}

func TestCollectAllSessionHealth_IncludesSessionsWithoutCanonicalPanesInSessionIDOrder(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "20260406-ctx"
	ghostSessionDir := filepath.Join(tmpDir, contextID, "ghost")
	mainSessionDir := filepath.Join(tmpDir, contextID, "main")

	t.Setenv("POSTMAN_HOME", tmpDir)

	configPath := filepath.Join(tmpDir, "postman.toml")
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"messenger --- worker\"]\n\n[messenger]\ntemplate = \"messenger\"\nrole = \"messenger\"\n\n[worker]\ntemplate = \"worker\"\nrole = \"worker\"\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	for _, dir := range []string{
		filepath.Join(ghostSessionDir, "inbox", "messenger"),
		filepath.Join(ghostSessionDir, "inbox", "worker"),
		filepath.Join(mainSessionDir, "inbox", "messenger"),
		filepath.Join(mainSessionDir, "inbox", "worker"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	if err := os.WriteFile(
		filepath.Join(tmpDir, contextID, "pane-activity.json"),
		[]byte(`{
  "%11": {"status":"active","lastChangeAt":"2026-04-06T00:00:00Z"},
  "%12": {"status":"idle","lastChangeAt":"2026-04-06T00:00:00Z"}
}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(pane-activity.json): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$1 $2 $3\" in\n" +
		"  \"list-sessions -F \"*)\n" +
		"    printf '%s\\n' 'ghost\t$210' 'main\t$173'\n" +
		"    ;;\n" +
		"  \"list-panes -a -F\")\n" +
		"    printf '%s\\n' '%11\t" + contextID + "\tmain\tworker' '%12\t" + contextID + "\tmain\tmessenger'\n" +
		"    ;;\n" +
		"  \"list-windows -t ghost\")\n" +
		"    printf ''\n" +
		"    ;;\n" +
		"  \"list-windows -t main\")\n" +
		"    printf '%s\\n' '0'\n" +
		"    ;;\n" +
		"  \"list-panes -t main:0\")\n" +
		"    printf '%s\\n' '0\t0\t%11\tworker\tclaude' '0\t1\t%12\tmessenger\tclaude'\n" +
		"    ;;\n" +
		"  *)\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	legacy, _, ok, err := collectAllSessionHealthLegacy(contextID, "", configPath)
	if err != nil {
		t.Fatalf("collectAllSessionHealthLegacy: %v", err)
	}
	if !ok {
		t.Fatal("collectAllSessionHealthLegacy reported no active context")
	}
	appendAllSessionHealthSnapshots(t, tmpDir, contextID, legacy.Sessions)

	payload, _, ok, err := collectAllSessionHealth(contextID, "", configPath)
	if err != nil {
		t.Fatalf("collectAllSessionHealth: %v", err)
	}
	if !ok {
		t.Fatal("collectAllSessionHealth reported no active context")
	}
	if len(payload.Sessions) != 2 {
		t.Fatalf("sessions = %#v, want main then ghost to preserve numeric tmux session_id order", payload.Sessions)
	}
	if payload.Sessions[0].SessionName != "main" || payload.Sessions[1].SessionName != "ghost" {
		t.Fatalf("session order = %#v, want main then ghost", payload.Sessions)
	}
	if payload.Sessions[0].Compact != "🟢🟢" {
		t.Fatalf("main compact = %q, want %q", payload.Sessions[0].Compact, "🟢🟢")
	}
	if payload.Sessions[1].VisibleState != "initial" {
		t.Fatalf("ghost visible_state = %q, want %q", payload.Sessions[1].VisibleState, "initial")
	}
	if payload.Sessions[1].Compact != "🔘" {
		t.Fatalf("ghost compact = %q, want %q", payload.Sessions[1].Compact, "🔘")
	}
}
