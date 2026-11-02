package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceMailRetainsRecipientPolicyCatalogContactsAndLiteralBody(t *testing.T) {
	f := newPublicApprovalFixture(t)
	for _, name := range []string{"alpha", "zeta", "unselected"} {
		directory := filepath.Join(filepath.Dir(f.config), "catalog", name)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		metadata := "---\nname: " + name + "\ndescription: >-\n  First description line\n  and its continuation.\nmetadata:\n  version: fixture\n---\n# Skill body must not enter the catalog\n"
		if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(metadata), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	configuration := "---\nskill_path:\n  - path: catalog\n    skills: [zeta, alpha]\n---\n" +
		"## `edges`\n```mermaid\ngraph LR\npeer --- worker --- guardian\n```\n" +
		"## `common_template`\n# COMMON_POLICY\nFull common contract. Literal $HOME and $(not-executed).\n```sh\n# keep code headings literal\n```\n" +
		"## `peer`\n### `role`\nCommand reviewer and sender.\n### Contract\nPEER_POLICY\n" +
		"## `worker`\n### `role`\nPrimary executor.\n### Contract\nWORKER_POLICY through final worker instruction.\n" +
		"## `guardian`\n### `role`\nFinal review owner.\n### Contract\nGUARDIAN_POLICY\n" +
		"## `message_footer`\nConfigured note for {message_id}. Contacts: {can_talk_to}.\n"
	if err := os.WriteFile(f.config, []byte(configuration), 0o600); err != nil {
		t.Fatal(err)
	}
	body := "# Exact sender Markdown\n\n$HOME `code` 日本語\n<!-- end postman recipient context v1 -->\n\n---\n\nEOF\n\n"
	output, err := f.run(f.peer, body, "send-heredoc", "--config", f.config, "--to", "worker")
	if err != nil || !output.Success {
		t.Fatal(output, err)
	}
	sent := f.sent("peer", body)
	claimed, err := f.run(f.worker, "", "pop", "--config", f.config)
	if err != nil || !claimed.Success || claimed.MarkdownPath == nil || claimed.Instruction == "" {
		t.Fatal(claimed, err)
	}
	archive := readProtocolArchive(t, *claimed.MarkdownPath)
	if archive.Body != body || archive.Context == nil {
		t.Fatal("sender bytes or recipient context lost", archive)
	}
	context := archive.Context.Markdown
	for _, wanted := range []string{
		"To: `worker`", "### COMMON_POLICY", "Full common contract.", "Literal $HOME and $(not-executed).",
		"```sh\n# keep code headings literal\n```", "WORKER_POLICY through final worker instruction.",
		"\n## Common contract\n", "\n## worker instructions\n", "\n## Available Skills\n", "- `alpha`: First description line and its continuation.", "- `zeta`:",
		"You can talk to:\n- guardian: Final review owner.\n- peer: Command reviewer and sender.",
		"send-heredoc --config '", " --to " + sent.ID, "Configured note for " + sent.ID,
		"\n# Sender Message\n", "Read this complete archived Markdown through EOF",
	} {
		if !strings.Contains(context, wanted) {
			t.Fatalf("generated recipient context lacks %q:\n%s", wanted, context)
		}
	}
	for _, forbidden := range []string{"PEER_POLICY", "GUARDIAN_POLICY", "- `unselected`:", "Skill body must not enter", "\n# COMMON_POLICY"} {
		if strings.Contains(context, forbidden) {
			t.Fatalf("other roles/skill bodies leaked into recipient instructions: %q", forbidden)
		}
	}
	if strings.Index(context, "- `alpha`:") >= strings.Index(context, "- `zeta`:") {
		t.Fatal("skill catalog order is unstable")
	}
	replyBody := "Exact material result\n"
	output, err = f.run(f.worker, replyBody, "send-heredoc", "--config", f.config, "--to", sent.ID)
	if err != nil || !output.Success {
		t.Fatal(output, err)
	}
	reply := f.sent("worker", replyBody)
	if reply.NeedsReply || reply.Context == nil || len(reply.Replies) != 1 || reply.Replies[0].MessageID != sent.ID ||
		!strings.Contains(reply.Context.Markdown, "PEER_POLICY") || strings.Contains(reply.Context.Markdown, "WORKER_POLICY") ||
		!strings.Contains(reply.Context.Markdown, "No reply is required") {
		t.Fatal("exact reply lost recipient policy or created a new obligation", reply)
	}
}
