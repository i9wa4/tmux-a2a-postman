package cli

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func TestDocumentedPracticalConfigRendersRecipientSpecificContext(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("..", "..", "testdata", "config", "practical.md"))
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
	for _, want := range []string{generatedSection(policy.Common), generatedSection(policy.Nodes["builder"].Instructions), "`postman-records`", "You can talk to:", "Review exact execution requests", "--to m-example", "Allowed contacts: approver, manager", "through EOF"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q in %s", want, rendered)
		}
	}
	if strings.Contains(rendered, "\n### reviewer instructions\n") || strings.Contains(rendered, "{reply_command}") || strings.Contains(rendered, "{can_talk_to}") {
		t.Fatal("wrong role or unresolved documented substitutions", rendered)
	}
}

func TestShippedFootersDoNotTargetOrdinaryReplyIDs(t *testing.T) {
	const id = "m-0123456789abcdef0123456789abcdef"
	for _, fixture := range []struct{ name, from, to, footerText string }{
		{"minimal.md", "worker", "orchestrator", "Reply using"},
		{"practical.md", "builder", "manager", "For ordinary mail, reply using"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			path, err := filepath.Abs(filepath.Join("..", "..", "testdata", "config", fixture.name))
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
			for _, reply := range []struct {
				name string
				msg  workspace.Message
			}{
				{"current", workspace.Message{ID: id, From: fixture.from, To: fixture.to, CreatedAt: "2026-01-01T00:00:00Z", Replies: []workspace.ReplyReference{{MessageID: "m-abcdef0123456789abcdef0123456789"}}}},
				{"legacy", workspace.Message{ID: id, From: fixture.from, To: fixture.to, CreatedAt: "2026-01-01T00:00:00Z", ReplyTo: "m-abcdef0123456789abcdef0123456789"}},
			} {
				t.Run(reply.name, func(t *testing.T) {
					rendered := renderRecipientContext(policy, topology, path, "team", reply.msg)
					_, footer, ok := strings.Cut(rendered, "### Configured guidance\n\n")
					if !ok || !strings.Contains(footer, fixture.footerText) || !strings.Contains(footer, "--config "+contextShellWord(path)) ||
						!strings.Contains(strings.Join(strings.Fields(footer), " "), "--to <configured-role>") ||
						regexp.MustCompile(`--to\s+`+regexp.QuoteMeta(id)+`\b`).MatchString(footer) {
						t.Fatal("shipped configured footer targets an ordinary reply ID", footer)
					}
				})
			}
		})
	}
}
