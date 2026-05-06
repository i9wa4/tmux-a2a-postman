package main

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/cli"
	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func TestFilterToUINode(t *testing.T) {
	makeNodes := func(names ...string) map[string]discovery.NodeInfo {
		m := make(map[string]discovery.NodeInfo, len(names))
		for _, n := range names {
			m[n] = discovery.NodeInfo{SessionName: "s"}
		}
		return m
	}
	cases := []struct {
		name      string
		nodes     map[string]discovery.NodeInfo
		uiNode    string
		wantKeys  []string
		wantEmpty bool
	}{
		{
			name:     "uiNode empty returns all",
			nodes:    makeNodes("s:messenger", "s:worker", "s:critic"),
			uiNode:   "",
			wantKeys: []string{"s:messenger", "s:worker", "s:critic"},
		},
		{
			name:     "uiNode found returns only match",
			nodes:    makeNodes("s:messenger", "s:worker", "s:critic"),
			uiNode:   "messenger",
			wantKeys: []string{"s:messenger"},
		},
		{
			name:      "uiNode not found returns empty",
			nodes:     makeNodes("s:worker", "s:critic"),
			uiNode:    "messenger",
			wantEmpty: true,
		},
		{
			name:      "nil input map returns empty",
			nodes:     nil,
			uiNode:    "messenger",
			wantEmpty: true,
		},
		{
			name:     "no-colon node name matched by simple name",
			nodes:    makeNodes("messenger", "worker"),
			uiNode:   "messenger",
			wantKeys: []string{"messenger"},
		},
		{
			name:     "multi-session multi-match returns all matching entries",
			nodes:    makeNodes("s1:messenger", "s2:messenger", "s1:worker"),
			uiNode:   "messenger",
			wantKeys: []string{"s1:messenger", "s2:messenger"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cliutil.FilterToUINode(tc.nodes, tc.uiNode)
			if tc.wantEmpty {
				if len(got) != 0 {
					t.Errorf("want empty map, got %v", got)
				}
				return
			}
			if len(got) != len(tc.wantKeys) {
				t.Errorf("len = %d, want %d; got keys: %v", len(got), len(tc.wantKeys), got)
				return
			}
			for _, k := range tc.wantKeys {
				if _, ok := got[k]; !ok {
					t.Errorf("missing key %q in result %v", k, got)
				}
			}
		})
	}
}

func TestSplitCommand_RequiresExplicitSubcommand(t *testing.T) {
	command, args, ok := splitCommand(nil)
	if ok {
		t.Fatalf("splitCommand(nil) ok = true, want false (command=%q args=%v)", command, args)
	}

	command, args, ok = splitCommand([]string{"send", "--to", "worker"})
	if !ok {
		t.Fatal("splitCommand returned ok = false, want true")
	}
	if command != "send" {
		t.Fatalf("command = %q, want send", command)
	}
	if len(args) != 2 || args[0] != "--to" || args[1] != "worker" {
		t.Fatalf("args = %v, want [--to worker]", args)
	}
}

func TestPrintUsage_ShowsReducedPublicSurface(t *testing.T) {
	var stderr bytes.Buffer
	var helpOverview bytes.Buffer

	printUsage(&stderr)
	if err := cli.WriteHelp(&helpOverview, io.Discard, nil); err != nil {
		t.Fatalf("WriteHelp: %v", err)
	}

	got := stderr.String()
	if got != helpOverview.String() {
		t.Fatalf("usage should be the help overview SSOT\nusage:\n%s\nhelp:\n%s", got, helpOverview.String())
	}
	if !strings.Contains(got, "Use an explicit command. Bare `tmux-a2a-postman` prints usage; it does not start the daemon.") {
		t.Fatalf("usage missing explicit-subcommand guidance: %q", got)
	}
	if !strings.Contains(got, "get-status                 Print canonical session health JSON") {
		t.Fatalf("usage missing get-status command: %q", got)
	}
	if !strings.Contains(got, "get-status-oneline         Print compact all-session health") {
		t.Fatalf("usage missing get-status-oneline command: %q", got)
	}
	if !strings.Contains(got, "version                    Print the build version JSON") {
		t.Fatalf("usage missing version command: %q", got)
	}
	if !strings.Contains(got, "inspect-message            Inspect persisted message content by id") {
		t.Fatalf("usage missing inspect-message command: %q", got)
	}
	for _, hidden := range []string{" status ", " read ", " todo ", "timeline", "replay", "schema", "bind", "supervisor-drain"} {
		if strings.Contains(got, hidden) {
			t.Fatalf("usage exposes hidden surface %q: %q", hidden, got)
		}
	}
	if strings.Contains(got, "Start tmux-a2a-postman daemon (default)") {
		t.Fatalf("usage still claims start is the default: %q", got)
	}
	if strings.Contains(got, "--context-id") {
		t.Fatalf("usage still exposes hidden context override: %q", got)
	}
	for _, removed := range []string{"--no-tui", "--log-file", "--base-dir", "--state-home"} {
		if strings.Contains(got, removed) {
			t.Fatalf("usage still exposes removed root flag %s: %q", removed, got)
		}
	}
	if strings.Contains(got, "get-context-id") {
		t.Fatalf("usage still exposes get-context-id in the default operator surface: %q", got)
	}
}
