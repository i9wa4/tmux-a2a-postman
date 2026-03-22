package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeFile is a helper to write a file, creating parent dirs as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

// TestParseFrontmatter covers parse rules: first-colon split, whitespace trim,
// no multi-line, no quotes, lines without colon ignored.
func TestParseFrontmatter(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    map[string]string
	}{
		{
			name: "basic key:value",
			content: `---
ui_node: messenger
reply_command: send
---
body`,
			want: map[string]string{
				"ui_node":       "messenger",
				"reply_command": "send",
			},
		},
		{
			name: "FirstColonSplit: value contains colon",
			content: `---
on_join: You are: worker
---`,
			want: map[string]string{
				"on_join": "You are: worker",
			},
		},
		{
			name: "NoMultiline: second line not a continuation",
			content: `---
role: assistant
  continued line
---`,
			want: map[string]string{
				"role": "assistant",
			},
		},
		{
			name: "QuotesLiteral: quotes preserved in value",
			content: `---
on_join: "You are worker"
---`,
			want: map[string]string{
				"on_join": `"You are worker"`,
			},
		},
		{
			name: "whitespace trimmed from key and value",
			content: `---
  role  :   executor
---`,
			want: map[string]string{
				"role": "executor",
			},
		},
		{
			name: "line without colon is ignored",
			content: `---
role: worker
no_colon_here
---`,
			want: map[string]string{
				"role": "worker",
			},
		},
		{
			name:    "no frontmatter returns empty map",
			content: "just body text",
			want:    map[string]string{},
		},
		{
			name: "unclosed frontmatter returns empty map",
			content: `---
role: worker`,
			want: map[string]string{},
		},
		{
			name: "keys are lowercased",
			content: `---
Role: executor
---`,
			want: map[string]string{
				"role": "executor",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseFrontmatter(tt.content)
			if len(got) != len(tt.want) {
				t.Fatalf("len mismatch: got %d keys %v, want %d keys %v", len(got), got, len(tt.want), tt.want)
			}
			for k, wv := range tt.want {
				if gv, ok := got[k]; !ok {
					t.Errorf("missing key %q", k)
				} else if gv != wv {
					t.Errorf("key %q: got %q, want %q", k, gv, wv)
				}
			}
		})
	}
}

// TestParseMermaidEdges covers: graph header stripped, --- normalized to --,
// blank lines skipped.
func TestParseMermaidEdges(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name: "graph header stripped, edges preserved",
			input: `
graph LR
    boss --- orchestrator
    orchestrator -- worker
`,
			want: []string{
				"boss -- orchestrator",
				"orchestrator -- worker",
			},
		},
		{
			name: "directed edge passthrough",
			input: `
graph TD
    a --> b
`,
			want: []string{"a --> b"},
		},
		{
			name: "graph TD stripped case-insensitive",
			input: `
GRAPH TD
    x --- y
`,
			want: []string{"x -- y"},
		},
		{
			name:  "empty block",
			input: "\n\n",
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseMermaidEdges(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestExtractH2Sections covers: backtick node name extraction, special Edges
// heading, headings without backticks skipped.
func TestExtractH2Sections(t *testing.T) {
	t.Run("BacktickName: worker-alt extracted", func(t *testing.T) {
		content := "## `worker-alt` Node\n\nbody text"
		sections := extractH2Sections(content)
		if v, ok := sections["worker-alt"]; !ok {
			t.Error("key 'worker-alt' missing")
		} else if v != "body text" {
			t.Errorf("body: got %q, want %q", v, "body text")
		}
	})

	t.Run("NoBacktick: heading skipped", func(t *testing.T) {
		content := "## Worker Node\n\nbody text"
		sections := extractH2Sections(content)
		if len(sections) != 0 {
			t.Errorf("expected empty, got %v", sections)
		}
	})

	t.Run("Edges heading", func(t *testing.T) {
		content := "## Edges\n\n```mermaid\ngraph LR\n    a -- b\n```"
		sections := extractH2Sections(content)
		if _, ok := sections["edges"]; !ok {
			t.Error("key 'edges' missing")
		}
	})

	t.Run("multiple sections body boundaries", func(t *testing.T) {
		content := "## `worker` Node\n\nworker body\n\n## `boss` Node\n\nboss body"
		sections := extractH2Sections(content)
		if v := sections["worker"]; v != "worker body" {
			t.Errorf("worker body: got %q, want %q", v, "worker body")
		}
		if v := sections["boss"]; v != "boss body" {
			t.Errorf("boss body: got %q, want %q", v, "boss body")
		}
	})
}

// TestLoadMarkdownConfig covers: global frontmatter, edges, node sections.
func TestLoadMarkdownConfig(t *testing.T) {
	content := `---
ui_node: messenger
reply_command: send --to <r> --body "<m>"
---

## Edges

` + "```mermaid" + `
graph LR
    boss --- orchestrator
` + "```" + `

## ` + "`orchestrator`" + ` Node

---
role: coordinator
on_join: You are coordinator.
---

You coordinate things.

## ` + "`worker`" + ` Node

Worker template.
`

	dir := t.TempDir()
	path := filepath.Join(dir, "postman.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadMarkdownConfig(path)
	if err != nil {
		t.Fatalf("loadMarkdownConfig error: %v", err)
	}

	t.Run("GlobalFrontmatter", func(t *testing.T) {
		if cfg.UINode != "messenger" {
			t.Errorf("UINode: got %q, want %q", cfg.UINode, "messenger")
		}
		if cfg.ReplyCommand != `send --to <r> --body "<m>"` {
			t.Errorf("ReplyCommand: got %q", cfg.ReplyCommand)
		}
	})

	t.Run("Edges parsed", func(t *testing.T) {
		if len(cfg.Edges) == 0 {
			t.Fatal("no edges parsed")
		}
		// Mermaid --- normalized to --
		found := false
		for _, e := range cfg.Edges {
			if e == "boss -- orchestrator" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected 'boss -- orchestrator' in %v", cfg.Edges)
		}
	})

	t.Run("Node sections", func(t *testing.T) {
		oc, ok := cfg.Nodes["orchestrator"]
		if !ok {
			t.Fatal("orchestrator node missing")
		}
		if oc.Role != "coordinator" {
			t.Errorf("orchestrator role: got %q", oc.Role)
		}
		if oc.OnJoin != "You are coordinator." {
			t.Errorf("orchestrator on_join: got %q", oc.OnJoin)
		}
		if oc.Template != "You coordinate things." {
			t.Errorf("orchestrator template: got %q, want %q", oc.Template, "You coordinate things.")
		}
		wc, ok := cfg.Nodes["worker"]
		if !ok {
			t.Fatal("worker node missing")
		}
		if wc.Template != "Worker template." {
			t.Errorf("worker template: got %q", wc.Template)
		}
	})
}

// TestLoadNodeMarkdownFile covers: body → Template, frontmatter → OnJoin/Role,
// ui_node in frontmatter is silently ignored (IgnoresUINode).
func TestLoadNodeMarkdownFile(t *testing.T) {
	t.Run("basic fields", func(t *testing.T) {
		content := `---
role: executor
on_join: You are executor.
---

# WORKER

You are the executor.
`
		dir := t.TempDir()
		path := filepath.Join(dir, "worker.md")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		name, nc, err := loadNodeMarkdownFile(path)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if name != "worker" {
			t.Errorf("name: got %q, want %q", name, "worker")
		}
		if nc.Role != "executor" {
			t.Errorf("role: got %q", nc.Role)
		}
		if nc.OnJoin != "You are executor." {
			t.Errorf("on_join: got %q", nc.OnJoin)
		}
		if nc.Template == "" {
			t.Error("template should not be empty")
		}
	})

	t.Run("IgnoresUINode: ui_node in frontmatter does not set any field", func(t *testing.T) {
		content := `---
ui_node: messenger
role: worker
---

Body.
`
		dir := t.TempDir()
		path := filepath.Join(dir, "worker.md")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		_, nc, err := loadNodeMarkdownFile(path)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		// ui_node is silently ignored; only role and on_join set on NodeConfig
		if nc.Role != "worker" {
			t.Errorf("role: got %q", nc.Role)
		}
	})
}

// setupXDGAndHome is a test helper that creates an XDG config dir and fake home,
// sets environment variables, and changes to the given cwd. Returns cleanup func.
func setupXDGAndHome(t *testing.T, tmpDir string) (xdgDir string, fakeHome string) {
	t.Helper()
	fakeHome = filepath.Join(tmpDir, "home")
	xdgDir = filepath.Join(tmpDir, "xdg", "tmux-a2a-postman")
	if err := os.MkdirAll(xdgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll xdgDir: %v", err)
	}
	if err := os.MkdirAll(fakeHome, 0o755); err != nil {
		t.Fatalf("MkdirAll fakeHome: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Dir(xdgDir))
	t.Setenv("HOME", fakeHome)
	return xdgDir, fakeHome
}

// TestLoadConfig_MarkdownOverlay: postman.md Template wins over postman.toml template.
func TestLoadConfig_MarkdownOverlay(t *testing.T) {
	tmpDir := t.TempDir()
	xdgDir, _ := setupXDGAndHome(t, tmpDir)

	writeFile(t, filepath.Join(xdgDir, "postman.toml"), `
[postman]
scan_interval_seconds = 2.0

[worker]
template = "from toml"
`)
	writeFile(t, filepath.Join(xdgDir, "postman.md"), "## `worker` Node\n\nfrom markdown\n")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Nodes["worker"].Template != "from markdown" {
		t.Errorf("worker.Template: got %q, want %q", cfg.Nodes["worker"].Template, "from markdown")
	}
}

// TestLoadConfig_TomlAndMarkdownCoexist: TOML structural fields preserved when postman.md
// only sets template.
func TestLoadConfig_TomlAndMarkdownCoexist(t *testing.T) {
	tmpDir := t.TempDir()
	xdgDir, _ := setupXDGAndHome(t, tmpDir)

	writeFile(t, filepath.Join(xdgDir, "postman.toml"), `
[postman]
scan_interval_seconds = 5.0

[worker]
template = "from toml"
idle_timeout_seconds = 300
`)
	writeFile(t, filepath.Join(xdgDir, "postman.md"), "## `worker` Node\n\nfrom markdown\n")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Nodes["worker"].Template != "from markdown" {
		t.Errorf("worker.Template: got %q, want %q", cfg.Nodes["worker"].Template, "from markdown")
	}
	if cfg.Nodes["worker"].IdleTimeoutSeconds != 300 {
		t.Errorf("worker.IdleTimeoutSeconds: got %v, want 300", cfg.Nodes["worker"].IdleTimeoutSeconds)
	}
	if cfg.ScanInterval != 5.0 {
		t.Errorf("ScanInterval: got %v, want 5.0", cfg.ScanInterval)
	}
}

// TestLoadConfig_ThreeWayConflict: postman.md > nodes/worker.md > nodes/worker.toml
// for the same node.
func TestLoadConfig_ThreeWayConflict(t *testing.T) {
	tmpDir := t.TempDir()
	xdgDir, fakeHome := setupXDGAndHome(t, tmpDir)

	// XDG level: no TOML (only local matters here)
	// Silence XDG so only project-local fires
	t.Setenv("XDG_CONFIG_HOME", "/nonexistent")
	_ = xdgDir

	// Project-local dir: subdir of fakeHome/project
	projectDir := filepath.Join(fakeHome, "project")
	localCfgDir := filepath.Join(projectDir, ".tmux-a2a-postman")
	nodesDir := filepath.Join(localCfgDir, "nodes")

	writeFile(t, filepath.Join(localCfgDir, "postman.toml"), `
[postman]
scan_interval_seconds = 1.0
`)
	writeFile(t, filepath.Join(nodesDir, "worker.toml"), `[worker]
template = "from nodes/worker.toml"
idle_timeout_seconds = 42
`)
	writeFile(t, filepath.Join(nodesDir, "worker.md"), "---\nrole: md-role\n---\n\nfrom nodes/worker.md\n")
	writeFile(t, filepath.Join(localCfgDir, "postman.md"), "## `worker` Node\n\nfrom postman.md\n")

	origWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(origWd) }()
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("Chdir: %v", err)
	}

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	// postman.md wins (loaded last)
	if cfg.Nodes["worker"].Template != "from postman.md" {
		t.Errorf("worker.Template: got %q, want %q", cfg.Nodes["worker"].Template, "from postman.md")
	}
	// nodes/worker.md role set (postman.md has no role frontmatter)
	if cfg.Nodes["worker"].Role != "md-role" {
		t.Errorf("worker.Role: got %q, want %q", cfg.Nodes["worker"].Role, "md-role")
	}
	// TOML structural field preserved
	if cfg.Nodes["worker"].IdleTimeoutSeconds != 42 {
		t.Errorf("worker.IdleTimeoutSeconds: got %v, want 42", cfg.Nodes["worker"].IdleTimeoutSeconds)
	}
}
