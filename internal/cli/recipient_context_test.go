package cli

import (
	"slices"
	"strings"
	"testing"

	helpdata "github.com/i9wa4/tmux-a2a-postman/help"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func TestRecipientContactsUseExactImmediateInterfaceViewpoint(t *testing.T) {
	topology := config.WorkspaceTopology{
		InterfaceNode: "top-interface",
		Edges:         map[string][]string{"worker": {"orchestrator"}, "child-interface": {"worker"}},
		Sessions: []config.WorkspaceSession{
			{Name: "top", InterfaceNode: "top-interface"},
			{Name: "child", Parent: "top", InterfaceNode: "child-interface"},
			{Name: "leaf", Parent: "child", InterfaceNode: "child-interface"},
			{Name: "sibling", Parent: "top", InterfaceNode: "child-interface"},
		},
	}
	if got := recipientAddresses(topology, "child", "child-interface"); !slices.Equal(got, []string{"leaf:child-interface", "top:top-interface", "worker"}) {
		t.Fatal("recipient contact hints leaked a sibling or lost mixed interface roles", got)
	}
	if got := recipientAddresses(topology, "child", "worker"); !slices.Equal(got, []string{"orchestrator"}) {
		t.Fatal("a non-interface role gained cross-session hints", got)
	}
	if topology.InterfaceNode != "top-interface" {
		t.Fatal("rendering changed invocation authorization")
	}
}

func TestExecutionContextShowsFixedDecisionActionsAndNoOrdinaryReply(t *testing.T) {
	msg := workspace.Message{ID: "m-0123456789abcdef0123456789abcdef", From: "worker", To: "approver", Kind: "execution-request", NeedsReply: true, GuidanceVersion: 1}
	text := renderRecipientContext(config.RecipientPolicy{}, config.WorkspaceTopology{}, "/tmp/config 'quoted'.md", "session", msg)
	for _, want := range []string{"approve-execution --config '", "decline-execution --config '", " --to " + msg.ID, "The requesting CLI waits"} {
		if !strings.Contains(text, want) {
			t.Fatalf("execution context missing %q", want)
		}
	}
	if strings.Contains(text, "send-heredoc") || !strings.Contains(text, "'\"'\"'") {
		t.Fatal("typed approval used ordinary reply guidance or lost shell quoting", text)
	}
}

func TestRecipientContextHeadingHierarchyPreservesTemplateSubsections(t *testing.T) {
	common := "### Core contract\nShared policy.\n#### Exceptions\nExplicit authority applies.\n```markdown\n# literal fenced heading\n## another literal heading\n```"
	role := "### `role`\nCommand approval owner.\n### Contract\nRead the exact request.\n#### Decision\nChoose the fixed helper."
	policy := config.RecipientPolicy{
		Common:  common,
		Catalog: "### Available Skills\n\n- `postman-usage`: mailbox handling\n",
		Nodes:   map[string]config.RecipientRole{"approver": {Instructions: role}},
	}
	msg := workspace.Message{ID: "m-0123456789abcdef0123456789abcdef", From: "worker", To: "approver", Kind: "execution-request", NeedsReply: true, GuidanceVersion: 1}
	text := renderRecipientContext(policy, config.WorkspaceTopology{}, "/tmp/postman.md", "session", msg)
	want := []string{"# Message", "## Recipient Instructions", "### Common contract", "#### Core contract", "##### Exceptions", "### Available Skills", "### approver instructions", "#### `role`", "#### Contract", "##### Decision", "## Delivery and Reply Guidance", "## Sender Message"}
	headings := recipientHeadingLines(text)
	assertRecipientHeadingHierarchy(t, headings)
	if !slices.Equal(headings, want) || !strings.Contains(text, "```markdown\n# literal fenced heading\n## another literal heading\n```") {
		t.Fatal("generated hierarchy changed template structure or fenced bytes", headings, text)
	}
}

func recipientHeadingLines(text string) []string {
	var headings []string
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
		} else if !inFence && strings.HasPrefix(line, "#") {
			headings = append(headings, line)
		}
	}
	return headings
}

func assertRecipientHeadingHierarchy(t *testing.T, headings []string) {
	t.Helper()
	h1, previous := 0, 0
	for _, heading := range headings {
		level := len(heading) - len(strings.TrimLeft(heading, "#"))
		if level == 1 {
			h1++
		}
		if level > previous+1 {
			t.Fatal("generated headings skip a level", headings)
		}
		previous = level
	}
	if h1 != 1 {
		t.Fatal("generated recipient context must have one H1", headings)
	}
}

// Fenced examples are opaque to the repository formatter; validate their
// Markdown structure explicitly as well as the actual renderer output above.
func TestRecipientMarkdownDocumentedExampleHasOneTitle(t *testing.T) {
	raw, err := helpdata.Content.ReadFile("recipient-markdown.md")
	if err != nil {
		t.Fatal(err)
	}
	_, example, ok := strings.Cut(string(raw), "````markdown\n")
	if !ok {
		t.Fatal("missing documented recipient example")
	}
	example, _, ok = strings.Cut(example, "\n````")
	if !ok {
		t.Fatal("unclosed documented recipient example")
	}
	assertRecipientHeadingHierarchy(t, recipientHeadingLines(example))
}
