package externalchannel

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

func localTextFixture(t *testing.T) (*LocalTextAdapter, Binding, string) {
	t.Helper()
	sessionDir := filepath.Join(t.TempDir(), "owner")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatal(err)
	}
	restore := message.SetDeliveryNotificationHookForTest(func(controlplane.Target, *config.Config, map[string][]string, map[string]discovery.NodeInfo, string, string, string, string, string, map[string]bool) {
	})
	t.Cleanup(restore)
	b := Binding{ChannelID: "text", ConversationID: "conversation-1", PrincipalID: "alice", Credential: "secret", SessionName: "owner"}
	adapter, err := NewLocalTextAdapter(&config.Config{InterfaceNode: "mouthpiece"}, func(session, node string) (discovery.NodeInfo, bool) {
		if session != "owner" || node != "mouthpiece" {
			return discovery.NodeInfo{}, false
		}
		return discovery.NodeInfo{SessionName: session, SessionDir: sessionDir, PaneID: "%9"}, true
	}, b)
	if err != nil {
		t.Fatal(err)
	}
	return adapter, b, sessionDir
}

func requestFor(b Binding, key, body string) Request {
	return Request{ChannelID: b.ChannelID, ConversationID: b.ConversationID, PrincipalID: b.PrincipalID, Credential: b.Credential, IdempotencyKey: key, Body: body}
}

func TestLocalTextReceiveAndReadReport(t *testing.T) {
	a, b, sessionDir := localTextFixture(t)
	req := requestFor(b, "request-1", "hello \x1b[31mred\x1b[0m")
	receipt, err := a.Receive(req)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ExchangeID == "" || receipt.MessageID == "" {
		t.Fatalf("empty receipt: %+v", receipt)
	}
	path := filepath.Join(sessionDir, "inbox", "mouthpiece", receipt.MessageID)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{"from: postman", "externalChannel: \"text\"", "externalPrincipal: \"alice\"", "hello red", "not human approval evidence"} {
		if !strings.Contains(content, want) {
			t.Errorf("delivered message missing %q: %s", want, content)
		}
	}
	if strings.Contains(content, "\x1b") {
		t.Fatal("control sequence reached mailbox")
	}
	duplicate, err := a.Receive(req)
	if err != nil || duplicate != receipt {
		t.Fatalf("duplicate = %+v, %v; want %+v", duplicate, err, receipt)
	}
	entries, err := os.ReadDir(filepath.Join(sessionDir, "inbox", "mouthpiece"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("inbox entries = %d, %v; want 1", len(entries), err)
	}
	if err := a.RecordReport(receipt.ExchangeID, "done \x1b[32mgreen\x1b[0m"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReadReport(b.ChannelID, b.ConversationID, b.PrincipalID, "wrong", receipt.ExchangeID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong secret read = %v; want unauthorized", err)
	}
	report, err := a.ReadReport(b.ChannelID, b.ConversationID, b.PrincipalID, b.Credential, receipt.ExchangeID)
	if err != nil || report.Body != "done green" {
		t.Fatalf("report = %+v, %v", report, err)
	}
}

func TestLocalTextRejectsUnauthorizedOrStaleBinding(t *testing.T) {
	a, b, sessionDir := localTextFixture(t)
	base := requestFor(b, "request-1", "hello")
	for name, change := range map[string]func(*Request){
		"channel":      func(r *Request) { r.ChannelID = "other" },
		"conversation": func(r *Request) { r.ConversationID = "other" },
		"principal":    func(r *Request) { r.PrincipalID = "mallory" },
		"credential":   func(r *Request) { r.Credential = "wrong" },
	} {
		t.Run(name, func(t *testing.T) {
			req := base
			change(&req)
			if _, err := a.Receive(req); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("Receive() = %v; want unauthorized", err)
			}
		})
	}
	if _, err := a.Receive(base); err != nil {
		t.Fatal(err)
	}
	base.Body = "changed"
	if _, err := a.Receive(base); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed duplicate = %v; want conflict", err)
	}
	a.Revoke()
	if _, err := a.Receive(requestFor(b, "request-2", "hello")); !errors.Is(err, ErrBindingInactive) {
		t.Fatalf("revoked binding = %v; want inactive", err)
	}
	newBinding := b
	newBinding.Credential = "new-secret"
	if err := a.Rebind(newBinding); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Receive(requestFor(b, "request-2", "hello")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("old credential after rebind = %v; want unauthorized", err)
	}
	if _, err := a.Receive(requestFor(newBinding, "request-2", "hello")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(sessionDir, "inbox", "mouthpiece"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("authorized deliveries = %d, %v; want 2", len(entries), err)
	}
}

func TestLocalTextRequiresLiveBoundTarget(t *testing.T) {
	b := Binding{ChannelID: "text", ConversationID: "conversation-1", PrincipalID: "alice", Credential: "secret", SessionName: "owner"}
	if _, err := NewLocalTextAdapter(&config.Config{}, func(string, string) (discovery.NodeInfo, bool) { return discovery.NodeInfo{}, true }, b); !errors.Is(err, ErrTargetUnavailable) {
		t.Fatalf("missing interface = %v; want unavailable", err)
	}
	for _, target := range []discovery.NodeInfo{{}, {SessionName: "foreign", SessionDir: t.TempDir(), PaneID: "%9"}, {SessionName: "owner", SessionDir: t.TempDir()}, {SessionName: "owner", SessionDir: t.TempDir(), PaneID: "%9", Collision: true}} {
		a, err := NewLocalTextAdapter(&config.Config{InterfaceNode: "mouthpiece"}, func(string, string) (discovery.NodeInfo, bool) { return target, true }, b)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.Receive(requestFor(b, "request-1", "hello")); !errors.Is(err, ErrTargetUnavailable) {
			t.Fatalf("target %+v: Receive() = %v; want unavailable", target, err)
		}
	}
}
