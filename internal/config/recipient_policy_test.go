package config

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRecipientPolicyRestoresOrdinaryCatalogWithoutOpeningPingOnlyRoots(t *testing.T) {
	directory := t.TempDir()
	skill := filepath.Join(directory, "catalog", "example")
	if err := os.MkdirAll(skill, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("---\nname: example\ndescription: |\n  First description\n  second line.\nmetadata:\n  version: one\n---\nIgnored body\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	content := "---\nskill_path:\n  - path: catalog\n    skills: [example]\n  - path: /nonexistent/ping-only-catalog\n    inject: [ping, compaction_ping]\n---\n" +
		"## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `common_template`\nCommon policy\n" +
		"## `worker`\n### 3.1. `role`\nExecutor description.\n### Contract\nFull worker policy\n"
	path := filepath.Join(directory, "postman.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	topology, err := LoadWorkspaceTopology(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := topology.RecipientPolicy(context.Background(), path)
	if err != nil || policy.Common != "Common policy" || policy.Nodes["worker"].Role != "Executor description." ||
		!strings.Contains(policy.Nodes["worker"].Instructions, "Full worker policy") ||
		!strings.Contains(policy.Catalog, "- `example`: First description second line.") || strings.Contains(policy.Catalog, "Ignored body") {
		t.Fatal(policy, err)
	}
	// The already-validated source is retained. A later file replacement cannot
	// combine unrelated routing and role policy during this same invocation.
	if err := os.WriteFile(path, []byte("malformed replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	loadedAgain, err := topology.RecipientPolicy(context.Background(), path)
	if err != nil || loadedAgain.Common != policy.Common {
		t.Fatal("policy source changed after route validation", loadedAgain, err)
	}
}

func TestRecipientPolicyFencedHeadingsDoNotCreateRoles(t *testing.T) {
	content := "## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `common_template`\n" +
		"````markdown\n## `impostor`\n```\n## `worker`\n````\n" +
		"## `worker`\n### `role`\nWorker\n"
	path := topologyFixture(t, content)
	topology, err := LoadWorkspaceTopology(context.Background(), path)
	if err != nil || !slices.Equal(topology.Nodes, []string{"peer", "worker"}) {
		t.Fatal("fenced presentation text became routing authority", topology, err)
	}
	policy, err := topology.RecipientPolicy(context.Background(), path)
	if err != nil || !strings.Contains(policy.Common, "## `impostor`") || !strings.Contains(policy.Common, "## `worker`") {
		t.Fatal("fenced common policy was split or lost", policy, err)
	}
	if got := MarkdownSectionContent("# Outside\n\n" + policy.Common + "\n\n## After"); got != "### Outside\n\n"+policy.Common+"\n\n### After" {
		t.Fatal("generated heading normalization changed fenced code", got)
	}
}

func TestUnreadableOrdinaryCatalogDoesNotInvalidateReadOnlyTopology(t *testing.T) {
	path := topologyFixture(t, "---\nskill_path: /nonexistent/ordinary-catalog\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n")
	topology, err := LoadWorkspaceTopology(context.Background(), path)
	if err != nil {
		t.Fatal("read-only topology opened an optional catalog", err)
	}
	if _, err := topology.RecipientPolicy(context.Background(), path); err == nil {
		t.Fatal("configured ordinary catalog disappeared without a rendering error")
	}
}

func TestRecipientRoleRetainsLegacyFrontmatterFallback(t *testing.T) {
	role, err := roleDescription("---\nrole: >-\n  Original first line\n  and continuation.\n---\nFull contract\n")
	if err != nil || role != "Original first line and continuation." {
		t.Fatal(role, err)
	}
	if _, err := roleDescription("---\nrole: [ambiguous, role]\n---\nContract"); err == nil {
		t.Fatal("non-string role description was silently dropped")
	}
}
