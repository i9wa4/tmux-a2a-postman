package config

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestBuiltinSourceIsPortableAndExplicitFileIsComplete(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "absent", "postman.md")
	source := Source{Path: path, Builtin: true}
	topology, err := source.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(topology.Nodes) != 8 || topology.Reviewer != "approver" || topology.InterfaceNode != "messenger" || len(topology.Sessions) != 0 {
		t.Fatal(topology)
	}
	policy, err := topology.RecipientPolicy(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range topology.Nodes {
		if name == "agent" {
			continue
		}
		if policy.Nodes[name].Instructions == "" || policy.Nodes[name].Role == "" {
			t.Fatal("missing established role", name)
		}
	}
	for _, text := range []string{"postman-records", "postman-review", "postman-plan", "postman-orchestrate", "no installed Skill tree"} {
		if !strings.Contains(policy.Catalog, text) {
			t.Fatal("unreachable builtin procedure", text)
		}
	}
	presets, err := topology.LaunchPresets()
	if err != nil {
		t.Fatal(err)
	}
	if len(presets) != 3 {
		t.Fatal("unexpected builtin preset set", presets)
	}
	reviewPreset := presets["preset-p"]
	if len(reviewPreset.Columns) != 2 || len(reviewPreset.Columns[1].Panes) != 3 {
		t.Fatal("unexpected builtin review preset", reviewPreset)
	}
	reviewRole := reviewPreset.Columns[1].Panes[2].Role
	if reviewRole == "" || reviewRole == topology.Reviewer ||
		!slices.Contains(topology.Edges[reviewRole], topology.Reviewer) ||
		slices.Contains(topology.Edges["agent"], topology.Reviewer) {
		t.Fatal("unexpected builtin approval routes", topology.Edges)
	}
	dev := presets["dev"]
	if len(dev.Columns) != 2 || len(dev.Columns[0].Panes) != 1 || len(dev.Columns[1].Panes) != 1 ||
		dev.Columns[0].Panes[0] != (LaunchPane{Title: "editor"}) ||
		dev.Columns[1].Panes[0] != (LaunchPane{Title: "agent", Role: "agent", Command: "codex --yolo --no-alt-screen --no-daemon --model gpt-6-sol --config model_reasoning_effort=medium"}) {
		t.Fatal("builtin dev differs from original two-pane layout", dev)
	}
	for name, want := range map[string][][]string{
		"preset-p": {
			{"messenger|codex --yolo --no-alt-screen --no-daemon --model gpt-6-luna --config model_reasoning_effort=high", "orchestrator|codex --yolo --no-alt-screen --no-daemon --model gpt-6-sol --config model_reasoning_effort=high", "worker|codex --yolo --no-alt-screen --no-daemon --model gpt-6-sol --config model_reasoning_effort=medium", "worker-alt|codex --yolo --no-alt-screen --no-daemon --model gpt-6-sol --config model_reasoning_effort=medium"},
			{"approver|codex --yolo --no-alt-screen --no-daemon --model gpt-6-luna --config model_reasoning_effort=high", "guardian|codex --yolo --no-alt-screen --no-daemon --model gpt-6-sol --config model_reasoning_effort=high", reviewRole + "|codex --yolo --no-alt-screen --no-daemon --model gpt-5.6-sol --config model_reasoning_effort=high"},
		},
		"preset-w": {
			{"messenger|codex --yolo --no-alt-screen --no-daemon --model gpt-6-luna --config model_reasoning_effort=high", "orchestrator|codex --yolo --no-alt-screen --no-daemon --model gpt-6-sol --config model_reasoning_effort=medium", "worker|codex --yolo --no-alt-screen --no-daemon --model gpt-6-luna --config model_reasoning_effort=high", "worker-alt|claude --allow-dangerously-skip-permissions --dangerously-skip-permissions --model sonnet --effort medium"},
			{"approver|codex --yolo --no-alt-screen --no-daemon --model gpt-6-luna --config model_reasoning_effort=high", "guardian|codex --yolo --no-alt-screen --no-daemon --model gpt-6-sol --config model_reasoning_effort=high", reviewRole + "|claude --allow-dangerously-skip-permissions --dangerously-skip-permissions --model \"opus[1m]\" --effort medium"},
		},
	} {
		var got [][]string
		for _, column := range presets[name].Columns {
			var panes []string
			for _, pane := range column.Panes {
				panes = append(panes, pane.Role+"|"+pane.Command)
			}
			got = append(got, panes)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("builtin seven-role layout changed", name, got)
		}
	}
	if _, err := os.Lstat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("loading builtin created user config", err)
	}
	if _, err := (Source{Path: path}).Load(ctx); err == nil {
		t.Fatal("explicit missing file fell back")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	raw := "## `edges`\n```mermaid\ngraph LR\nx --- y\n```\n## `x`\nOnly my custom role.\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Load(ctx); err == nil {
		t.Fatal("pinned builtin silently switched to appeared file")
	}
	custom, err := (Source{Path: path}).Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := custom.RecipientPolicy(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if len(custom.Nodes) != 2 || custom.Reviewer != "" || cp.Common != "" || cp.Catalog != "" || custom.BuiltinBundleID() != "" {
		t.Fatal("implicit builtin merge", custom, cp)
	}
	link := filepath.Join(t.TempDir(), "nix-managed.md")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := (Source{Path: link}).Load(ctx); err != nil {
		t.Fatal("valid configuration symlink rejected", err)
	}
}

func TestBuiltinSourceRejectsExistingEntriesAndCancellation(t *testing.T) {
	for _, kind := range []string{"directory", "invalid", "dangling", "unreadable"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "postman.md")
			switch kind {
			case "directory":
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			case "invalid":
				if err := os.WriteFile(path, []byte("## `edges`\n```mermaid\ngraph LR\nbad.name --- b\n```\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				if err := os.WriteFile(path, []byte("## `worker`\nCustom\n"), 0o000); err != nil {
					t.Fatal(err)
				}
			case "dangling":
				if err := os.Symlink(path+".missing", path); err != nil {
					t.Fatal(err)
				}
			}
			for _, builtin := range []bool{false, true} {
				if kind == "unreadable" && !builtin && os.Geteuid() == 0 {
					continue
				}
				if _, err := (Source{Path: path, Builtin: builtin}).Load(context.Background()); err == nil {
					t.Fatal("invalid entry accepted", builtin)
				}
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Source{Path: filepath.Join(t.TempDir(), "absent"), Builtin: true}).Load(ctx); err == nil {
		t.Fatal("cancelled builtin accepted")
	}
}
