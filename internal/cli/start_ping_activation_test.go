package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func TestActivateSessionForPing_ActivatesUnownedForeignSession(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "state")
	contextID := "ctx-self"
	selfSession := "tmux-a2a-postman"
	targetSession := "dotfiles"
	contextDir := filepath.Join(baseDir, contextID)
	if err := config.CreateMultiSessionDirs(contextDir, selfSession); err != nil {
		t.Fatalf("CreateMultiSessionDirs(self): %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Edges = []string{"dotfiles:orchestrator -- dotfiles:messenger"}

	scriptDir := t.TempDir()
	logPath := filepath.Join(root, "tmux.log")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"LOGFILE='" + logPath + "'\n" +
		"case \"$1 $2 $3 $4 $5 $6\" in\n" +
		"  'list-panes -s -t dotfiles -F #{pane_id} #{pane_title}')\n" +
		"    printf '%s\\n' '%704 messenger' '%705 orchestrator'\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"  'list-panes -a -F #{pane_id}\t#{@a2a_context_id}\t#{session_name}\t#{pane_title} ')\n" +
		"    ;;\n" +
		"esac\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ]; then\n" +
		"  printf '%s\\n' '%704\t\tdotfiles\tmessenger' '%705\t\tdotfiles\torchestrator'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'set-option' ] && [ \"$2\" = '-g' ]; then\n" +
		"  printf '%s\\n' \"$*\" >> \"$LOGFILE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'set-option' ] && [ \"$2\" = '-p' ]; then\n" +
		"  printf '%s\\n' \"$*\" >> \"$LOGFILE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := activateSessionForPing(baseDir, contextDir, contextID, selfSession, targetSession, cfg, nil, nil)
	if err != nil {
		t.Fatalf("activateSessionForPing() error = %v", err)
	}

	if _, ok := got["dotfiles:messenger"]; !ok {
		t.Fatalf("activateSessionForPing() missing dotfiles:messenger in %v", got)
	}
	if _, ok := got["dotfiles:orchestrator"]; !ok {
		t.Fatalf("activateSessionForPing() missing dotfiles:orchestrator in %v", got)
	}
	if _, err := os.Stat(filepath.Join(contextDir, targetSession, "inbox")); err != nil {
		t.Fatalf("dotfiles inbox dir not created: %v", err)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(tmux log): %v", err)
	}
	logText := string(logBytes)
	for _, want := range []string{
		"set-option -g @a2a_session_on_dotfiles ctx-self:",
		"set-option -p -t %704 @a2a_context_id ctx-self",
		"set-option -p -t %705 @a2a_context_id ctx-self",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("tmux pre-claim log missing %q: %q", want, logText)
		}
	}
}

func TestActivateStartupSessions_MakesForeignSessionDiscoverableAndOwned(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "state")
	contextID := "ctx-self"
	selfSession := "0"
	targetSession := "tmux-a2a-postman"
	contextDir := filepath.Join(baseDir, contextID)
	if err := config.CreateMultiSessionDirs(contextDir, selfSession); err != nil {
		t.Fatalf("CreateMultiSessionDirs(self): %v", err)
	}
	if err := os.WriteFile(filepath.Join(contextDir, selfSession, "postman.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile(postman.pid): %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Edges = []string{"messenger -- orchestrator"}

	scriptDir := t.TempDir()
	logPath := filepath.Join(root, "tmux.log")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"LOGFILE='" + logPath + "'\n" +
		"if [ \"$1\" = 'list-sessions' ] && [ \"$2\" = '-F' ]; then\n" +
		"  printf '%s\\n' '0\t$0' 'tmux-a2a-postman\t$1'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-s' ] && [ \"$3\" = '-t' ] && [ \"$4\" = 'tmux-a2a-postman' ]; then\n" +
		"  printf '%s\\n' '%132 messenger' '%133 orchestrator'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'show-options' ] && [ \"$2\" = '-gqv' ] && [ \"$3\" = '@a2a_session_on_tmux-a2a-postman' ]; then\n" +
		"  printf '%s\\n' 'ctx-self:12345'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ]; then\n" +
		"  printf '%s\\n' '%132\tctx-self\ttmux-a2a-postman\tmessenger' '%133\tctx-self\ttmux-a2a-postman\torchestrator'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'set-option' ] && [ \"$2\" = '-g' ]; then\n" +
		"  printf '%s\\n' \"$*\" >> \"$LOGFILE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'set-option' ] && [ \"$2\" = '-p' ]; then\n" +
		"  printf '%s\\n' \"$*\" >> \"$LOGFILE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	activated := activateStartupSessions(baseDir, contextDir, contextID, selfSession, cfg)
	if len(activated) != 1 || activated[0] != targetSession {
		t.Fatalf("activateStartupSessions() = %v, want [%s]", activated, targetSession)
	}

	resolvedContextID, err := config.ResolveContextIDFromSession(baseDir, targetSession)
	if err != nil {
		t.Fatalf("ResolveContextIDFromSession(%q): %v", targetSession, err)
	}
	if resolvedContextID != contextID {
		t.Fatalf("ResolveContextIDFromSession(%q) = %q, want %q", targetSession, resolvedContextID, contextID)
	}

	nodes, _, err := discovery.DiscoverNodesWithCollisions(baseDir, contextID, selfSession)
	if err != nil {
		t.Fatalf("DiscoverNodesWithCollisions: %v", err)
	}
	if _, ok := nodes["tmux-a2a-postman:messenger"]; !ok {
		t.Fatalf("DiscoverNodesWithCollisions missing tmux-a2a-postman:messenger in %v", nodes)
	}
	if _, ok := nodes["tmux-a2a-postman:orchestrator"]; !ok {
		t.Fatalf("DiscoverNodesWithCollisions missing tmux-a2a-postman:orchestrator in %v", nodes)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(tmux log): %v", err)
	}
	logText := string(logBytes)
	for _, want := range []string{
		"set-option -g @a2a_session_on_tmux-a2a-postman ctx-self:",
		"set-option -p -t %132 @a2a_context_id ctx-self",
		"set-option -p -t %133 @a2a_context_id ctx-self",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("tmux startup activation log missing %q: %q", want, logText)
		}
	}
}

func TestActivateStartupSessions_UsesConfiguredNodeNamesWhenEdgesAreEmpty(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "state")
	contextID := "ctx-self"
	selfSession := "0"
	targetSession := "tmux-a2a-postman"
	contextDir := filepath.Join(baseDir, contextID)
	if err := config.CreateMultiSessionDirs(contextDir, selfSession); err != nil {
		t.Fatalf("CreateMultiSessionDirs(self): %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Nodes["messenger"] = config.NodeConfig{}
	cfg.Nodes["orchestrator"] = config.NodeConfig{}

	scriptDir := t.TempDir()
	logPath := filepath.Join(root, "tmux.log")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"LOGFILE='" + logPath + "'\n" +
		"if [ \"$1\" = 'list-sessions' ] && [ \"$2\" = '-F' ]; then\n" +
		"  printf '%s\\n' '0\t$0' 'tmux-a2a-postman\t$1'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-s' ] && [ \"$3\" = '-t' ] && [ \"$4\" = 'tmux-a2a-postman' ]; then\n" +
		"  printf '%s\\n' '%132 messenger' '%133 orchestrator' '%134 unrelated'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'set-option' ] && [ \"$2\" = '-g' ]; then\n" +
		"  printf '%s\\n' \"$*\" >> \"$LOGFILE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'set-option' ] && [ \"$2\" = '-p' ]; then\n" +
		"  printf '%s\\n' \"$*\" >> \"$LOGFILE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	activated := activateStartupSessions(baseDir, contextDir, contextID, selfSession, cfg)
	if len(activated) != 1 || activated[0] != targetSession {
		t.Fatalf("activateStartupSessions() = %v, want [%s]", activated, targetSession)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile(tmux log): %v", err)
	}
	logText := string(logBytes)
	for _, want := range []string{
		"set-option -p -t %132 @a2a_context_id ctx-self",
		"set-option -p -t %133 @a2a_context_id ctx-self",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("tmux startup activation log missing %q: %q", want, logText)
		}
	}
	if strings.Contains(logText, "set-option -p -t %134 @a2a_context_id ctx-self") {
		t.Fatalf("unexpected non-node pane was pre-claimed: %q", logText)
	}
}

func TestFilterDiscoveredActivationNodes_PreservesSessionPrefixedKeys(t *testing.T) {
	filtered := filterDiscoveredActivationNodes(map[string]discovery.NodeInfo{
		"dotfiles:messenger":    {},
		"dotfiles:orchestrator": {},
		"review:critic":         {},
	}, map[string]bool{
		"dotfiles:messenger":    true,
		"dotfiles:orchestrator": true,
	})

	if _, ok := filtered["dotfiles:messenger"]; !ok {
		t.Fatal("expected session-prefixed sender node to remain after edge filtering")
	}
	if _, ok := filtered["dotfiles:orchestrator"]; !ok {
		t.Fatal("expected session-prefixed recipient node to remain after edge filtering")
	}
	if _, ok := filtered["review:critic"]; ok {
		t.Fatal("unexpected unrelated node remained after edge filtering")
	}
}

func TestActivateSessionForPing_RejectsForeignOwnedSession(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "state")
	contextID := "ctx-self"
	selfSession := "tmux-a2a-postman"
	targetSession := "dotfiles"
	contextDir := filepath.Join(baseDir, contextID)
	if err := config.CreateMultiSessionDirs(contextDir, selfSession); err != nil {
		t.Fatalf("CreateMultiSessionDirs(self): %v", err)
	}

	ownerContext := "ctx-owner"
	ownerDir := filepath.Join(baseDir, ownerContext, targetSession)
	if err := os.MkdirAll(ownerDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(ownerDir): %v", err)
	}
	if err := os.WriteFile(filepath.Join(ownerDir, "postman.pid"), []byte("1"), 0o600); err != nil {
		t.Fatalf("WriteFile(postman.pid): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg := config.DefaultConfig()
	cfg.Edges = []string{"orchestrator -- messenger"}

	_, err := activateSessionForPing(baseDir, contextDir, contextID, selfSession, targetSession, cfg, nil, nil)
	if err == nil {
		t.Fatal("activateSessionForPing() error = nil, want ownership rejection")
	}
	if !errors.Is(err, errPingSessionOwned) {
		t.Fatalf("activateSessionForPing() error = %v, want errPingSessionOwned", err)
	}
	if _, err := os.Stat(filepath.Join(contextDir, targetSession)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("self dotfiles dir exists unexpectedly: %v", err)
	}
}

func TestActivateSessionForPing_PreservesBareEdgeKeys(t *testing.T) {
	root := t.TempDir()
	baseDir := filepath.Join(root, "state")
	contextID := "ctx-self"
	selfSession := "tmux-a2a-postman"
	targetSession := "dotfiles"
	contextDir := filepath.Join(baseDir, contextID)
	if err := config.CreateMultiSessionDirs(contextDir, selfSession); err != nil {
		t.Fatalf("CreateMultiSessionDirs(self): %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Edges = []string{"orchestrator -- messenger"}

	scriptDir := t.TempDir()
	logPath := filepath.Join(root, "tmux.log")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"LOGFILE='" + logPath + "'\n" +
		"case \"$1 $2 $3 $4 $5 $6\" in\n" +
		"  'list-panes -s -t dotfiles -F #{pane_id} #{pane_title}')\n" +
		"    printf '%s\\n' '%704 messenger' '%705 orchestrator'\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"  'list-panes -a -F #{pane_id}\t#{@a2a_context_id}\t#{session_name}\t#{pane_title} ')\n" +
		"    ;;\n" +
		"esac\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ]; then\n" +
		"  printf '%s\\n' '%704\t\tdotfiles\tmessenger' '%705\t\tdotfiles\torchestrator'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'set-option' ] && [ \"$2\" = '-g' ]; then\n" +
		"  printf '%s\\n' \"$*\" >> \"$LOGFILE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'set-option' ] && [ \"$2\" = '-p' ]; then\n" +
		"  printf '%s\\n' \"$*\" >> \"$LOGFILE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := activateSessionForPing(baseDir, contextDir, contextID, selfSession, targetSession, cfg, nil, nil)
	if err != nil {
		t.Fatalf("activateSessionForPing() error = %v", err)
	}
	if _, ok := got["dotfiles:messenger"]; !ok {
		t.Fatalf("activateSessionForPing() missing dotfiles:messenger in %v", got)
	}
	if _, ok := got["dotfiles:orchestrator"]; !ok {
		t.Fatalf("activateSessionForPing() missing dotfiles:orchestrator in %v", got)
	}
}

func TestFilterDiscoveredActivationNodes_PreservesBareKeys(t *testing.T) {
	filtered := filterDiscoveredActivationNodes(map[string]discovery.NodeInfo{
		"dotfiles:messenger":    {},
		"dotfiles:orchestrator": {},
		"review:critic":         {},
	}, map[string]bool{
		"messenger":    true,
		"orchestrator": true,
	})

	if _, ok := filtered["dotfiles:messenger"]; !ok {
		t.Fatal("expected bare-edge sender node to remain after edge filtering")
	}
	if _, ok := filtered["dotfiles:orchestrator"]; !ok {
		t.Fatal("expected bare-edge recipient node to remain after edge filtering")
	}
	if _, ok := filtered["review:critic"]; ok {
		t.Fatal("unexpected unrelated node remained after edge filtering")
	}
}
