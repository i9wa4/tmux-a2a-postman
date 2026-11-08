package cli

import (
	"slices"
	"strings"
	"testing"
	"time"
	_ "time/tzdata"

	helpdata "github.com/i9wa4/tmux-a2a-postman/help"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func TestLocalTimestampShowsOffsetAcrossDateAndDSTBoundaries(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ recorded, want string }{
		{"2026-01-01T01:30:00Z", "2025-12-31T20:30:00-05:00"},
		{"2026-03-08T06:59:59Z", "2026-03-08T01:59:59-05:00"},
		{"2026-03-08T07:00:00Z", "2026-03-08T03:00:00-04:00"},
	} {
		if got := localTimestamp(tc.recorded, newYork); got != tc.want {
			t.Fatalf("local timestamp for %s: got %s, want %s", tc.recorded, got, tc.want)
		}
	}
	if got := localTimestamp("2026-01-01T00:00:00Z", time.UTC); got != "2026-01-01T00:00:00+00:00" {
		t.Fatal("UTC display requires an explicit numeric offset", got)
	}
}

func TestRecipientContextUsesLocalDisplayOnlyForHumanFields(t *testing.T) {
	previous := time.Local
	time.Local = time.FixedZone("JST", 9*60*60)
	t.Cleanup(func() { time.Local = previous })
	msg := workspace.Message{ID: "m-20260101T000000.000000000Z-aA09Zz", From: "worker", To: "peer", CreatedAt: "2026-01-01T00:00:00Z"}
	policy := config.RecipientPolicy{Footer: "Sent {timestamp}"}
	rendered := renderRecipientContext(policy, config.WorkspaceTopology{}, "", "session", msg)
	if !strings.Contains(rendered, "Timestamp: `2026-01-01T09:00:00+09:00`") || !strings.Contains(rendered, "Sent 2026-01-01T09:00:00+09:00") {
		t.Fatal("recipient context did not localize both human-facing fields", rendered)
	}
	if msg.CreatedAt != "2026-01-01T00:00:00Z" {
		t.Fatal("rendering changed canonical UTC metadata")
	}
}

func TestDirectoryContactsMatchNearestLiveInterfaceRoutes(t *testing.T) {
	topology := config.WorkspaceTopology{InterfaceNode: "messenger", Edges: map[string][]string{"messenger": {"worker"}}}
	interfaces := []workspace.DirectoryInterface{
		{Session: "top", Root: "~/github.com"},
		{Session: "child", Root: "~/github.com/repo"},
		{Session: "leaf", Root: "~/github.com/repo/sub"},
		{Session: "sibling", Root: "~/github.com/other"},
	}
	if got := recipientAddressesDirectory(topology, "child", "messenger", interfaces); !slices.Equal(got, []string{"leaf:messenger", "top:messenger", "worker"}) {
		t.Fatal("directory contacts do not match direct routes", got)
	}
	if got := recipientAddressesDirectory(topology, "top", "messenger", interfaces); !slices.Equal(got, []string{"child:messenger", "sibling:messenger", "worker"}) {
		t.Fatal("parent contact hints skipped a live level", got)
	}
	if got := recipientAddressesDirectory(topology, "child", "worker", interfaces); len(got) != 0 {
		t.Fatal("non-interface role gained directory contacts", got)
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
