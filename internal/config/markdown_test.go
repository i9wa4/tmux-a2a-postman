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

func setSkillCatalogHome(t *testing.T, home string) {
	t.Helper()
	old := skillCatalogUserHomeDir
	skillCatalogUserHomeDir = func() (string, error) {
		return home, nil
	}
	t.Cleanup(func() {
		skillCatalogUserHomeDir = old
	})
}

func assertContains(t *testing.T, content, want string) {
	t.Helper()
	if !strings.Contains(content, want) {
		t.Fatalf("content missing %q:\n%s", want, content)
	}
}

func assertNotContains(t *testing.T, content, want string) {
	t.Helper()
	if strings.Contains(content, want) {
		t.Fatalf("content unexpectedly contains %q:\n%s", want, content)
	}
}

// TestMarkdownFrontmatterAccept covers the supported syntax subset for
// Markdown frontmatter.
func TestMarkdownFrontmatterAccept(t *testing.T) {
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
			name: "BlankLinesAllowed",
			content: `---
role: assistant

reply_command: send
---`,
			want: map[string]string{
				"role":          "assistant",
				"reply_command": "send",
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
			name:    "no frontmatter returns empty map",
			content: "just body text",
			want:    map[string]string{},
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
		{
			name: "leading blank lines before frontmatter are allowed",
			content: `

---
role: executor
---`,
			want: map[string]string{
				"role": "executor",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseFrontmatter(tt.content)
			if err != nil {
				t.Fatalf("parseFrontmatter error: %v", err)
			}
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

func TestMarkdownFrontmatterReject(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "ContinuationLine",
			content: `---
role: assistant
  continued line
---`,
			want: `unsupported markdown frontmatter syntax at line 3: "  continued line"`,
		},
		{
			name: "ListItem",
			content: `---
talks_to:
  - worker
---`,
			want: `unsupported markdown frontmatter syntax at line 3: "  - worker"`,
		},
		{
			name: "CommentLine",
			content: `---
role: worker
# note
---`,
			want: `unsupported markdown frontmatter syntax at line 3: "# note"`,
		},
		{
			name: "UnclosedFrontmatter",
			content: `---
role: worker`,
			want: "unclosed markdown frontmatter starting at line 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseFrontmatter(tt.content)
			if err == nil {
				t.Fatal("expected parseFrontmatter to fail")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error mismatch: got %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

// TestParseMermaidEdges covers: graph header stripped, --- preserved,
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
    orchestrator --- worker
`,
			want: []string{
				"boss --- orchestrator",
				"orchestrator --- worker",
			},
		},
		{
			name: "user topology graph",
			input: `
graph LR
    messenger --- orchestrator
    orchestrator --- worker
    orchestrator --- worker-alt
    orchestrator --- critic
    orchestrator --- boss
    guardian --- critic
    orchestrator --- agent
`,
			want: []string{
				"messenger --- orchestrator",
				"orchestrator --- worker",
				"orchestrator --- worker-alt",
				"orchestrator --- critic",
				"orchestrator --- boss",
				"guardian --- critic",
				"orchestrator --- agent",
			},
		},
		{
			name: "arrow edge ignored",
			input: `
graph TD
    a --> b
`,
			want: nil,
		},
		{
			name: "graph TD stripped case-insensitive",
			input: `
GRAPH TD
    x --- y
`,
			want: []string{"x --- y"},
		},
		{
			name: "skips directives and strips node labels",
			input: `
flowchart LR
    messenger["Messenger"] --- orchestrator("Orchestrator")
    classDef active fill:#afa
    class messenger active
    click messenger call callback()
`,
			want: []string{"messenger --- orchestrator"},
		},
		{
			name:  "semicolon-separated graph",
			input: `graph LR; a --- b; b --- c;`,
			want:  []string{"a --- b", "b --- c"},
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

func TestParseMermaidUINode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantSet bool
		wantErr bool
	}{
		{
			name: "inline class",
			input: `
graph LR
    messenger:::ui_node --- orchestrator
    classDef ui_node fill:#fff3bf
`,
			want:    "messenger",
			wantSet: true,
		},
		{
			name: "class statement",
			input: `
graph LR
    messenger --- orchestrator
    class messenger ui_node
`,
			want:    "messenger",
			wantSet: true,
		},
		{
			name: "class statement with comma separated class names",
			input: `
graph LR
    messenger --- orchestrator
    class messenger active,ui_node
`,
			want:    "messenger",
			wantSet: true,
		},
		{
			name: "non ui class ignored",
			input: `
graph LR
    messenger:::active --- orchestrator
    classDef ui_node fill:#fff3bf
`,
			wantSet: false,
		},
		{
			name: "conflicting ui nodes rejected",
			input: `
graph LR
    messenger:::ui_node --- orchestrator
    class worker ui_node
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotSet, err := parseMermaidUINode(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatal("parseMermaidUINode() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMermaidUINode() error = %v", err)
			}
			if got != tt.want || gotSet != tt.wantSet {
				t.Fatalf("parseMermaidUINode() = %q, %v; want %q, %v", got, gotSet, tt.want, tt.wantSet)
			}
		})
	}
}

// TestExtractH2Sections covers: backtick node name extraction, special `edges`
// heading, headings without backticks skipped.
func TestExtractH2Sections(t *testing.T) {
	t.Run("BacktickName: worker-alt extracted", func(t *testing.T) {
		content := "## `worker-alt` Node\n\nbody text"
		order, sections := extractH2Sections(content)
		if len(order) != 1 || order[0] != "worker-alt" {
			t.Fatalf("order = %v, want %v", order, []string{"worker-alt"})
		}
		if v, ok := sections["worker-alt"]; !ok {
			t.Error("key 'worker-alt' missing")
		} else if v != "body text" {
			t.Errorf("body: got %q, want %q", v, "body text")
		}
	})

	t.Run("NoBacktick: heading skipped", func(t *testing.T) {
		content := "## Worker Node\n\nbody text"
		_, sections := extractH2Sections(content)
		if len(sections) != 0 {
			t.Errorf("expected empty, got %v", sections)
		}
	})

	t.Run("Edges plain text skipped", func(t *testing.T) {
		content := "## Edges\n\n```mermaid\ngraph LR\n    a --- b\n```"
		_, sections := extractH2Sections(content)
		if _, ok := sections["edges"]; ok {
			t.Error("plain-text Edges heading should not become key 'edges'")
		}
	})

	t.Run("edges backtick", func(t *testing.T) {
		content := "## 1. `edges`\n\n```mermaid\ngraph LR\n    a --- b\n```"
		_, sections := extractH2Sections(content)
		if _, ok := sections["edges"]; !ok {
			t.Error("key 'edges' missing for backtick format")
		}
	})

	t.Run("multiple sections body boundaries", func(t *testing.T) {
		content := "## `worker` Node\n\nworker body\n\n## `boss` Node\n\nboss body"
		order, sections := extractH2Sections(content)
		if strings.Join(order, ",") != "worker,boss" {
			t.Fatalf("order = %v, want %v", order, []string{"worker", "boss"})
		}
		if v := sections["worker"]; v != "worker body" {
			t.Errorf("worker body: got %q, want %q", v, "worker body")
		}
		if v := sections["boss"]; v != "boss body" {
			t.Errorf("boss body: got %q, want %q", v, "boss body")
		}
	})
}

// TestExtractH2Sections_Numbered covers numbered backtick headings like "## 1. `edges`".
func TestExtractH2Sections_Numbered(t *testing.T) {
	t.Run("plain Edges with number skipped", func(t *testing.T) {
		content := "## 1. Edges\n\nedges body"
		_, sections := extractH2Sections(content)
		if _, ok := sections["edges"]; ok {
			t.Error("plain numbered Edges heading should not become key 'edges'")
		}
	})

	t.Run("common_template backtick", func(t *testing.T) {
		content := "## `common_template`\n\nshared instructions"
		_, sections := extractH2Sections(content)
		if v, ok := sections["common_template"]; !ok {
			t.Error("key 'common_template' missing")
		} else if v != "shared instructions" {
			t.Errorf("body: got %q, want %q", v, "shared instructions")
		}
	})

	t.Run("common_template with number and suffix", func(t *testing.T) {
		content := "## 2.1 `common_template` yay!\n\nshared\n\n## `boss`\n\nboss body"
		_, sections := extractH2Sections(content)
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

// TestExtractNodeFields_FallbackFrontmatter covers frontmatter parsing
// independently from h3 reserved sections.
func TestExtractNodeFields_FallbackFrontmatter(t *testing.T) {
	body := "---\nrole: executor\n---\n\nTemplate body."
	role, tmpl := extractNodeFields(body)
	// extractNodeFields itself returns empty (no h3 sections)
	if role != "" {
		t.Fatalf("expected empty from extractNodeFields, got role=%q", role)
	}
	fm, err := parseFrontmatter(body)
	if err != nil {
		t.Fatalf("parseFrontmatter error: %v", err)
	}
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
reply_command: send-heredoc --to <r>
---

## ` + "`edges`" + `

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
		if cfg.ReplyCommand != `send-heredoc --to <r>` {
			t.Errorf("ReplyCommand: got %q", cfg.ReplyCommand)
		}
	})

	t.Run("Edges parsed", func(t *testing.T) {
		if len(cfg.Edges) == 0 {
			t.Fatal("no edges parsed")
		}
		found := false
		for _, e := range cfg.Edges {
			if e == "boss --- orchestrator" {
				found = true
			}
		}
		if !found {
			t.Errorf("expected 'boss --- orchestrator' in %v", cfg.Edges)
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

func TestLoadMarkdownConfig_MermaidUINode(t *testing.T) {
	content := `## ` + "`edges`" + `

` + "```mermaid" + `
graph LR
    messenger --- orchestrator
    orchestrator --- worker
    class messenger ui_node
    classDef ui_node fill:#fff3bf
` + "```" + `
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
	if cfg.UINode != "messenger" {
		t.Fatalf("UINode: got %q, want %q", cfg.UINode, "messenger")
	}
	if !cfg.HasExplicitUINodeSetting() {
		t.Fatal("Mermaid ui_node should be treated as an explicit setting")
	}
	if len(cfg.Edges) != 2 {
		t.Fatalf("Edges: got %v, want 2 edges", cfg.Edges)
	}
}

func TestLoadMarkdownConfig_FrontmatterUINodeOverridesMermaid(t *testing.T) {
	content := `---
ui_node: messenger
---

## ` + "`edges`" + `

` + "```mermaid" + `
graph LR
    boss:::ui_node --- orchestrator
` + "```" + `
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
	if cfg.UINode != "messenger" {
		t.Fatalf("UINode: got %q, want frontmatter value", cfg.UINode)
	}
}

func TestLoadMarkdownConfig_SkillPathAppendsGeneratedCatalog(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills", "bash", "SKILL.md"), `---
name: bash
description: Bash scripting rules for commands.
---

# Bash
`)
	writeFile(t, filepath.Join(dir, "skills", "find-docs", "SKILL.md"), `---
name: find-docs
description: >-
  Retrieves current documentation for developer tools.

  Use for API syntax questions.
---

# Find Docs
`)
	content := `---
skill_path: skills
---

## ` + "`common_template`" + `

Shared instructions.
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	cfg, err := loadMarkdownConfig(path)
	if err != nil {
		t.Fatalf("loadMarkdownConfig error: %v", err)
	}

	assertContains(t, cfg.CommonTemplate, "Shared instructions.")
	assertContains(t, cfg.CommonTemplate, "### Available Skills")
	assertContains(t, cfg.CommonTemplate, "Skill files live under `skills`.")
	assertContains(t, cfg.CommonTemplate, "- `bash`: Bash scripting rules for commands.")
	assertContains(t, cfg.CommonTemplate, "- `find-docs`: Retrieves current documentation for developer tools. Use for API syntax questions.")
}

func TestLoadMarkdownConfig_SkillPathReadsSymlinkedSkillDirectories(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real-skills", "bash")
	writeFile(t, filepath.Join(target, "SKILL.md"), `---
name: bash
description: Bash rules from a symlinked skill.
---

# Bash
`)
	linkParent := filepath.Join(dir, "linked-skills")
	if err := os.MkdirAll(linkParent, 0o755); err != nil {
		t.Fatalf("MkdirAll(linkParent): %v", err)
	}
	if err := os.Symlink(target, filepath.Join(linkParent, "bash")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	content := `---
skill_path: linked-skills
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	cfg, err := loadMarkdownConfig(path)
	if err != nil {
		t.Fatalf("loadMarkdownConfig error: %v", err)
	}

	assertContains(t, cfg.CommonTemplate, "- `bash`: Bash rules from a symlinked skill.")
}

func TestLoadMarkdownConfig_SkillPathYAMLListSelectsSkills(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills-a", "bash", "SKILL.md"), `---
name: bash
description: Bash rules.
---
`)
	writeFile(t, filepath.Join(dir, "skills-a", "python", "SKILL.md"), `---
name: python
description: Python rules.
---
`)
	writeFile(t, filepath.Join(dir, "skills-b", "postman-config-auditor", "SKILL.md"), `---
name: postman-config-auditor
description: Audit postman configs.
---
`)
	writeFile(t, filepath.Join(dir, "skills-b", "postman-session-operator", "SKILL.md"), `---
name: postman-session-operator
description: Operate postman sessions.
---
`)
	writeFile(t, filepath.Join(dir, "skills-b", "noise", "SKILL.md"), `---
name: noise
description: Unselected skill.
---
`)
	content := `---
skill_path:
  - path: skills-a
    skills:
      - bash
  - path: skills-b
    skills:
      - postman-config-auditor
      - postman-session-operator
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	cfg, err := loadMarkdownConfig(path)
	if err != nil {
		t.Fatalf("loadMarkdownConfig error: %v", err)
	}

	assertContains(t, cfg.CommonTemplate, "Skill files live under `skills-a`, `skills-b`.")
	assertContains(t, cfg.CommonTemplate, "- `bash`: Bash rules.")
	assertContains(t, cfg.CommonTemplate, "- `postman-config-auditor`: Audit postman configs.")
	assertContains(t, cfg.CommonTemplate, "- `postman-session-operator`: Operate postman sessions.")
	assertNotContains(t, cfg.CommonTemplate, "Python rules.")
	assertNotContains(t, cfg.CommonTemplate, "Unselected skill.")
}

func TestLoadMarkdownConfig_SkillPathOmittedSkillsSelectsEverySkill(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills", "bash", "SKILL.md"), `---
name: bash
description: Bash rules.
---
`)
	writeFile(t, filepath.Join(dir, "skills", "markdown", "SKILL.md"), `---
name: markdown
description: Markdown rules.
---
`)
	content := `---
skill_path:
  - path: skills
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	cfg, err := loadMarkdownConfig(path)
	if err != nil {
		t.Fatalf("loadMarkdownConfig error: %v", err)
	}

	assertContains(t, cfg.CommonTemplate, "- `bash`: Bash rules.")
	assertContains(t, cfg.CommonTemplate, "- `markdown`: Markdown rules.")
}

func TestLoadMarkdownConfig_SkillPathExplicitAllSkillName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills", "all", "SKILL.md"), `---
name: all
description: A real skill named all.
---
`)
	writeFile(t, filepath.Join(dir, "skills", "bash", "SKILL.md"), `---
name: bash
description: Bash rules.
---
`)
	content := `---
skill_path:
  - path: skills
    skills: [all]
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	cfg, err := loadMarkdownConfig(path)
	if err != nil {
		t.Fatalf("loadMarkdownConfig error: %v", err)
	}

	assertContains(t, cfg.CommonTemplate, "- `all`: A real skill named all.")
	assertNotContains(t, cfg.CommonTemplate, "- `bash`: Bash rules.")
}

func TestLoadMarkdownConfig_SkillPathLegacyScalarAllSelectsEverySkill(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills", "bash", "SKILL.md"), `---
name: bash
description: Bash rules.
---
`)
	writeFile(t, filepath.Join(dir, "skills", "markdown", "SKILL.md"), `---
name: markdown
description: Markdown rules.
---
`)
	content := `---
skill_path:
  - path: skills
    skills: all
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	cfg, err := loadMarkdownConfig(path)
	if err != nil {
		t.Fatalf("loadMarkdownConfig error: %v", err)
	}

	assertContains(t, cfg.CommonTemplate, "- `bash`: Bash rules.")
	assertContains(t, cfg.CommonTemplate, "- `markdown`: Markdown rules.")
}

func TestLoadMarkdownConfig_SkillPathDuplicateNamesUseLaterEntryOnce(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills-a", "bash", "SKILL.md"), `---
name: bash
description: Earlier bash rules.
---
`)
	writeFile(t, filepath.Join(dir, "skills-a", "markdown", "SKILL.md"), `---
name: markdown
description: Markdown rules.
---
`)
	writeFile(t, filepath.Join(dir, "skills-b", "bash", "SKILL.md"), `---
name: bash
description: Later bash rules.
---
`)
	content := `---
skill_path:
  - path: skills-a
  - path: skills-b
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	cfg, err := loadMarkdownConfig(path)
	if err != nil {
		t.Fatalf("loadMarkdownConfig error: %v", err)
	}

	assertContains(t, cfg.CommonTemplate, "Skill files live under `skills-a`, `skills-b`.")
	assertNotContains(t, cfg.CommonTemplate, "Earlier bash rules.")
	assertContains(t, cfg.CommonTemplate, "- `bash`: Later bash rules.")
	assertContains(t, cfg.CommonTemplate, "- `markdown`: Markdown rules.")
	if got := strings.Count(cfg.CommonTemplate, "- `bash`:"); got != 1 {
		t.Fatalf("duplicate bash catalog entries: got %d in:\n%s", got, cfg.CommonTemplate)
	}
}

func TestLoadMarkdownConfig_SkillPathInjectCompactionPingBuildsRuntimeCatalogs(t *testing.T) {
	dir := t.TempDir()
	setSkillCatalogHome(t, dir)
	writeFile(t, filepath.Join(dir, "context-skills", "repo-local", "SKILL.md"), `---
name: repo-local
description: Normal repo rules.
---
`)
	writeFile(t, filepath.Join(dir, ".config", "tmux-a2a-postman", "skills", "postman-session-operator", "SKILL.md"), `---
name: postman-session-operator
description: Shared ping rules.
---
`)
	writeFile(t, filepath.Join(dir, ".claude", "skills", "agent-harness-engineering", "SKILL.md"), `---
name: agent-harness-engineering
description: Claude harness rules.
---
`)
	writeFile(t, filepath.Join(dir, ".codex", "skills", "bash", "SKILL.md"), `---
name: bash
description: Codex shell rules.
---
`)
	content := `---
skill_path:
  - path: context-skills
    inject: role
  - path: ~/.config/tmux-a2a-postman/skills
    inject: compaction_ping
  - path: ~/.claude/skills
    inject: compaction_ping
    runtime: claude
  - path: ~/.codex/skills
    inject: compaction_ping
    runtime: codex
---

## ` + "`common_template`" + `

Compact shared instructions.
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	cfg, err := loadMarkdownConfig(path)
	if err != nil {
		t.Fatalf("loadMarkdownConfig error: %v", err)
	}

	assertContains(t, cfg.CommonTemplate, "Compact shared instructions.")
	assertContains(t, cfg.CommonTemplate, "- `repo-local`: Normal repo rules.")
	assertNotContains(t, cfg.CommonTemplate, "Shared ping rules.")
	assertNotContains(t, cfg.CommonTemplate, "Claude harness rules.")
	assertNotContains(t, cfg.CommonTemplate, "Codex shell rules.")

	claudeCatalog := cfg.CompactionSkillCatalogForRuntime("claude")
	assertContains(t, claudeCatalog, "- `postman-session-operator`: Shared ping rules.")
	assertContains(t, claudeCatalog, "- `agent-harness-engineering`: Claude harness rules.")
	assertNotContains(t, claudeCatalog, "Codex shell rules.")

	codexCatalog := cfg.CompactionSkillCatalogForRuntime("codex")
	assertContains(t, codexCatalog, "- `postman-session-operator`: Shared ping rules.")
	assertContains(t, codexCatalog, "- `bash`: Codex shell rules.")
	assertNotContains(t, codexCatalog, "Claude harness rules.")

	unknownCatalog := cfg.CompactionSkillCatalogForRuntime("node")
	assertContains(t, unknownCatalog, "- `postman-session-operator`: Shared ping rules.")
	assertNotContains(t, unknownCatalog, "Claude harness rules.")
	assertNotContains(t, unknownCatalog, "Codex shell rules.")
}

func TestLoadMarkdownConfig_SkillPathInjectAliasesRemainCompatible(t *testing.T) {
	dir := t.TempDir()
	setSkillCatalogHome(t, dir)
	writeFile(t, filepath.Join(dir, "role-skills", "repo-local", "SKILL.md"), `---
name: repo-local
description: Role rules.
---
`)
	writeFile(t, filepath.Join(dir, ".config", "tmux-a2a-postman", "skills", "postman-session-operator", "SKILL.md"), `---
name: postman-session-operator
description: Compaction ping rules.
---
`)
	content := `---
skill_path:
  - path: role-skills
    inject: context
  - path: ~/.config/tmux-a2a-postman/skills
    inject: ping
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	cfg, err := loadMarkdownConfig(path)
	if err != nil {
		t.Fatalf("loadMarkdownConfig error: %v", err)
	}

	assertContains(t, cfg.CommonTemplate, "- `repo-local`: Role rules.")
	assertNotContains(t, cfg.CommonTemplate, "Compaction ping rules.")
	assertContains(t, cfg.CompactionSkillCatalogForRuntime("codex"), "- `postman-session-operator`: Compaction ping rules.")
}

func TestLoadMarkdownConfig_SkillPathInjectCompactionPingRuntimeDuplicateOverridesShared(t *testing.T) {
	dir := t.TempDir()
	setSkillCatalogHome(t, dir)
	writeFile(t, filepath.Join(dir, ".config", "tmux-a2a-postman", "skills", "bash", "SKILL.md"), `---
name: bash
description: Shared bash rules.
---
`)
	writeFile(t, filepath.Join(dir, ".config", "tmux-a2a-postman", "skills", "repo-local", "SKILL.md"), `---
name: repo-local
description: Shared repo rules.
---
`)
	writeFile(t, filepath.Join(dir, ".claude", "skills", "bash", "SKILL.md"), `---
name: bash
description: Claude-specific bash rules.
---
`)
	content := `---
skill_path:
  - path: ~/.config/tmux-a2a-postman/skills
    inject: compaction_ping
  - path: ~/.claude/skills
    inject: compaction_ping
    runtime: claude
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	cfg, err := loadMarkdownConfig(path)
	if err != nil {
		t.Fatalf("loadMarkdownConfig error: %v", err)
	}

	claudeCatalog := cfg.CompactionSkillCatalogForRuntime("claude")
	assertContains(t, claudeCatalog, "Skill files live under `~/.config/tmux-a2a-postman/skills`, `~/.claude/skills`.")
	assertNotContains(t, claudeCatalog, "Shared bash rules.")
	assertContains(t, claudeCatalog, "- `bash`: Claude-specific bash rules.")
	assertContains(t, claudeCatalog, "- `repo-local`: Shared repo rules.")
	if got := strings.Count(claudeCatalog, "- `bash`:"); got != 1 {
		t.Fatalf("duplicate bash catalog entries: got %d in:\n%s", got, claudeCatalog)
	}

	fallbackCatalog := cfg.CompactionSkillCatalogForRuntime("other")
	assertContains(t, fallbackCatalog, "- `bash`: Shared bash rules.")
	assertNotContains(t, fallbackCatalog, "Claude-specific bash rules.")
}

func TestLoadMarkdownConfig_SkillPathInjectCompactionPingSamePathOverlapRendersOnce(t *testing.T) {
	dir := t.TempDir()
	setSkillCatalogHome(t, dir)
	writeFile(t, filepath.Join(dir, ".claude", "skills", "bash", "SKILL.md"), `---
name: bash
description: Bash rules.
---
`)
	content := `---
skill_path:
  - path: ~/.claude/skills
    inject: compaction_ping
  - path: ~/.claude/skills
    inject: compaction_ping
    runtime: claude
    skills: [bash]
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	cfg, err := loadMarkdownConfig(path)
	if err != nil {
		t.Fatalf("loadMarkdownConfig error: %v", err)
	}

	claudeCatalog := cfg.CompactionSkillCatalogForRuntime("claude")
	if got := strings.Count(claudeCatalog, "`~/.claude/skills`"); got != 1 {
		t.Fatalf("duplicate source path displays: got %d in:\n%s", got, claudeCatalog)
	}
	if got := strings.Count(claudeCatalog, "- `bash`:"); got != 1 {
		t.Fatalf("duplicate bash catalog entries: got %d in:\n%s", got, claudeCatalog)
	}
}

func TestLoadMarkdownConfig_CompactionSkillPathBuildsRuntimeCatalogs(t *testing.T) {
	dir := t.TempDir()
	setSkillCatalogHome(t, dir)
	writeFile(t, filepath.Join(dir, ".config", "tmux-a2a-postman", "skills", "repo-local", "SKILL.md"), `---
name: repo-local
description: Shared repo rules.
---
`)
	writeFile(t, filepath.Join(dir, ".claude", "skills", "agent-harness-engineering", "SKILL.md"), `---
name: agent-harness-engineering
description: Claude harness rules.
---
`)
	writeFile(t, filepath.Join(dir, ".codex", "skills", "bash", "SKILL.md"), `---
name: bash
description: Codex shell rules.
---
`)
	content := `---
compaction_skill_path:
  - path: ~/.config/tmux-a2a-postman/skills
    skills: all
  - path: ~/.claude/skills
    runtime: claude
    skills: all
  - path: ~/.codex/skills
    runtime: codex
    skills: all
---

## ` + "`common_template`" + `

Compact shared instructions.
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	cfg, err := loadMarkdownConfig(path)
	if err != nil {
		t.Fatalf("loadMarkdownConfig error: %v", err)
	}

	assertContains(t, cfg.CommonTemplate, "Compact shared instructions.")
	assertNotContains(t, cfg.CommonTemplate, "Claude harness rules.")
	assertNotContains(t, cfg.CommonTemplate, "Codex shell rules.")

	claudeCatalog := cfg.CompactionSkillCatalogForRuntime("claude")
	assertContains(t, claudeCatalog, "- `repo-local`: Shared repo rules.")
	assertContains(t, claudeCatalog, "- `agent-harness-engineering`: Claude harness rules.")
	assertNotContains(t, claudeCatalog, "Codex shell rules.")

	codexCatalog := cfg.CompactionSkillCatalogForRuntime("codex")
	assertContains(t, codexCatalog, "- `repo-local`: Shared repo rules.")
	assertContains(t, codexCatalog, "- `bash`: Codex shell rules.")
	assertNotContains(t, codexCatalog, "Claude harness rules.")

	unknownCatalog := cfg.CompactionSkillCatalogForRuntime("node")
	assertContains(t, unknownCatalog, "- `repo-local`: Shared repo rules.")
	assertNotContains(t, unknownCatalog, "Claude harness rules.")
	assertNotContains(t, unknownCatalog, "Codex shell rules.")
}

func TestLoadMarkdownConfig_SkillPathRejectsRuntimeSelector(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "skills", "bash", "SKILL.md"), `---
name: bash
description: Bash rules.
---
`)
	content := `---
skill_path:
  - path: skills
    runtime: claude
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	_, err := loadMarkdownConfig(path)
	if err == nil {
		t.Fatal("expected loadMarkdownConfig to fail")
	}
	if !strings.Contains(err.Error(), "skill_path item runtime requires inject: compaction_ping") {
		t.Fatalf("error mismatch: %v", err)
	}
}

func TestLoadMarkdownConfig_SkillPathRejectsUnsupportedInject(t *testing.T) {
	dir := t.TempDir()
	content := `---
skill_path:
  - path: skills
    inject: startup
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	_, err := loadMarkdownConfig(path)
	if err == nil {
		t.Fatal("expected loadMarkdownConfig to fail")
	}
	if !strings.Contains(err.Error(), `unsupported skill_path item inject "startup"`) {
		t.Fatalf("error mismatch: %v", err)
	}
}

func TestLoadMarkdownConfig_SkillPathInjectCompactionPingRejectsRepoLocalPath(t *testing.T) {
	dir := t.TempDir()
	content := `---
skill_path:
  - path: skills
    inject: compaction_ping
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	_, err := loadMarkdownConfig(path)
	if err == nil {
		t.Fatal("expected loadMarkdownConfig to fail")
	}
	if !strings.Contains(err.Error(), "skill_path item requires a global/user-level path") {
		t.Fatalf("error mismatch: %v", err)
	}
}

func TestLoadMarkdownConfig_CompactionSkillPathRejectsRepoLocalPath(t *testing.T) {
	dir := t.TempDir()
	content := `---
compaction_skill_path:
  - path: skills
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	_, err := loadMarkdownConfig(path)
	if err == nil {
		t.Fatal("expected loadMarkdownConfig to fail")
	}
	if !strings.Contains(err.Error(), "compaction_skill_path item requires a global/user-level path") {
		t.Fatalf("error mismatch: %v", err)
	}
}

func TestLoadMarkdownConfig_CompactionSkillPathRejectsInjectKey(t *testing.T) {
	dir := t.TempDir()
	content := `---
compaction_skill_path:
  - path: ~/.claude/skills
    inject: compaction_ping
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	_, err := loadMarkdownConfig(path)
	if err == nil {
		t.Fatal("expected loadMarkdownConfig to fail")
	}
	if !strings.Contains(err.Error(), `unsupported compaction_skill_path item key "inject"`) {
		t.Fatalf("error mismatch: %v", err)
	}
}

func TestLoadMarkdownConfig_SkillPathRejectsUnsupportedRuntimeSelector(t *testing.T) {
	dir := t.TempDir()
	content := `---
skill_path:
  - path: skills
    inject: compaction_ping
    runtime: vim
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	_, err := loadMarkdownConfig(path)
	if err == nil {
		t.Fatal("expected loadMarkdownConfig to fail")
	}
	if !strings.Contains(err.Error(), `unsupported skill_path item runtime "vim"; supported runtimes are claude, codex`) {
		t.Fatalf("error mismatch: %v", err)
	}
}

func TestLoadMarkdownConfig_SkillPathRejectsGlobSkillSelector(t *testing.T) {
	dir := t.TempDir()
	content := `---
skill_path:
  - path: skills
    skills:
      - postman-*
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	_, err := loadMarkdownConfig(path)
	if err == nil {
		t.Fatal("expected loadMarkdownConfig to fail")
	}
	if !strings.Contains(err.Error(), "does not support glob patterns") {
		t.Fatalf("error mismatch: %v", err)
	}
}

func TestLoadMarkdownConfig_SkillPathRejectsScalarSkillName(t *testing.T) {
	dir := t.TempDir()
	content := `---
skill_path:
  - path: skills
    skills: bash
---
`
	path := filepath.Join(dir, "postman.md")
	writeFile(t, path, content)

	_, err := loadMarkdownConfig(path)
	if err == nil {
		t.Fatal("expected loadMarkdownConfig to fail")
	}
	if !strings.Contains(err.Error(), "skills must be omitted, all, or a YAML list") {
		t.Fatalf("error mismatch: %v", err)
	}
}

func TestResolveSkillPathExpandsHome(t *testing.T) {
	old := skillCatalogUserHomeDir
	home := t.TempDir()
	skillCatalogUserHomeDir = func() (string, error) {
		return home, nil
	}
	t.Cleanup(func() {
		skillCatalogUserHomeDir = old
	})

	got, err := resolveSkillPath(filepath.Join(t.TempDir(), "postman.md"), "~/.claude/skills")
	if err != nil {
		t.Fatalf("resolveSkillPath error: %v", err)
	}
	want := filepath.Join(home, ".claude", "skills")
	if got != want {
		t.Fatalf("resolveSkillPath = %q, want %q", got, want)
	}
}

func TestParseSkillFrontmatter(t *testing.T) {
	content := `---
name: find-docs
description: |-
  First line.
  Second line.
license: MIT
---

# Body
`
	got, err := parseSkillFrontmatter(content)
	if err != nil {
		t.Fatalf("parseSkillFrontmatter error: %v", err)
	}
	if got["name"] != "find-docs" {
		t.Fatalf("name = %q, want find-docs", got["name"])
	}
	if got["description"] != "First line.\nSecond line." {
		t.Fatalf("description = %q", got["description"])
	}
	if got["license"] != "MIT" {
		t.Fatalf("license = %q, want MIT", got["license"])
	}
}

// TestLoadMarkdownConfig_NumberedHeadings covers numbered backtick headings.
func TestLoadMarkdownConfig_NumberedHeadings(t *testing.T) {
	content := `---
ui_node: messenger
---

## 1. ` + "`edges`" + `

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

func TestMarkdownLoadReject(t *testing.T) {
	t.Run("single-file node frontmatter", func(t *testing.T) {
		content := `## ` + "`worker`" + `

---
role:
  nested: nope
---

Template body.
`
		dir := t.TempDir()
		path := filepath.Join(dir, "postman.md")
		writeFile(t, path, content)

		_, err := loadMarkdownConfig(path)
		if err == nil {
			t.Fatal("expected loadMarkdownConfig to fail")
		}
		want := `node "worker": unsupported markdown frontmatter syntax at line 3: "  nested: nope"`
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error mismatch: got %q, want substring %q", err.Error(), want)
		}
	})

	t.Run("split-file node frontmatter", func(t *testing.T) {
		content := `---
role: worker`
		dir := t.TempDir()
		path := filepath.Join(dir, "worker.md")
		writeFile(t, path, content)

		_, _, err := loadNodeMarkdownFile(path)
		if err == nil {
			t.Fatal("expected loadNodeMarkdownFile to fail")
		}
		want := "unclosed markdown frontmatter starting at line 1"
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error mismatch: got %q, want substring %q", err.Error(), want)
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
delivery_idle_timeout_seconds = 300
`)
	writeFile(t, filepath.Join(xdgDir, "postman.md"), "## `worker` Node\n\nfrom markdown\n")

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Nodes["worker"].Template != "from markdown" {
		t.Errorf("worker.Template: got %q, want %q", cfg.Nodes["worker"].Template, "from markdown")
	}
	if cfg.Nodes["worker"].DeliveryIdleTimeoutSeconds != 300 {
		t.Errorf("worker.DeliveryIdleTimeoutSeconds: got %v, want 300", cfg.Nodes["worker"].DeliveryIdleTimeoutSeconds)
	}
	if cfg.ScanInterval != 5.0 {
		t.Errorf("ScanInterval: got %v, want 5.0", cfg.ScanInterval)
	}
}

// TestLoadConfig_ThreeWayConflict: postman.md > nodes/worker.md > nodes/worker.toml
// for the same node.
func TestLoadConfig_ThreeWayConflict(t *testing.T) {
	t.Skip("project-local implicit overlays retired by #419")
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
delivery_idle_timeout_seconds = 42
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
	if cfg.Nodes["worker"].DeliveryIdleTimeoutSeconds != 42 {
		t.Errorf("worker.DeliveryIdleTimeoutSeconds: got %v, want 42", cfg.Nodes["worker"].DeliveryIdleTimeoutSeconds)
	}
}

func TestLoadConfig_ProjectLocalMarkdownMessageFooterShellExpansionBlocked(t *testing.T) {
	t.Skip("project-local implicit overlays retired by #419")
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
	if !strings.Contains(cfg.MessageFooter, "You can talk to:\n{contacts_section}") {
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

func TestLoadConfig_ProjectLocalMarkdown_EmptyUINodeOverridesEmbeddedDefault(t *testing.T) {
	t.Skip("project-local implicit overlays retired by #419")
	tmpDir := t.TempDir()
	_, fakeHome := setupXDGAndHome(t, tmpDir)

	projectDir := filepath.Join(fakeHome, "project")
	localConfigDir := filepath.Join(projectDir, ".tmux-a2a-postman")
	writeFile(t, filepath.Join(localConfigDir, "postman.md"), "---\nui_node:\n---\n")

	t.Chdir(projectDir)

	cfg, err := LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.UINode != "" {
		t.Fatalf("UINode: got %q, want empty", cfg.UINode)
	}
	if !cfg.HasExplicitUINodeSetting() {
		t.Fatal("project-local Markdown empty ui_node should remain an explicit setting")
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
