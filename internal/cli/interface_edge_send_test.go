package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

// #624/#700 A4: ONE table drives the CLI pre-check (RunSendHeredoc path) and
// the authoritative delivery (message.DeliverMessage) with the same
// configuration and the same live view, and asserts both the expected decision
// and that the CLI never allows what delivery denies. Sessions top <- owner <-
// repo; the daemon's own session is "owner". There are no static edges, so any
// success is a derived-edge success.

const ifaceEdgeContext = "ctx-iface-edge"

type ifaceEdgeCase struct {
	name        string
	global      string // [postman] interface_node
	declares    map[string]string
	fromSession string
	fromNode    string
	to          string // explicit session:node or an alias
	compiledTo  string // the session:node the alias/explicit address compiles to
	notOwned    string // session reported as not owned
	absentNode  string // address missing from live discovery (both paths)
	notEnabled  string // session with no enabled marker (both paths deny)
	collision   string // address claimed by two live panes (both paths deny)
	// cliOnlyNotEnabled / cliOnlyAbsentNode model a CLI view that is STRICTER
	// than delivery: the CLI refuses early while delivery would have allowed it.
	// The only forbidden direction is CLI allow with delivery deny (#624/#700
	// F-700-R2-3).
	cliOnlyNotEnabled string
	cliOnlyAbsentNode string
	wantAllowed       bool
}

func ifaceEdgeTOML(c ifaceEdgeCase) string {
	var b strings.Builder
	b.WriteString("[postman]\n")
	if c.global != "" {
		fmt.Fprintf(&b, "interface_node = %q\n", c.global)
	}
	b.WriteString("edges = []\n\n[mouthpiece]\nrole = \"interface\"\n\n[worker]\nrole = \"worker\"\n\n")
	for _, level := range []struct{ session, parent string }{{"top", ""}, {"owner", "top"}, {"repo", "owner"}} {
		b.WriteString("[[postman.workspace_tree]]\n")
		fmt.Fprintf(&b, "session = %q\nrepresentative = \"mouthpiece\"\n", level.session)
		if level.parent != "" {
			fmt.Fprintf(&b, "parent = %q\n", level.parent)
		}
		if declared, ok := c.declares[level.session]; ok && declared != "" {
			fmt.Fprintf(&b, "interface_node = %q\n", declared)
		}
		b.WriteString("\n")
	}
	return b.String()
}

func TestInterfaceEdge_CLIAndDeliveryAgreeOnOneTable(t *testing.T) {
	allDeclared := map[string]string{"top": "mouthpiece", "owner": "mouthpiece", "repo": "mouthpiece"}
	without := func(session string) map[string]string {
		m := map[string]string{}
		for k, v := range allDeclared {
			if k != session {
				m[k] = v
			}
		}
		return m
	}
	cases := []ifaceEdgeCase{
		{name: "owner to parent explicit", global: "mouthpiece", declares: allDeclared, fromSession: "owner", fromNode: "mouthpiece", to: "top:mouthpiece", compiledTo: "top:mouthpiece", wantAllowed: true},
		{name: "owner to parent by alias", global: "mouthpiece", declares: allDeclared, fromSession: "owner", fromNode: "mouthpiece", to: "@parent", compiledTo: "top:mouthpiece", wantAllowed: true},
		{name: "owner to child by alias", global: "mouthpiece", declares: allDeclared, fromSession: "owner", fromNode: "mouthpiece", to: "@child/repo", compiledTo: "repo:mouthpiece", wantAllowed: true},
		{name: "top to child", global: "mouthpiece", declares: allDeclared, fromSession: "top", fromNode: "mouthpiece", to: "owner:mouthpiece", compiledTo: "owner:mouthpiece", wantAllowed: true},
		{name: "own entry omits its declaration", global: "mouthpiece", declares: without("owner"), fromSession: "owner", fromNode: "mouthpiece", to: "top:mouthpiece", compiledTo: "top:mouthpiece", wantAllowed: true},
		{name: "transitive top to repo", global: "mouthpiece", declares: allDeclared, fromSession: "top", fromNode: "mouthpiece", to: "repo:mouthpiece", compiledTo: "repo:mouthpiece"},
		{name: "peer has not declared", global: "mouthpiece", declares: without("top"), fromSession: "owner", fromNode: "mouthpiece", to: "top:mouthpiece", compiledTo: "top:mouthpiece"},
		{name: "global interface_node empty", global: "", declares: allDeclared, fromSession: "owner", fromNode: "mouthpiece", to: "top:mouthpiece", compiledTo: "top:mouthpiece"},
		{name: "sender is not the declared interface node", global: "mouthpiece", declares: allDeclared, fromSession: "owner", fromNode: "worker", to: "top:mouthpiece", compiledTo: "top:mouthpiece"},
		{name: "recipient session not owned", global: "mouthpiece", declares: allDeclared, fromSession: "owner", fromNode: "mouthpiece", to: "top:mouthpiece", compiledTo: "top:mouthpiece", notOwned: "top"},
		{name: "recipient node gone", global: "mouthpiece", declares: allDeclared, fromSession: "owner", fromNode: "mouthpiece", to: "top:mouthpiece", compiledTo: "top:mouthpiece", absentNode: "top:mouthpiece"},
		{name: "own entry disagrees with the global", global: "mouthpiece", declares: map[string]string{"top": "mouthpiece", "owner": "desk", "repo": "mouthpiece"}, fromSession: "owner", fromNode: "mouthpiece", to: "top:mouthpiece", compiledTo: "top:mouthpiece"},
		// F-700-R2-1: a real duplicate live pane is a collision, never a unique node.
		{name: "recipient node claimed by two live panes", global: "mouthpiece", declares: allDeclared, fromSession: "owner", fromNode: "mouthpiece", to: "top:mouthpiece", compiledTo: "top:mouthpiece", collision: "top:mouthpiece"},
		// F-700-R2-3: owned-but-disabled session denies on both paths.
		{name: "recipient session owned but disabled", global: "mouthpiece", declares: allDeclared, fromSession: "owner", fromNode: "mouthpiece", to: "top:mouthpiece", compiledTo: "top:mouthpiece", notEnabled: "top"},
		// F-700-R2-3 asymmetric: the CLI is stricter than delivery, never looser.
		{name: "CLI sees session disabled, delivery enabled", global: "mouthpiece", declares: allDeclared, fromSession: "owner", fromNode: "mouthpiece", to: "top:mouthpiece", compiledTo: "top:mouthpiece", cliOnlyNotEnabled: "top", wantAllowed: true},
		{name: "CLI sees foreign-claimed node absent, delivery has it", global: "mouthpiece", declares: allDeclared, fromSession: "owner", fromNode: "mouthpiece", to: "top:mouthpiece", compiledTo: "top:mouthpiece", cliOnlyAbsentNode: "top:mouthpiece", wantAllowed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Chdir(home)
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", home)
			configPath := filepath.Join(home, "postman.toml")
			if err := os.WriteFile(configPath, []byte(ifaceEdgeTOML(tc)), 0o600); err != nil {
				t.Fatalf("WriteFile config: %v", err)
			}
			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				t.Fatalf("LoadConfig: %v", err)
			}

			nodes := map[string]discovery.NodeInfo{}
			cliNodes := map[string]discovery.NodeInfo{}
			for _, session := range []string{"top", "owner", "repo"} {
				dir := filepath.Join(home, ifaceEdgeContext, session)
				if err := config.CreateSessionDirs(dir); err != nil {
					t.Fatalf("CreateSessionDirs(%s): %v", session, err)
				}
				for _, node := range []string{"mouthpiece", "worker"} {
					address := session + ":" + node
					if address == tc.absentNode {
						continue
					}
					info := discovery.NodeInfo{PaneID: "%" + session + node, SessionName: session, SessionDir: dir, Collision: address == tc.collision}
					nodes[address] = info
					if address != tc.cliOnlyAbsentNode {
						cliNodes[address] = info
					}
				}
			}
			owns := func(_, _, session string) bool { return session != tc.notOwned }
			deliveryEnabled := func(session string) bool { return session != tc.notEnabled }
			defer message.SetContextOwnsSessionForTest(owns)()

			installFakeTmuxForCLI(t, home, tc.fromSession, tc.fromNode)
			ctx := commandContext{
				stdin:              strings.NewReader("derived edge body"),
				stdinIsTerminal:    func(io.Reader) bool { return false },
				contextOwnsSession: owns,
				sessionEnabledMarker: func(_, _, session string) bool {
					return session != tc.notEnabled && session != tc.cliOnlyNotEnabled
				},
				discoverNodes: func(string, string, string) (map[string]discovery.NodeInfo, error) {
					return cliNodes, nil
				},
				daemonSessionName: func(string, string) string { return "owner" },
			}.withDefaults()
			sendErr := runSendHeredocWithContext(ctx, []string{"--config", configPath, "--context-id", ifaceEdgeContext, "--to", tc.to})
			cliAllowed := sendErr == nil

			// Delivery: the CLI's own post when it allowed; otherwise a
			// handcrafted post for the same compiled recipient, so delivery
			// decides independently of the CLI's pre-check.
			postDir := filepath.Join(home, ifaceEdgeContext, tc.fromSession, "post")
			var postPath string
			if cliAllowed {
				entries, err := os.ReadDir(postDir)
				if err != nil || len(entries) != 1 {
					t.Fatalf("CLI allowed but post/ has %v (err %v), want exactly one", entries, err)
				}
				postPath = filepath.Join(postDir, entries[0].Name())
			} else {
				if entries, _ := os.ReadDir(postDir); len(entries) != 0 {
					t.Fatalf("CLI denied but left %d post file(s)", len(entries))
				}
				name := "20260709-120000-r0001-from-" + tc.fromNode + "-to-" + tc.compiledTo + ".md"
				body := "---\nparams:\n  contextId: " + ifaceEdgeContext + "\n  from: " + tc.fromNode + "\n  to: " + tc.compiledTo + "\n  timestamp: 2026-07-09T12:00:00Z\n---\n\nhandcrafted\n"
				postPath = filepath.Join(postDir, name)
				if err := os.WriteFile(postPath, []byte(body), 0o644); err != nil {
					t.Fatalf("WriteFile handcrafted post: %v", err)
				}
			}
			if err := message.DeliverMessage(postPath, ifaceEdgeContext, nodes, map[string][]string{}, cfg, deliveryEnabled, nil, idle.NewIdleTracker(), "owner"); err != nil {
				t.Fatalf("DeliverMessage: %v", err)
			}
			recipientSession, recipientNode, _ := strings.Cut(tc.compiledTo, ":")
			_, statErr := os.Stat(filepath.Join(home, ifaceEdgeContext, recipientSession, "inbox", recipientNode, filepath.Base(postPath)))
			deliveryAllowed := statErr == nil

			wantCLI := tc.wantAllowed && tc.cliOnlyNotEnabled == "" && tc.cliOnlyAbsentNode == ""
			if cliAllowed != wantCLI || deliveryAllowed != tc.wantAllowed {
				t.Fatalf("decision: cli=%v delivery=%v, want cli=%v delivery=%v (cli error: %v)", cliAllowed, deliveryAllowed, wantCLI, tc.wantAllowed, sendErr)
			}
			if cliAllowed && !deliveryAllowed {
				t.Fatal("the CLI allowed a send that delivery denied")
			}
		})
	}
}
