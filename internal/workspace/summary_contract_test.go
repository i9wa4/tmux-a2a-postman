package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestNodeSummaryCountsMailboxBeyondTransportScanLimit(t *testing.T) {
	m := mailFixture(t)
	first := sendStateMessage(t, m, "worker", "peer", "first", "", "", true)
	second := sendStateMessage(t, m, "worker", "peer", "second", "", "", true)
	third := sendStateMessage(t, m, "worker", "peer", "third", "", "", true)
	for _, request := range []Message{first, second, third} {
		claimStateMessage(t, m, "peer", request.ID)
	}
	if _, err := m.SendReply(context.Background(), mailRuntime("peer"), []string{first.ID, second.ID}, "combined answer\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SendWithReply(context.Background(), mailRuntime("peer"), "worker", "legacy answer\n", third.ID, third.InputRequestID, false); err != nil {
		t.Fatal(err)
	}
	msg := sendMail(t, m)
	msg.Body = "archived mail\n"
	box := filepath.Dir(mailPath(m, "worker", "sent", msg.ID))
	readBox := filepath.Dir(mailPath(m, "peer", "read", msg.ID))
	claimedAt := time.Now().UTC().Format(time.RFC3339Nano)
	for i := 0; i < 2049; i++ {
		msg.ID = fmt.Sprintf("m-%032x", i)
		msg.InputRequestID = fmt.Sprintf("ireq_%032x", i)
		data, err := encodeMessage(msg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(box, msg.ID+".md"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(readBox, msg.ID+".md"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		receipt, err := json.Marshal(ClaimReceipt{Version: 1, ID: msg.ID, Role: "peer", ClaimedAt: claimedAt})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(readBox, msg.ID+".pop.json"), append(receipt, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Each direction exceeds the transport cap; the claimed peer archive has
	// both message and receipt entries, for more than 4096 entries overall.
	if err := m.Store.withView(context.Background(), mailRuntime("worker"), func(ws *os.Root, _ Snapshot) error {
		for _, box := range []struct{ role, name string }{{"worker", "sent"}, {"peer", "read"}} {
			_, err := readSummaryBox(context.Background(), ws, box.role, box.name)
			if err == nil || err.Error() != "mail snapshot exceeds bounded scan" {
				t.Fatalf("transport scan %s/%s: %v", box.role, box.name, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	node := nodeFacts(t, m, "worker")
	if node.OutboundCount == nil || *node.OutboundCount != 2050 || node.InboundCount == nil || *node.InboundCount != 0 || node.UnreadCount == nil || *node.UnreadCount != 2 {
		t.Fatal(node)
	}
	ctx, cancel := context.WithTimeout(context.Background(), StatusRefreshBudget)
	defer cancel()
	started := time.Now()
	_, _, nodes, err := m.Summaries(ctx, mailRuntime("worker"), []string{"worker", "peer"})
	t.Logf("two-role Summaries with >4096 mailbox entries: %s", time.Since(started))
	if err != nil || len(nodes) != 2 || nodes[0].OutboundCount == nil || *nodes[0].OutboundCount != 2050 || nodes[1].InboundCount == nil || *nodes[1].InboundCount != 2050 || nodes[1].LastPopAt == nil || *nodes[1].LastPopAt != claimedAt {
		t.Fatal(nodes, err)
	}
}

func TestNodeSummaryPropagatesInvalidPopTime(t *testing.T) {
	m := mailFixture(t)
	msg := sendMail(t, m)
	claimStateMessage(t, m, "peer", msg.ID)
	receiptPath := strings.TrimSuffix(mailPath(m, "peer", "read", msg.ID), ".md") + ".pop.json"
	var receipt ClaimReceipt
	data, err := os.ReadFile(receiptPath)
	if err != nil || json.Unmarshal(data, &receipt) != nil {
		t.Fatal(err)
	}
	receipt.ClaimedAt = "invalid"
	data, err = json.Marshal(receipt)
	if err != nil || os.WriteFile(receiptPath, append(data, '\n'), 0o600) != nil {
		t.Fatal(err)
	}
	_, _, node := m.Summary(context.Background(), mailRuntime("peer"), "peer")
	if node.Reason != "invalid POP receipt time" || node.LastPopAt != nil || node.InboundCount != nil {
		t.Fatal(node)
	}
}

func TestStatusSummaryHonorsCancellation(t *testing.T) {
	m := mailFixture(t)
	msg := sendMail(t, m)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Store.withView(context.Background(), mailRuntime("worker"), func(ws *os.Root, _ Snapshot) error {
		box, err := openOwnBox(ws, "worker", "sent")
		if err != nil {
			return err
		}
		defer func() { _ = box.Close() }()
		_, err = statusEntries(ctx, box)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("directory scan: %v", err)
		}
		indexed, err := indexAnswers(ctx, []Message{msg})
		if !errors.Is(err, context.Canceled) || indexed != nil {
			t.Fatalf("answer index: %v", err)
		}
		_, err = hasExactAnswer(ctx, msg, map[string][]*Message{msg.ID: {&msg}})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("answer match: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
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
