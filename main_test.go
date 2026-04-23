package main

import (
	"bytes"
	"flag"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

// --- Issue #351: parseParams / parseShorthand unit tests ---

// TestParseParams verifies parseParams behavior across JSON, shorthand, and edge-case inputs.
func TestParseParams(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantKey string // if non-empty, result must contain this key
		wantVal string // expected value for wantKey
		wantErr string // if non-empty, error must contain this substring
		wantNil bool   // true if result map should be nil (empty/no-op)
	}{
		{
			name:    "json integer preserved",
			input:   `{"n":1000000}`,
			wantKey: "n",
			wantVal: "1000000",
		},
		{
			name:    "json float preserved",
			input:   `{"n":3.14}`,
			wantKey: "n",
			wantVal: "3.14",
		},
		{
			name:    "json null returns error",
			input:   `{"to":null}`,
			wantErr: "must be a scalar value, not null",
		},
		{
			name:    "json array returns error",
			input:   `{"to":["a","b"]}`,
			wantErr: "must be scalar",
		},
		{
			name:    "shorthand happy path",
			input:   "to=worker",
			wantKey: "to",
			wantVal: "worker",
		},
		{
			name:    "shorthand no-equals returns error with prefix",
			input:   "invalid-no-equals-no-brace",
			wantErr: "--params: invalid shorthand pair",
		},
		{
			name:    "shorthand no-equals returns error with separator hint",
			input:   "invalid-no-equals-no-brace",
			wantErr: "missing = separator",
		},
		{
			name:    "empty string is no-op",
			input:   "",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := cliutil.ParseParams(tc.input)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q; want to contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNil {
				if result != nil {
					t.Errorf("result = %v; want nil", result)
				}
				return
			}
			if tc.wantKey != "" {
				got, ok := result[tc.wantKey]
				if !ok {
					t.Errorf("result missing key %q; got %v", tc.wantKey, result)
				} else if got != tc.wantVal {
					t.Errorf("result[%q] = %q; want %q", tc.wantKey, got, tc.wantVal)
				}
			}
		})
	}
}

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

func TestPrintUsage_ShowsNeutralSchemaDescription(t *testing.T) {
	var stderr bytes.Buffer
	fs := flag.NewFlagSet("postman", flag.ContinueOnError)
	fs.Bool("version", false, "show version")
	fs.Bool("help", false, "show help")
	fs.Bool("no-tui", false, "run without TUI")
	fs.String("context-id", "", "context ID (auto-generated if not specified)")
	fs.String("config", "", "path to config file")
	fs.String("log-file", "", "log file path")
	fs.String("base-dir", "", "override state directory")
	fs.String("state-home", "", "override XDG_STATE_HOME")

	printUsage(&stderr, fs)

	got := stderr.String()
	if !strings.Contains(got, "Usage: tmux-a2a-postman [options] <command>") {
		t.Fatalf("usage missing explicit-command form: %q", got)
	}
	if !strings.Contains(got, "Use an explicit subcommand; bare `tmux-a2a-postman` prints usage.") {
		t.Fatalf("usage missing explicit-subcommand guidance: %q", got)
	}
	if !strings.Contains(got, "Print JSON Schema for config or supported command surfaces") {
		t.Fatalf("usage missing neutral schema description: %q", got)
	}
	if strings.Contains(got, "Print JSON Schema for config or command options") {
		t.Fatalf("usage still contains stale schema wording: %q", got)
	}
	if strings.Contains(got, "Start tmux-a2a-postman daemon (default)") {
		t.Fatalf("usage still claims start is the default: %q", got)
	}
	if strings.Contains(got, "--context-id") {
		t.Fatalf("usage still exposes hidden context override: %q", got)
	}
	if strings.Contains(got, "get-context-id") {
		t.Fatalf("usage still exposes get-context-id in the default operator surface: %q", got)
	}
}
