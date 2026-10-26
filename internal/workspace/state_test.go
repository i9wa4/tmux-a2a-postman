package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func mustState(t *testing.T, m Mail, role string) SelfState {
	t.Helper()
	allowed := []string{"worker"}
	if role == "worker" {
		allowed = []string{"peer"}
	}
	state := m.State(context.Background(), mailRuntime(role), allowed)
	if !state.Known || state.Unread == nil {
		t.Fatalf("unavailable: %+v", state)
	}
	return state
}

func sendStateMessage(t *testing.T, m Mail, role, to, body, reply, fills string, required bool) Message {
	t.Helper()
	msg, err := m.SendWithReply(context.Background(), mailRuntime(role), to, body, reply, fills, required)
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func claimStateMessage(t *testing.T, m Mail, role, id string) {
	t.Helper()
	msg, err := m.Claim(context.Background(), mailRuntime(role), id)
	if err != nil || msg == nil {
		t.Fatal("claim", msg, err)
	}
}

func treeFacts(t *testing.T, root string) map[string]string {
	t.Helper()
	facts := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fact := info.Mode().String() + ":" + info.ModTime().String()
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			fact += ":" + hex.EncodeToString(sum[:])
		}
		facts[rel] = fact
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func TestSelfStateEmptyPopAndReadOnlySnapshot(t *testing.T) {
	m := mailFixture(t)
	result, err := m.PopWithState(context.Background(), mailRuntime("worker"), "", []string{"peer", "peer"})
	if err != nil || result.Claim != nil || !result.Self.Known || result.Self.Unread == nil ||
		*result.Self.Unread != 0 || result.Self.NextAction != "none" || result.Self.AsOf == "" {
		t.Fatal(result, err)
	}
	before := treeFacts(t, m.Store.StateRoot)
	state := mustState(t, m, "worker")
	if state.Workspace != "~/repo" || state.Node != "worker" || !filepath.IsAbs(state.Mailbox) ||
		!reflect.DeepEqual(state.Peers, []string{"peer"}) {
		t.Fatal(state)
	}
	if after := treeFacts(t, m.Store.StateRoot); !reflect.DeepEqual(before, after) {
		t.Fatal("status mutated saved state")
	}
	raw, err := json.Marshal(state)
	if err != nil || strings.Contains(string(raw), "body") {
		t.Fatal("body leaked", err)
	}
}

func TestSelfStateTracksExactInboundOutboundAcrossPopAndUnrelatedSend(t *testing.T) {
	m := mailFixture(t)
	first := sendStateMessage(t, m, "worker", "peer", "first", "", "", true)
	second := sendStateMessage(t, m, "worker", "peer", "second", "", "", true)
	back := sendStateMessage(t, m, "peer", "worker", "dependency", "", "", true)
	peer := mustState(t, m, "peer")
	if *peer.Unread != 2 || len(peer.Inbound) != 2 || len(peer.Outbound) != 1 || peer.NextAction != "pop" {
		t.Fatal(peer)
	}
	claimStateMessage(t, m, "peer", first.ID)
	// POP and a reply-to-only status update leave the exact obligation open.
	sendStateMessage(t, m, "peer", "worker", "progress", first.ID, "", false)
	peer = mustState(t, m, "peer")
	if len(peer.Inbound) != 2 || len(peer.Outbound) != 1 {
		t.Fatal("unrelated send closed request", peer)
	}
	sendStateMessage(t, m, "peer", "worker", "answer", first.ID, first.InputRequestID, false)
	peer = mustState(t, m, "peer")
	if len(peer.Inbound) != 1 || peer.Inbound[0].MessageID != second.ID || len(peer.Outbound) != 1 ||
		peer.Outbound[0].InputRequestID != back.InputRequestID {
		t.Fatal(peer)
	}
	worker := mustState(t, m, "worker")
	if len(worker.Outbound) != 1 || worker.Outbound[0].MessageID != second.ID || len(worker.Inbound) != 1 ||
		worker.Inbound[0].MessageID != back.ID || *worker.Unread != 3 {
		t.Fatal("unread reply not distinguished", worker)
	}
	claimStateMessage(t, m, "peer", second.ID)
	peer = mustState(t, m, "peer")
	if *peer.Unread != 0 || peer.NextAction != "reply" || !peer.Inbound[0].Claimed {
		t.Fatal(peer)
	}
}

func TestSelfStateRejectsWrongCorrelationAndRequiresClaim(t *testing.T) {
	m := mailFixture(t)
	first := sendStateMessage(t, m, "worker", "peer", "one", "", "", true)
	second := sendStateMessage(t, m, "worker", "peer", "two", "", "", true)
	if _, err := m.SendWithReply(context.Background(), mailRuntime("peer"), "worker", "answer", first.ID, first.InputRequestID, false); err == nil {
		t.Fatal("unread request was treated as claimed history")
	}
	claimStateMessage(t, m, "peer", first.ID)
	if _, err := m.SendWithReply(context.Background(), mailRuntime("peer"), "worker", "answer", first.ID, second.InputRequestID, false); err == nil {
		t.Fatal("wrong exact request accepted")
	}
	if state := mustState(t, m, "worker"); len(state.Outbound) != 2 {
		t.Fatal("wrong correlation filled request", state)
	}
}

func TestSelfStateUnavailableDoesNotFabricateZeroOrCreateState(t *testing.T) {
	m := mailFixture(t)
	state := m.State(context.Background(), mailRuntime("worker"), []string{"peer"})
	if state.Known || state.Unread != nil || state.UnavailableReason == "" {
		t.Fatal(state)
	}
	if _, err := os.Stat(m.Store.StateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read created state", err)
	}
	sendMail(t, m)
	broken := mailPath(m, "worker", "sent", "m-"+strings.Repeat("a", 32))
	if err := os.WriteFile(broken, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := treeFacts(t, m.Store.StateRoot)
	state = m.State(context.Background(), mailRuntime("worker"), []string{"peer"})
	if state.Known || state.Unread != nil || state.Inbound != nil || state.Outbound != nil || state.Peers != nil || state.NextAction != "inspect state error" {
		t.Fatal("fabricated known state", state)
	}
	if after := treeFacts(t, m.Store.StateRoot); !reflect.DeepEqual(before, after) {
		t.Fatal("failed snapshot mutated state")
	}
}

func TestSelfStateDiagnosticsDoNotUndoSendOrClaim(t *testing.T) {
	m := mailFixture(t)
	result, err := m.SendWithState(context.Background(), mailRuntime("worker"), "peer", "committed", "", "", false, []string{"unbound"})
	if err != nil || result.Self.Known || !messageID(result.Message.ID) {
		t.Fatal(result, err)
	}
	if _, err := os.Stat(mailPath(m, "peer", "inbox", result.Message.ID)); err != nil {
		t.Fatal("diagnostic error undid send", err)
	}
	pop, err := m.PopWithState(context.Background(), mailRuntime("peer"), "", []string{"unbound"})
	if err != nil || pop.Claim == nil || pop.Self.Known {
		t.Fatal(pop, err)
	}
	if _, err := os.Stat(pop.Claim.Path); err != nil {
		t.Fatal("diagnostic error undid claim", err)
	}
}

func TestSelfStatePendingIntentIsUnknownAndDoesNotRecover(t *testing.T) {
	m := mailFixture(t)
	failure := errors.New("interrupt")
	m.afterStep = func(step string) error {
		if step == "send-inbox" {
			return failure
		}
		return nil
	}
	if _, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "partial", "", true); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	m.afterStep = nil
	before := treeFacts(t, m.Store.StateRoot)
	state := m.State(context.Background(), mailRuntime("worker"), []string{"peer"})
	if state.Known || state.Unread != nil || !strings.Contains(state.UnavailableReason, "pending") {
		t.Fatal(state)
	}
	if after := treeFacts(t, m.Store.StateRoot); !reflect.DeepEqual(before, after) {
		t.Fatal("status recovered a transaction")
	}
	if err := m.Recover(context.Background(), mailRuntime("worker")); err != nil {
		t.Fatal(err)
	}
	if state := mustState(t, m, "worker"); len(state.Outbound) != 1 {
		t.Fatal(state)
	}
}

func TestSelfStateDoesNotInspectOtherAgentsFiles(t *testing.T) {
	m := mailFixture(t)
	sendMail(t, m)
	// Own snapshot does not read another node's corrupt sent archive.
	path := mailPath(m, "peer", "sent", "m-"+strings.Repeat("b", 32))
	if err := os.WriteFile(path, []byte("invalid unrelated history"), 0o600); err != nil {
		t.Fatal(err)
	}
	if state := mustState(t, m, "worker"); len(state.Outbound) != 1 {
		t.Fatal(state)
	}
}

func TestSelfStateActivityIsReadOnlyUnknownForMissingStaleAndConflictingEvidence(t *testing.T) {
	m := mailFixture(t)
	rt := mailRuntime("worker")
	msg := sendStateMessage(t, m, "worker", "peer", "private activity payload", "", "", false)
	if got := mustState(t, m, "worker").Activity; got.Status != "unknown" || got.Reason != "no saved pane observation" {
		t.Fatal(got)
	}
	path := filepath.Join(m.Store.StateRoot, "repo", "agents", "worker", "captures")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	owner := rt.snapshot.Caller.Owner
	now := time.Now().UTC()
	text := "private pane activity"
	digest := sha256.Sum256([]byte(text))
	record := CaptureResult{
		Version: 1, MessageID: msg.ID, CapturedAt: now.Format(time.RFC3339Nano), Incarnation: owner.Incarnation,
		Observations: []PaneObservation{{
			Role: "worker", Pane: owner.Pane, Incarnation: owner.Incarnation,
			CapturedAt: now.Format(time.RFC3339Nano), Text: text, Fingerprint: "sha256:" + hex.EncodeToString(digest[:]),
		}},
	}
	name := msg.ID + ".json"
	check := func(want string) {
		t.Helper()
		before := treeFacts(t, m.Store.StateRoot)
		state := mustState(t, m, "worker")
		if state.Activity.Status != "unknown" || state.Activity.Reason != want ||
			state.Activity.ObservedAt == "" || state.Activity.AgeSeconds == nil ||
			strings.Contains(state.Activity.Reason, text) {
			t.Fatalf("activity: %+v, want reason %q", state.Activity, want)
		}
		if after := treeFacts(t, m.Store.StateRoot); !reflect.DeepEqual(before, after) {
			t.Fatal("activity read mutated saved files")
		}
		encoded, err := json.Marshal(state)
		if err != nil || strings.Contains(string(encoded), text) {
			t.Fatalf("pane text leaked through status: %v", err)
		}
	}
	put := func() {
		t.Helper()
		if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if err := writeJSON(root, name, record); err != nil {
			t.Fatal(err)
		}
	}
	put()
	check("saved pane observation cannot prove current work")
	record.CapturedAt = now.Add(-10 * time.Minute).Format(time.RFC3339Nano)
	record.Observations[0].CapturedAt = record.CapturedAt
	put()
	check("saved pane observation is stale")
	record.CapturedAt = now.Format(time.RFC3339Nano)
	put()
	check("saved pane observation is stale")
	record.CapturedAt = now.Add(-10 * time.Minute).Format(time.RFC3339Nano)
	record.Observations[0].CapturedAt = now.Format(time.RFC3339Nano)
	put()
	check("saved pane observation is stale")
	ws, err := os.OpenRoot(filepath.Join(m.Store.StateRoot, "repo"))
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()
	record.CapturedAt = now.Add(500 * time.Millisecond).Format(time.RFC3339Nano)
	record.Observations[0].CapturedAt = record.CapturedAt
	put()
	if got := readOwnActivity(ws, rt.snapshot.Caller, now); got.Status != "unknown" ||
		got.Reason != "saved pane observation conflicts with current time" || got.AgeSeconds != nil {
		t.Fatal("subsecond future observation was accepted", got)
	}
	record.CapturedAt = now.Format(time.RFC3339Nano)
	record.Observations[0].CapturedAt = record.CapturedAt
	record.Incarnation = "other-incarnation"
	put()
	check("saved pane observation conflicts with current incarnation")
	record.Incarnation = owner.Incarnation
	record.Observations[0].Pane = "%other"
	put()
	check("saved pane observation conflicts with current pane")
	record.Observations[0].Pane = owner.Pane
	record.Observations[0].Fingerprint = "sha256:invalid"
	put()
	check("saved pane evidence invalid")
}
