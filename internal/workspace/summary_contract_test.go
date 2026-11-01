package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func nodeFacts(t *testing.T, m Mail, role string) NodeStatus {
	t.Helper()
	_, _, node := m.Summary(context.Background(), mailRuntime(role), role)
	if node.Reason != "" {
		t.Fatal(node.Reason)
	}
	return node
}

func TestCombinedReplyClosesOnlyReferencedRequestsOnce(t *testing.T) {
	m := mailFixture(t)
	first := sendStateMessage(t, m, "worker", "peer", "first", "", "", true)
	second := sendStateMessage(t, m, "worker", "peer", "second", "", "", true)
	third := sendStateMessage(t, m, "worker", "peer", "third", "", "", true)
	claimStateMessage(t, m, "peer", first.ID)
	claimStateMessage(t, m, "peer", second.ID)
	before := nodeFacts(t, m, "worker")
	if *before.OutboundCount != 3 {
		t.Fatal(before)
	}
	reply, err := m.SendReply(context.Background(), mailRuntime("peer"), []string{first.ID, second.ID}, "both complete\n")
	if err != nil || reply.NeedsReply || len(reply.Replies) != 2 {
		t.Fatal(reply, err)
	}
	requester, responder := nodeFacts(t, m, "worker"), nodeFacts(t, m, "peer")
	if *requester.UnreadCount != 1 || *requester.OutboundCount != 1 || *requester.InboundCount != 0 || *responder.InboundCount != 1 {
		t.Fatal(requester, responder)
	}
	if !exactAnswer(first, reply) || !exactAnswer(second, reply) || exactAnswer(third, reply) {
		t.Fatal("inexact combined reply")
	}
	for _, ids := range [][]string{{first.ID, third.ID}, {first.ID, first.ID}, {"m-00000000000000000000000000000000"}} {
		before := treeFacts(t, m.Store.StateRoot)
		if _, err := m.SendReply(context.Background(), mailRuntime("peer"), ids, "invalid"); err == nil {
			t.Fatal("accepted invalid reply IDs", ids)
		}
		if after := treeFacts(t, m.Store.StateRoot); !reflect.DeepEqual(before, after) {
			t.Fatal("invalid reply changed saved state")
		}
	}
}

func TestNodeSummaryReadsFactsWithoutMutationAndEmptyPopKeepsTime(t *testing.T) {
	m := mailFixture(t)
	msg := sendStateMessage(t, m, "worker", "peer", "literal body\n", "", "", true)
	claim, err := m.Claim(context.Background(), mailRuntime("peer"), msg.ID)
	if err != nil {
		t.Fatal(err)
	}
	node := nodeFacts(t, m, "peer")
	if node.LastPopAt == nil || *node.LastPopAt != claim.Receipt.ClaimedAt || *node.UnreadCount != 0 || *node.InboundCount != 1 {
		t.Fatal(node)
	}
	raw, err := os.ReadFile(claim.Path)
	if err != nil || !strings.Contains(string(raw), "--to "+msg.ID) || !strings.Contains(string(raw), msg.Body) {
		t.Fatal("reply guidance missing", err)
	}
	before := treeFacts(t, m.Store.StateRoot)
	_, _, nodes, err := m.Summaries(context.Background(), mailRuntime("peer"), []string{"worker", "peer", "unused"})
	if err != nil || len(nodes) != 3 || *nodes[2].UnreadCount != 0 {
		t.Fatal(nodes, err)
	}
	if after := treeFacts(t, m.Store.StateRoot); !reflect.DeepEqual(before, after) {
		t.Fatal("get-status facts mutated files")
	}
	if empty, err := m.Claim(context.Background(), mailRuntime("peer"), ""); err != nil || empty != nil {
		t.Fatal(empty, err)
	}
	after := nodeFacts(t, m, "peer")
	if after.LastPopAt == nil || *after.LastPopAt != *node.LastPopAt {
		t.Fatal("empty POP changed claim time")
	}
}

func TestDraftAndConfirmedFailurePreserveCompleteMessage(t *testing.T) {
	m := mailFixture(t)
	stop := errors.New("interrupted before intent")
	m.afterStep = func(step string) error {
		if step == "send-draft" {
			return stop
		}
		return nil
	}
	msg, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "full draft\n", "", true)
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(mailPath(m, "worker", "draft", msg.ID)); err != nil || !strings.Contains(string(data), "full draft\n") {
		t.Fatal("lost draft", err)
	}
	node := nodeFacts(t, m, "worker")
	if *node.DraftCount != 1 || *node.FailedCount != 0 {
		t.Fatal(node)
	}
	m.afterStep = nil
	if err := m.Recover(context.Background(), mailRuntime("worker")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mailPath(m, "peer", "inbox", msg.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("orphan draft was blindly delivered")
	}
	rt := mailRuntime("worker")
	rt.snapshot.Bindings = rt.snapshot.Bindings[:1]
	failed, err := m.Send(context.Background(), rt, "peer", "full failed\n", "", true)
	var delivery *DeliveryFailure
	if !errors.As(err, &delivery) || delivery.MessageID != failed.ID {
		t.Fatal(failed, err)
	}
	if data, err := os.ReadFile(mailPath(m, "worker", "failed", failed.ID)); err != nil || !strings.Contains(string(data), "full failed\n") {
		t.Fatal("lost failed body", err)
	}
	if _, err := os.Stat(filepath.Join(m.Store.StateRoot, "repo", "agents", "worker", "failed", failed.ID+".failure.json")); err != nil {
		t.Fatal(err)
	}
	node = nodeFacts(t, m, "worker")
	if *node.DraftCount != 1 || *node.FailedCount != 1 {
		t.Fatal(node)
	}
}
