package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func topologyFixture(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "postman.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWorkspaceTopologyReadsReviewerWithoutCatalogSideEffects(t *testing.T) {
	path := topologyFixture(t, "---\ninterface_node: worker\nskill_path: /nonexistent/issue937-do-not-open-catalog\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- approver:::command_approver_node\n```\n## `worker`\n### `role`\nWorker\n## `approver`\nApprover\n")
	got, err := LoadWorkspaceTopology(context.Background(), path)
	if err != nil || got.Reviewer != "approver" || got.InterfaceNode != "worker" ||
		len(got.Nodes) != 2 || len(got.Edges["worker"]) != 1 {
		t.Fatal(got, err)
	}
	// Configuration symlinks are distinct from the strict runtime-record rules.
	alias := filepath.Join(t.TempDir(), "postman.md")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	linked, err := LoadWorkspaceTopology(context.Background(), alias)
	if err != nil || linked.Reviewer != got.Reviewer {
		t.Fatal(linked, err)
	}
}

func TestWorkspaceTopologyVerifiedAbsenceAndUnknownInputs(t *testing.T) {
	absent := topologyFixture(t, "## `worker`\nWorker\n")
	got, err := LoadWorkspaceTopology(context.Background(), absent)
	if err != nil || got.Reviewer != "" || len(got.Nodes) != 1 {
		t.Fatal(got, err)
	}
	for _, content := range []string{
		"---\ninterface_node: &node worker\nreply_command: *node\n---\n## `worker`\nWorker\n",
		"---\ninterface_node: worker\nInterface_Node: worker\n---\n## `worker`\nWorker\n",
		"---\n\"command_approver_node\": reviewer\n---\n## `worker`\nWorker\n",
		"## `edges`\n```mermaid\ngraph LR\nclass worker\n```\n## `worker`\nWorker\n",
		"## `edges`\n```mermaid\ngraph LR\nworker --- reviewer::: command_approver_node\n```\n",
		"## `edges`\n```mermaid\ngraph LR\nworker --- bad_role\n```\n",
		"---\ninterface_node: worker\n",
		"## `edges`\n```mermaid\ngraph LR\nworker --- approver:::command_approver_node\n",
		"## `edges`\n```mermaid\ngraph LR\nworker --- a:::command_approver_node\nworker --- b:::command_approver_node\n```\n",
		"## `edges`\n```mermaid\ngraph LR\nclass missing command_approver_node\n```\n## `worker`\nworker\n",
		"## `../worker`\nworker\n",
		"## `worker`\na\n## `worker`\nb\n",
		"## `edges`\n```mermaid\ngraph LR\nworker --> approver:::command_approver_node\n```\n",
		"---\ncommand_approver_node: approver\n---\n## `worker`\nworker\n",
		"## `edges`\n```mermaid\ngraph LR\n```\n```mermaid\ngraph LR\n```\n",
	} {
		path := topologyFixture(t, content)
		if _, err := LoadWorkspaceTopology(context.Background(), path); err == nil {
			t.Fatal("unknown parsed as absence", content)
		}
	}
	if _, err := LoadWorkspaceTopology(context.Background(), filepath.Join(t.TempDir(), "missing.md")); err == nil {
		t.Fatal("missing parsed as absence")
	}
	if _, err := LoadWorkspaceTopology(context.Background(), "relative.md"); err == nil {
		t.Fatal("relative input")
	}
	if _, err := LoadWorkspaceTopology(context.Background(), t.TempDir()); err == nil {
		t.Fatal("directory parsed as absence")
	}
}

func TestWorkspaceTopologyBoundsAndCancellation(t *testing.T) {
	for _, content := range []string{strings.Repeat("x", 1024*1024+1), strings.Repeat("x", 64*1024+1)} {
		if _, err := LoadWorkspaceTopology(context.Background(), topologyFixture(t, content)); err == nil {
			t.Fatal("unbounded input")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LoadWorkspaceTopology(ctx, topologyFixture(t, "")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
