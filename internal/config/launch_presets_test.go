package config

import (
	"context"
	"strings"
	"testing"
)

func TestLaunchPresetsAreOrderedDataAndNeverRenderCommandSource(t *testing.T) {
	source := "---\npresets:\n  dev:\n    columns:\n      - panes:\n          - role: worker\n            command: 'echo DO_NOT_RENDER_STARTUP_COMMAND'\n          - role: peer\n            command: |\n              printf '%s\\n' 'quoted $HOME'\n      - panes:\n          - role: reviewer\n            command: claude\n  alternate:\n    columns:\n      - panes:\n          - role: worker\n            command: codex\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\nworker --- reviewer\n```\n## `common_template`\nRead full mail.\n"
	path := topologyFixture(t, source)
	topology, err := LoadWorkspaceTopology(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	presets, err := topology.LaunchPresets()
	if err != nil || len(presets) != 2 || len(presets["dev"].Columns) != 2 || presets["dev"].Columns[0].Panes[1].Role != "peer" {
		t.Fatal(presets, err)
	}
	policy, err := topology.RecipientPolicy(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(policy.Common+policy.Catalog+policy.Footer, "DO_NOT_RENDER_STARTUP_COMMAND") {
		t.Fatal("preset command leaked into recipient policy")
	}
}

func TestLaunchPresetsKeepShellTitlesSeparateFromRoles(t *testing.T) {
	source := "---\npresets:\n  dev:\n    columns:\n      - panes: [{title: editor}]\n      - panes: [{role: agent, command: 'codex --model gpt-6-sol'}]\n---\n## `agent`\nAgent.\n"
	topology, err := LoadWorkspaceTopology(context.Background(), topologyFixture(t, source))
	if err != nil {
		t.Fatal(err)
	}
	presets, err := topology.LaunchPresets()
	if err != nil {
		t.Fatal(err)
	}
	left, right := presets["dev"].Columns[0].Panes[0], presets["dev"].Columns[1].Panes[0]
	if left.Title != "editor" || left.Role != "" || left.Command != "" || right.Title != "agent" || right.Role != "agent" || right.Command != "codex --model gpt-6-sol" {
		t.Fatal(left, right)
	}
}

func TestLaunchPresetsRejectInvalidPlansBeforeExecution(t *testing.T) {
	for _, body := range []string{
		"presets: []", "presets: {}", "presets:\n  bad_name: {}", "presets:\n  dev: {layout: []}",
		"presets:\n  dev: {columns: []}", "presets:\n  dev: {columns: [{panes: []}]}",
		"presets:\n  dev: {columns: [{panes: [{role: missing, command: codex}]}]}",
		"presets:\n  dev: {columns: [{panes: [{role: worker, command: true}]}]}",
		"presets:\n  dev: {columns: [{panes: [{role: worker, command: ''}]}]}",
		"presets:\n  dev: {columns: [{panes: [{role: worker, command: codex, model: custom}]}]}",
		"presets:\n  dev: {columns: [{panes: [{role: worker, command: codex}, {role: worker, command: claude}]}]}",
		"presets:\n  dev: {columns: [{panes: [{title: editor, command: codex}, {role: worker, command: codex}]}]}",
		"presets:\n  dev: {columns: [{panes: [{title: worker}, {role: worker, command: codex}]}]}",
		"presets:\n  dev: {columns: [{panes: [{title: editor}, {title: editor}, {role: worker, command: codex}]}]}",
		"presets:\n  dev: {columns: [{panes: [{title: editor}]}]}",
		"presets:\n  dev: {columns: [{panes: [{role: worker, title: editor, command: codex}]}]}",
	} {
		t.Run(body, func(t *testing.T) {
			path := topologyFixture(t, "---\n"+body+"\n---\n## `worker`\nWorker\n")
			topology, err := LoadWorkspaceTopology(context.Background(), path)
			if err == nil {
				_, err = topology.LaunchPresets()
			}
			if err == nil {
				t.Fatal("invalid preset accepted", body)
			}
		})
	}
}
