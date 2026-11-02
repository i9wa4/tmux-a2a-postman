package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func TestDocumentedPracticalConfigRendersRecipientSpecificContext(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("..", "..", "examples", "practical", "postman.md"))
	if err != nil {
		t.Fatal(err)
	}
	topology, err := config.LoadWorkspaceTopology(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := topology.RecipientPolicy(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	msg := workspace.Message{ID: "m-example", From: "manager", To: "builder", NeedsReply: true}
	rendered := renderRecipientContext(policy, topology, path, "team", msg)
	for _, want := range []string{policy.Common, policy.Nodes["builder"].Instructions, "`postman-usage`", "You can talk to:", "Review exact execution requests", "--to m-example", "Allowed contacts: approver, manager", "through EOF"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q in %s", want, rendered)
		}
	}
	if strings.Contains(rendered, "\n## reviewer instructions\n") || strings.Contains(rendered, "{reply_command}") || strings.Contains(rendered, "{can_talk_to}") {
		t.Fatal("wrong role or unresolved documented substitutions", rendered)
	}
}
