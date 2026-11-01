package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// Test-only exact-ID evidence augments the public compact summary for crash
// and selective-reply assertions. Production exposes counts, never this view.
type testObligation struct {
	MessageID, InputRequestID, Peer string
	Claimed                         bool
}
type testMailView struct {
	Workspace         string
	Known             bool
	Unread            *int
	Inbound, Outbound []testObligation
	UnavailableReason string
}

func stateForTest(ctx context.Context, m Mail, rt Runtime) testMailView {
	var view testMailView
	snapshot, err := rt.Snapshot(ctx)
	if err != nil {
		view.UnavailableReason = err.Error()
		return view
	}
	workspaceRoot, _, summary := m.Summary(ctx, rt, snapshot.Caller.Role)
	view.Workspace = workspaceRoot
	view.Unread = summary.UnreadCount
	view.Known = summary.UnreadCount != nil && summary.InboundCount != nil && summary.OutboundCount != nil
	view.UnavailableReason = summary.Reason
	if !view.Known {
		return view
	}
	_ = m.Store.withView(ctx, rt, func(ws *os.Root, s Snapshot) error {
		inbox, err := readSummaryBox(ws, s.Caller.Role, "inbox")
		if err != nil {
			return err
		}
		read, err := readSummaryBox(ws, s.Caller.Role, "read")
		if err != nil {
			return err
		}
		sent, err := readSummaryBox(ws, s.Caller.Role, "sent")
		if err != nil {
			return err
		}
		incoming := append(append([]Message{}, inbox...), read...)
		peer := func(msg Message, inbound bool) string {
			if inbound {
				if msg.FromSession != "" {
					return msg.FromSession + ":" + msg.From
				}
				return msg.From
			}
			if msg.ToSession != "" {
				return msg.ToSession + ":" + msg.To
			}
			return msg.To
		}
		for i, req := range incoming {
			if !req.NeedsReply {
				continue
			}
			answered := false
			for _, reply := range sent {
				if exactAnswer(req, reply) {
					answered = true
				}
			}
			if !answered {
				view.Inbound = append(view.Inbound, testObligation{req.ID, req.InputRequestID, peer(req, true), i >= len(inbox)})
			}
		}
		for _, req := range sent {
			if !req.NeedsReply {
				continue
			}
			answered := false
			for _, reply := range incoming {
				if exactAnswer(req, reply) {
					answered = true
				}
			}
			if !answered {
				view.Outbound = append(view.Outbound, testObligation{req.ID, req.InputRequestID, peer(req, false), true})
			}
		}
		return nil
	})
	return view
}

func mustState(t *testing.T, m Mail, role string) testMailView {
	t.Helper()
	state := stateForTest(context.Background(), m, mailRuntime(role))
	if !state.Known {
		t.Fatal(state)
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
