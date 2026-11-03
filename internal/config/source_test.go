package config

import (
	"context"
	"os"
	"path/filepath"
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
	if !slices.Contains(topology.Edges["critic"], "approver") || slices.Contains(topology.Edges["agent"], "approver") {
		t.Fatal("unexpected builtin approval routes", topology.Edges)
	}
	policy, err := topology.RecipientPolicy(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"critic", "guardian", "messenger", "orchestrator", "approver", "worker", "worker-alt"} {
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
	for _, name := range []string{"codex", "claude"} {
		count := 0
		for _, column := range presets[name].Columns {
			for _, pane := range column.Panes {
				count++
				if pane.Command != "exec "+name {
					t.Fatal("personal launch flags", pane)
				}
			}
		}
		if count != 8 {
			t.Fatal("incomplete team", name, count)
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
