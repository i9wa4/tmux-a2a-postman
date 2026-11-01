package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
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

func TestWorkspaceTopologyAcceptsExistingTemplateSections(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("testdata", "postman-with-templates.md"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadWorkspaceTopology(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"agent", "approver", "critic", "diplomat", "guardian", "messenger", "orchestrator", "worker", "worker-alt"}
	if !slices.Equal(got.Nodes, want) || got.Reviewer != "approver" || got.InterfaceNode != "messenger" || !slices.Contains(got.Edges["orchestrator"], "worker") {
		t.Fatal("template sections changed routing or role discovery", got)
	}
}

func TestWorkspaceTopologyReservedSectionsDoNotDeclareAuthority(t *testing.T) {
	for _, section := range []string{"common_template", "message_footer"} {
		t.Run(section, func(t *testing.T) {
			// Mixed-case numbered headings use the existing heading normalization.
			content := "## 1. `" + strings.ToUpper(section) + "`\n```mermaid\ngraph LR\nworker --- impostor:::command_approver_node\n```\n## 2. `worker`\nWorker\n"
			got, err := LoadWorkspaceTopology(context.Background(), topologyFixture(t, content))
			if err != nil || !slices.Equal(got.Nodes, []string{"worker"}) || got.Reviewer != "" || len(got.Edges) != 0 {
				t.Fatal("presentation prose became topology", got, err)
			}
			for _, invalid := range []string{
				content + "## 3. `" + section + "`\nDuplicate reserved section\n",
				content + "## `unrecognized_template`\nNot a reserved section\n",
				content + "## `edges`\n```mermaid\ngraph LR\nworker --- " + section + "\n```\n",
			} {
				if _, err := LoadWorkspaceTopology(context.Background(), topologyFixture(t, invalid)); err == nil {
					t.Fatal("invalid or ambiguous topology accepted", invalid)
				}
			}
		})
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

func TestWorkspaceInterfaceRouteDirectOnly(t *testing.T) {
	path := topologyFixture(t, "---\ninterface_node: worker\nworkspace_tree:\n  - session_name: parent\n    interface_node: worker\n  - session_name: child\n    parent_session_name: parent\n    interface_node: worker\n  - session_name: grandchild\n    parent_session_name: child\n    interface_node: worker\n  - session_name: sibling\n    parent_session_name: parent\n    interface_node: worker\n---\n## `worker`\nWorker\n## `other`\nOther\n")
	topology, err := LoadWorkspaceTopology(context.Background(), path)
	if err != nil || len(topology.Sessions) != 4 {
		t.Fatal(topology, err)
	}
	for _, route := range [][3]string{{"parent", "worker", "child:worker"}, {"child", "worker", "parent:worker"}, {"child", "worker", "grandchild:worker"}} {
		if _, _, err := topology.InterfaceRoute(route[0], route[1], route[2]); err != nil {
			t.Fatal(route, err)
		}
	}
	for _, route := range [][3]string{
		{"parent", "worker", "grandchild:worker"},
		{"child", "worker", "sibling:worker"},
		{"parent", "worker", "child:other"},
		{"parent", "other", "child:worker"},
		{"parent", "worker", "worker"},
		{"parent", "worker", "unknown:worker"},
	} {
		if _, _, err := topology.InterfaceRoute(route[0], route[1], route[2]); err == nil {
			t.Fatal("unauthorized route", route)
		}
	}
}

func TestWorkspaceInterfaceTreeRejectsAmbiguity(t *testing.T) {
	for _, tree := range []string{
		"  - session_name: parent\n    interface_node: missing\n",
		"  - session_name: parent\n    interface_node: worker\n  - session_name: parent\n    interface_node: worker\n",
		"  - session_name: parent\n    parent_session_name: missing\n    interface_node: worker\n",
		"  - session_name: parent\n    parent_session_name: child\n    interface_node: worker\n  - session_name: child\n    parent_session_name: parent\n    interface_node: worker\n",
		"  - session_name: parent\n    interface_node: worker\n    unknown: value\n",
		"  - session_name: parent:worker\n    interface_node: worker\n",
	} {
		path := topologyFixture(t, "---\ninterface_node: worker\nworkspace_tree:\n"+tree+"---\n## `worker`\nWorker\n")
		if _, err := LoadWorkspaceTopology(context.Background(), path); err == nil {
			t.Fatal("ambiguous tree accepted", tree)
		}
	}
}
