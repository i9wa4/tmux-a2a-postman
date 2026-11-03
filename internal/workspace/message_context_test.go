package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestRecipientContextPreservesLiteralBodyAndIndependentCopies(t *testing.T) {
	m := mailFixture(t)
	prefix := "# Message\n\n## Recipient Instructions\n\ncommon + recipient policy 日本語\n\n## Sender Message\n\n---\n\n"
	m.Context = func(_ context.Context, _ Snapshot, msg Message) (string, error) {
		if msg.From != "worker" || msg.To != "peer" || msg.ID == "" {
			t.Fatal("context must use the final recipient/message identity", msg)
		}
		return prefix, nil
	}
	body := "# literal heading\n\n<!-- postman recipient context v1 -->\n\n$HOME `code` 日本語\n\n"
	msg, err := m.Send(context.Background(), mailRuntime("worker"), "peer", body, "", true)
	if err != nil {
		t.Fatal(err)
	}
	left, err := os.ReadFile(mailPath(m, "peer", "inbox", msg.ID))
	if err != nil {
		t.Fatal(err)
	}
	right, err := os.ReadFile(mailPath(m, "worker", "sent", msg.ID))
	if err != nil || !bytes.Equal(left, right) || bytes.Count(left, []byte(prefix)) != 1 || !bytes.HasSuffix(left, []byte(body)) {
		t.Fatal("archive must contain generated context once, then exact sender bytes", err)
	}
	claim, err := m.Claim(context.Background(), mailRuntime("peer"), "")
	if err != nil || claim == nil || claim.Message.Body != body || claim.Message.Context == nil || claim.Message.Context.Markdown != prefix {
		t.Fatal("POP lost the boundary between recipient context and sender bytes", claim, err)
	}
}

func TestRecipientContextRecoveryRetainsTheOriginalConfiguration(t *testing.T) {
	m := mailFixture(t)
	m.Context = func(context.Context, Snapshot, Message) (string, error) { return "original common/role/catalog\n", nil }
	m.afterStep = func(point string) error {
		if point == "send-intent" {
			return errors.New("simulated interruption")
		}
		return nil
	}
	msg, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "literal source\n", "", true)
	if err == nil || msg.ID == "" {
		t.Fatal(msg, err)
	}
	m.afterStep = nil
	m.Context = func(context.Context, Snapshot, Message) (string, error) { return "changed configuration\n", nil }
	if err := m.Recover(context.Background(), mailRuntime("worker")); err != nil {
		t.Fatal(err)
	}
	claim, err := m.Claim(context.Background(), mailRuntime("peer"), "")
	if err != nil || claim == nil || !sameMessage(claim.Message, msg) || strings.Contains(claim.Message.Context.Markdown, "changed") {
		t.Fatal("recovery regenerated immutable instructions", claim, err)
	}
}

func TestRecipientContextTamperingIsNotClaimed(t *testing.T) {
	m := mailFixture(t)
	m.Context = func(context.Context, Snapshot, Message) (string, error) { return "original recipient contract\n", nil }
	msg := sendMail(t, m)
	path := mailPath(m, "peer", "inbox", msg.ID)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte("original recipient contract"), []byte("modified recipient contract"), 1)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	claim, err := m.Claim(context.Background(), mailRuntime("peer"), "")
	if err == nil || claim != nil {
		t.Fatal("modified generated instructions must fail canonical archive validation", claim, err)
	}
}

func TestOversizedRecipientInstructionsDoNotPublishTruncatedMail(t *testing.T) {
	m := mailFixture(t)
	m.Context = func(context.Context, Snapshot, Message) (string, error) { return strings.Repeat("x", 49*1024), nil }
	msg, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "body", "", true)
	if err == nil {
		t.Fatal("oversized context was silently accepted")
	}
	if _, err := os.Stat(mailPath(m, "peer", "inbox", msg.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial instructions were published", err)
	}
}

func TestLegacyContextHeadingsRemainClaimableAndReplyable(t *testing.T) {
	m := mailFixture(t)
	oldPrefix := "# Message\n\n## Recipient Instructions\n\n### Common contract\n\nOld captured policy.\n\n## Sender Message\n\n"
	m.Context = func(context.Context, Snapshot, Message) (string, error) { return oldPrefix, nil }
	ctx := context.Background()
	original, err := m.Send(ctx, mailRuntime("worker"), "peer", "# Literal body\n```markdown\n## unchanged\n```\n", "", true)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(mailPath(m, "worker", "sent", original.ID))
	if err != nil {
		t.Fatal(err)
	}
	newPrefix := "# Message\n\n## Recipient Instructions\n\n### Common contract\n\nNew policy.\n\n## Sender Message\n\n"
	m.Context = func(context.Context, Snapshot, Message) (string, error) { return newPrefix, nil }
	claim, err := m.Claim(ctx, mailRuntime("peer"), "")
	if err != nil || claim == nil || claim.Message.Context.Markdown != oldPrefix || claim.Message.Body != original.Body {
		t.Fatal("new renderer must not reinterpret historical captured Markdown", claim, err)
	}
	reply, err := m.SendWithReply(ctx, mailRuntime("peer"), "worker", "Exact result\n", original.ID, original.InputRequestID, false)
	if err != nil || reply.ReplyTo != original.ID || reply.Context.Markdown != newPrefix {
		t.Fatal("legacy context lost exact reply correlation", reply, err)
	}
	after, err := os.ReadFile(mailPath(m, "worker", "sent", original.ID))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("heading change rewrote the original archive", err)
	}
}
