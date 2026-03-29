package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/template"
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
custom_key: You are: worker
---`,
			want: map[string]string{
				"custom_key": "You are: worker",
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
custom_key: "You are worker"
---`,
			want: map[string]string{
				"custom_key": `"You are worker"`,
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

	t.Run("Edges plain text", func(t *testing.T) {
		content := "## Edges\n\n```mermaid\ngraph LR\n    a -- b\n```"
		sections := extractH2Sections(content)
		if _, ok := sections["edges"]; !ok {
			t.Error("key 'edges' missing")
		}
	})

	t.Run("edges backtick", func(t *testing.T) {
		content := "## 1. `edges`\n\n```mermaid\ngraph LR\n    a -- b\n```"
		sections := extractH2Sections(content)
		if _, ok := sections["edges"]; !ok {
			t.Error("key 'edges' missing for backtick format")
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

// TestStripHeadingNumber covers: numbered and unnumbered headings.
func TestStripHeadingNumber(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Edges", "Edges"},
		{"1. Edges", "Edges"},
		{"13. Common Template", "Common Template"},
		{"", ""},
	}
	for _, tt := range tests {
		got := stripHeadingNumber(tt.input)
		if got != tt.want {
			t.Errorf("stripHeadingNumber(%q): got %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestExtractH2Sections_Numbered covers: numbered headings like "## 1. Edges".
func TestExtractH2Sections_Numbered(t *testing.T) {
	t.Run("Edges with number", func(t *testing.T) {
		content := "## 1. Edges\n\nedges body"
		sections := extractH2Sections(content)
		if _, ok := sections["edges"]; !ok {
			t.Error("key 'edges' missing for numbered heading")
		}
	})

	t.Run("common_template backtick", func(t *testing.T) {
		content := "## `common_template`\n\nshared instructions"
		sections := extractH2Sections(content)
		if v, ok := sections["common_template"]; !ok {
			t.Error("key 'common_template' missing")
		} else if v != "shared instructions" {
			t.Errorf("body: got %q, want %q", v, "shared instructions")
		}
	})

	t.Run("common_template with number and suffix", func(t *testing.T) {
		content := "## 2.1 `common_template` yay!\n\nshared\n\n## `boss`\n\nboss body"
		sections := extractH2Sections(content)
		if v := sections["common_template"]; v != "shared" {
			t.Errorf("common_template: got %q, want %q", v, "shared")
		}
		if v := sections["boss"]; v != "boss body" {
			t.Errorf("boss: got %q, want %q", v, "boss body")
		}
	})
}

// TestExtractNodeFields covers: h3 reserved sections extraction.
func TestExtractNodeFields(t *testing.T) {
	t.Run("role extracted; non-reserved sections left in template", func(t *testing.T) {
		body := "### `role`\nexecutor\n\n### Tool Constraints\nCRITICAL"
		role, tmpl := extractNodeFields(body)
		if role != "executor" {
			t.Errorf("role: got %q, want %q", role, "executor")
		}
		if tmpl != "### Tool Constraints\nCRITICAL" {
			t.Errorf("template: got %q, want %q", tmpl, "### Tool Constraints\nCRITICAL")
		}
	})

	t.Run("no reserved sections returns body unchanged", func(t *testing.T) {
		body := "### Tool Constraints\nCRITICAL\n\n### Rules\nDo things"
		role, tmpl := extractNodeFields(body)
		if role != "" {
			t.Errorf("role should be empty, got %q", role)
		}
		if tmpl != body {
			t.Errorf("template should be unchanged")
		}
	})

	t.Run("only role present", func(t *testing.T) {
		body := "### `role`\ncoordinator\n\n### Workflow\nStep 1"
		role, tmpl := extractNodeFields(body)
		if role != "coordinator" {
			t.Errorf("role: got %q", role)
		}
		if tmpl != "### Workflow\nStep 1" {
			t.Errorf("template: got %q", tmpl)
		}
	})

	t.Run("role at end of body", func(t *testing.T) {
		body := "### Workflow\nStep 1\n\n### `role`\nexecutor"
		role, tmpl := extractNodeFields(body)
		if role != "executor" {
			t.Errorf("role: got %q", role)
		}
		if tmpl != "### Workflow\nStep 1" {
			t.Errorf("template: got %q", tmpl)
		}
	})
}

// TestExtractNodeFields_FallbackFrontmatter covers backward compat: frontmatter
// still works when no h3 reserved sections present.
func TestExtractNodeFields_FallbackFrontmatter(t *testing.T) {
	body := "---\nrole: executor\n---\n\nTemplate body."
	role, tmpl := extractNodeFields(body)
	// extractNodeFields itself returns empty (no h3 sections)
	if role != "" {
		t.Fatalf("expected empty from extractNodeFields, got role=%q", role)
	}
	// Caller (loadMarkdownConfig) falls back to frontmatter
	fm := parseFrontmatter(body)
	if fm["role"] != "executor" {
		t.Errorf("frontmatter role: got %q", fm["role"])
	}
	// Template should be body unchanged (no h3 stripping)
	if tmpl != body {
		t.Error("body should be unchanged when no h3 sections found")
	}
	// After stripFrontmatter, template is clean
	stripped := strings.TrimSpace(stripFrontmatter(tmpl))
	if stripped != "Template body." {
		t.Errorf("stripped template: got %q, want %q", stripped, "Template body.")
	}
}

// TestLoadMarkdownConfig covers: global frontmatter, edges, common template,
// node sections with h3 reserved fields.
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

## ` + "`common_template`" + `

Shared instructions for all nodes.

## ` + "`orchestrator`" + `

### ` + "`role`" + `
coordinator

### Workflow
You coordinate things.

## ` + "`worker`" + `

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

	t.Run("CommonTemplate", func(t *testing.T) {
		if cfg.CommonTemplate != "Shared instructions for all nodes." {
			t.Errorf("CommonTemplate: got %q, want %q", cfg.CommonTemplate, "Shared instructions for all nodes.")
		}
	})

	t.Run("Node h3 fields", func(t *testing.T) {
		oc, ok := cfg.Nodes["orchestrator"]
		if !ok {
			t.Fatal("orchestrator node missing")
		}
		if oc.Role != "coordinator" {
			t.Errorf("orchestrator role: got %q", oc.Role)
		}
		if oc.Template != "### Workflow\nYou coordinate things." {
			t.Errorf("orchestrator template: got %q, want %q", oc.Template, "### Workflow\nYou coordinate things.")
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

// TestLoadMarkdownConfig_NumberedHeadings covers: numbered headings like "## 1. Edges".
func TestLoadMarkdownConfig_NumberedHeadings(t *testing.T) {
	content := `---
ui_node: messenger
---

## 1. Edges

` + "```mermaid" + `
graph LR
    a --- b
` + "```" + `

## 2. ` + "`common_template`" + `

Shared text.

## 3. ` + "`worker`" + `

### ` + "`role`" + `
executor

### Rules
Do things.
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

	if len(cfg.Edges) == 0 {
		t.Error("edges not parsed from numbered heading")
	}
	if cfg.CommonTemplate != "Shared text." {
		t.Errorf("CommonTemplate: got %q", cfg.CommonTemplate)
	}
	wc := cfg.Nodes["worker"]
	if wc.Role != "executor" {
		t.Errorf("worker role: got %q", wc.Role)
	}
	if wc.Template != "### Rules\nDo things." {
		t.Errorf("worker template: got %q", wc.Template)
	}
}

// TestLoadNodeMarkdownFile covers: h3 reserved fields and template extraction.
func TestLoadNodeMarkdownFile(t *testing.T) {
	t.Run("h3 fields", func(t *testing.T) {
		content := "### `role`\nexecutor\n\n### Workflow\nYou are the executor.\n"
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
		if nc.Template != "### Workflow\nYou are the executor." {
			t.Errorf("template: got %q", nc.Template)
		}
	})

	t.Run("frontmatter fallback", func(t *testing.T) {
		content := "---\nrole: worker\n---\n\nBody.\n"
		dir := t.TempDir()
		path := filepath.Join(dir, "worker.md")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}

		_, nc, err := loadNodeMarkdownFile(path)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if nc.Role != "worker" {
			t.Errorf("role: got %q", nc.Role)
		}
		if nc.Template != "Body." {
			t.Errorf("template: got %q", nc.Template)
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
	// Isolate CWD so project-local .tmux-a2a-postman/ is not discovered.
	t.Chdir(fakeHome)
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

func TestLoadConfig_ProjectLocalMarkdownMessageFooterShellExpansionBlocked(t *testing.T) {
	tmpDir := t.TempDir()
	xdgDir, fakeHome := setupXDGAndHome(t, tmpDir)

	writeFile(t, filepath.Join(xdgDir, "postman.toml"), `
[postman]
allow_shell_templates = true

[worker]
role = "worker"
`)

	projectDir := filepath.Join(fakeHome, "project")
	localConfigDir := filepath.Join(projectDir, ".tmux-a2a-postman")
	writeFile(t, filepath.Join(localConfigDir, "postman.md"), "## `message_footer`\n\nProject footer $(printf project-local-markdown-footer)\n")

	t.Chdir(projectDir)

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.AllowShellTemplates {
		t.Fatal("AllowShellTemplates = false, want true from trusted base")
	}
	if !strings.Contains(cfg.MessageFooter, "You can talk to: {can_talk_to}") {
		t.Fatalf("MessageFooter missing default footer content: %q", cfg.MessageFooter)
	}
	if !strings.Contains(cfg.MessageFooter, "Project footer $(printf project-local-markdown-footer)") {
		t.Fatalf("MessageFooter missing appended project-local footer: %q", cfg.MessageFooter)
	}

	got := template.ExpandTemplate(cfg.MessageFooter, map[string]string{}, 5*time.Second, cfg.AllowShellForMessageFooter())
	if !strings.Contains(got, "$(printf project-local-markdown-footer)") {
		t.Fatalf("project-local Markdown footer unexpectedly executed shell command: %q", got)
	}
}

func TestLoadConfig_TrustedXDGMarkdownMessageFooterShellExpansionAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	xdgDir, _ := setupXDGAndHome(t, tmpDir)

	writeFile(t, filepath.Join(xdgDir, "postman.toml"), `
[postman]
allow_shell_templates = true

[worker]
role = "worker"
`)
	writeFile(t, filepath.Join(xdgDir, "postman.md"), "## `message_footer`\n\nTrusted footer $(printf trusted-xdg-markdown-footer)\n")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.AllowShellTemplates {
		t.Fatal("AllowShellTemplates = false, want true from trusted XDG base")
	}

	got := template.ExpandTemplate(cfg.MessageFooter, map[string]string{}, 5*time.Second, cfg.AllowShellForMessageFooter())
	if got != "Trusted footer trusted-xdg-markdown-footer" {
		t.Fatalf("trusted XDG Markdown footer = %q, want %q", got, "Trusted footer trusted-xdg-markdown-footer")
	}
}
