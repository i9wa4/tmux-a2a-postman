package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
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
