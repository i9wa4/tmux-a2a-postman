package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

// F-700-R2-5 (output level): the rendered "You can talk to" footer of a send to
// a BARE same-session recipient lists that recipient's derived interface
// neighbors. worker (session owner) sends to its sibling "mouthpiece" over a
// static edge; mouthpiece's derived neighbor is the parent's top:mouthpiece, and
// the footer is rendered from the recipient's viewpoint.
func TestRunSendHeredoc_FooterIncludesDerivedNeighborsForBareRecipient(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	const contextID = "ctx-footer-derived"
	configPath := filepath.Join(home, "postman.toml")
	toml := "[postman]\ninterface_node = \"mouthpiece\"\nreply_command = \"tmux-a2a-postman send-heredoc --to <recipient>\"\nmessage_footer = \"\"\"You can talk to: {can_talk_to}\n\"\"\"\nedges = [\"worker --- mouthpiece\"]\n\n" +
		"[mouthpiece]\nrole = \"interface\"\n\n[worker]\nrole = \"worker\"\n\n" +
		"[[postman.workspace_tree]]\nsession = \"top\"\nrepresentative = \"mouthpiece\"\ninterface_node = \"mouthpiece\"\n\n" +
		"[[postman.workspace_tree]]\nsession = \"owner\"\nrepresentative = \"mouthpiece\"\nparent = \"top\"\ninterface_node = \"mouthpiece\"\n"
	if err := os.WriteFile(configPath, []byte(toml), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{}
	for _, address := range []string{"top:mouthpiece", "owner:mouthpiece", "owner:worker"} {
		session, _, _ := strings.Cut(address, ":")
		nodes[address] = discovery.NodeInfo{PaneID: "%" + address, SessionName: session, SessionDir: filepath.Join(home, contextID, session)}
	}
	for _, session := range []string{"top", "owner"} {
		if err := config.CreateSessionDirs(filepath.Join(home, contextID, session)); err != nil {
			t.Fatalf("CreateSessionDirs(%s): %v", session, err)
		}
	}
	installFakeTmuxForCLI(t, home, "owner", "worker")
	ctx := commandContext{
		stdin:                strings.NewReader("hello"),
		stdinIsTerminal:      func(io.Reader) bool { return false },
		contextOwnsSession:   func(string, string, string) bool { return true },
		sessionEnabledMarker: func(string, string, string) bool { return true },
		discoverNodes: func(string, string, string) (map[string]discovery.NodeInfo, error) {
			return nodes, nil
		},
		daemonSessionName: func(string, string) string { return "owner" },
	}.withDefaults()

	if err := runSendHeredocWithContext(ctx, []string{"--config", configPath, "--context-id", contextID, "--to", "mouthpiece"}); err != nil {
		t.Fatalf("runSendHeredocWithContext: %v", err)
	}
	postDir := filepath.Join(home, contextID, "owner", "post")
	entries, err := os.ReadDir(postDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("post/ = %v (err %v), want exactly one message", entries, err)
	}
	raw, err := os.ReadFile(filepath.Join(postDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile post: %v", err)
	}
	if !strings.Contains(string(raw), "top:mouthpiece") || !strings.Contains(string(raw), "You can talk to: ") {
		t.Fatalf("footer lacks the derived neighbor top:mouthpiece for the bare recipient:\n%s", raw)
	}
}

// F-700-R2-2 (CLI path): the verdict-debt exemption of the configured interface
// node is bound to the daemon's own session. The sender "mouthpiece" in session
// "peer" has overdue verdict debt; it is exempt only when "peer" is the daemon's
// own session. A same-bare-named interface node in a peer session is gated like
// any other requester.
func TestRunSendHeredoc_VerdictDebtExemptionBoundToOwnSession(t *testing.T) {
	for _, tc := range []struct {
		name       string
		daemonSess string
		wantErr    bool
	}{
		{name: "own session interface node stays exempt", daemonSess: "peer", wantErr: false},
		{name: "peer session same bare name is not exempt", daemonSess: "owner", wantErr: true},
		{name: "unknown daemon session falls back to the sender session", daemonSess: "", wantErr: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Chdir(home)
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", home)
			const contextID = "ctx-verdict-own"
			configPath := filepath.Join(home, "postman.toml")
			toml := "[postman]\ninterface_node = \"mouthpiece\"\nverdict_grace_seconds = 60\nverdict_debt_cap = 0\nedges = [\"mouthpiece --- worker\"]\n\n[mouthpiece]\nrole = \"interface\"\n\n[worker]\nrole = \"worker\"\n"
			if err := os.WriteFile(configPath, []byte(toml), 0o600); err != nil {
				t.Fatalf("WriteFile config: %v", err)
			}
			sessionDir := filepath.Join(home, contextID, "peer")
			if err := config.CreateSessionDirs(sessionDir); err != nil {
				t.Fatalf("CreateSessionDirs: %v", err)
			}
			appendSendVerdictDebt(t, sessionDir, "peer", "mouthpiece", "worker", "ireq_cli_own", time.Now().Add(-2*time.Hour).UTC())

			nodes := map[string]discovery.NodeInfo{
				"peer:mouthpiece": {PaneID: "%1", SessionName: "peer", SessionDir: sessionDir},
				"peer:worker":     {PaneID: "%2", SessionName: "peer", SessionDir: sessionDir},
			}
			installFakeTmuxForCLI(t, home, "peer", "mouthpiece")
			ctx := commandContext{
				stdin:              strings.NewReader("needs a decision"),
				stdinIsTerminal:    func(io.Reader) bool { return false },
				contextOwnsSession: func(string, string, string) bool { return false },
				discoverNodes: func(string, string, string) (map[string]discovery.NodeInfo, error) {
					return nodes, nil
				},
				daemonSessionName: func(string, string) string { return tc.daemonSess },
			}.withDefaults()

			err := runSendHeredocWithContext(ctx, []string{"--config", configPath, "--context-id", contextID, "--to", "worker", "--reply-required"})
			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "reply gate: verdict required") {
					t.Fatalf("err = %v, want a verdict-gate rejection for a non-exempt peer-session interface node", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v, want the own-session interface node exempt from verdict debt", err)
			}
		})
	}
}
