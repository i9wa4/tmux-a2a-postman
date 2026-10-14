package externalchannel

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestLocalTextDecisionAnswerRequiresCurrentBoundContext(t *testing.T) {
	a, b, _ := localTextFixture(t)
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	if _, err := a.IssueDecision("unknown", "digest-1", "context-1", now.Add(time.Hour)); !errors.Is(err, ErrDecisionUnavailable) {
		t.Fatalf("unknown exchange = %v", err)
	}
	receipt, err := a.Receive(requestFor(b, "request-1", "hello"))
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := a.IssueDecision(receipt.ExchangeID, "digest-1", "context-1", now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	access := DecisionAccess{ChannelID: b.ChannelID, ConversationID: b.ConversationID, PrincipalID: b.PrincipalID, Credential: b.Credential, ExchangeID: challenge.ExchangeID, Token: challenge.Token, ActionDigest: challenge.ActionDigest, ApprovalContext: challenge.ApprovalContext, BindingEpoch: challenge.BindingEpoch}
	for name, change := range map[string]func(*DecisionAccess){
		"channel":      func(v *DecisionAccess) { v.ChannelID = "other" },
		"conversation": func(v *DecisionAccess) { v.ConversationID = "other" },
		"principal":    func(v *DecisionAccess) { v.PrincipalID = "mallory" },
		"credential":   func(v *DecisionAccess) { v.Credential = "wrong" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := access
			change(&bad)
			if _, err := a.ReadPendingDecision(bad); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("poll = %v", err)
			}
			if _, err := a.SubmitAnswer(AnswerRequest{DecisionAccess: bad, IdempotencyKey: "answer-1", Answer: "deny"}); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("answer = %v", err)
			}
		})
	}
	for name, change := range map[string]func(*DecisionAccess){
		"exchange":         func(v *DecisionAccess) { v.ExchangeID = "other" },
		"action":           func(v *DecisionAccess) { v.ActionDigest = "changed" },
		"approval context": func(v *DecisionAccess) { v.ApprovalContext = "changed" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := access
			change(&bad)
			if _, err := a.ReadPendingDecision(bad); !errors.Is(err, ErrDecisionMismatch) {
				t.Fatalf("poll = %v", err)
			}
			if _, err := a.SubmitAnswer(AnswerRequest{DecisionAccess: bad, IdempotencyKey: "answer-1", Answer: "deny"}); !errors.Is(err, ErrDecisionMismatch) {
				t.Fatalf("answer = %v", err)
			}
		})
	}
	bad := access
	bad.Token = "wrong"
	if _, err := a.ReadPendingDecision(bad); !errors.Is(err, ErrDecisionUnavailable) {
		t.Fatalf("wrong token poll = %v", err)
	}
	if _, err := a.SubmitAnswer(AnswerRequest{DecisionAccess: bad, IdempotencyKey: "answer-1", Answer: "deny"}); !errors.Is(err, ErrDecisionUnavailable) {
		t.Fatalf("wrong token = %v", err)
	}
	if len(a.answers) != 0 {
		t.Fatal("rejected attempts changed answer state")
	}
	if got, err := a.ReadPendingDecision(access); err != nil || got != challenge {
		t.Fatalf("poll = %+v, %v", got, err)
	}
	answer := AnswerRequest{DecisionAccess: access, IdempotencyKey: "answer-1", Answer: "deny"}
	first, err := a.SubmitAnswer(answer)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := a.SubmitAnswer(answer); err != nil || again != first {
		t.Fatalf("same retry = %+v, %v", again, err)
	}
	bad = access
	bad.Credential = "wrong"
	if _, err := a.SubmitAnswer(AnswerRequest{DecisionAccess: bad, IdempotencyKey: "answer-1", Answer: "deny"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("accepted retry with wrong credential = %v", err)
	}
	answer.Answer = "approve"
	if _, err := a.SubmitAnswer(answer); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("changed retry = %v", err)
	}
	answer.IdempotencyKey = "answer-2"
	if _, err := a.SubmitAnswer(answer); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("second answer = %v", err)
	}
	if len(a.answers) != 1 {
		t.Fatalf("answer state = %d; want 1", len(a.answers))
	}
	if got := a.answers["answer-1"].answer; got != "deny" {
		t.Fatalf("recorded answer = %q; want deny", got)
	}
	reissued, err := a.IssueDecision(receipt.ExchangeID, "digest-2", "context-2", now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if reissued.Token == challenge.Token {
		t.Fatal("reissued token was reused")
	}
	answer.IdempotencyKey, answer.Answer = "answer-1", "deny"
	if again, err := a.SubmitAnswer(answer); err != nil || again != first {
		t.Fatalf("accepted retry after same-epoch reissue = %+v, %v", again, err)
	}
	answer.IdempotencyKey = "answer-new"
	if _, err := a.SubmitAnswer(answer); !errors.Is(err, ErrDecisionSuperseded) {
		t.Fatalf("new key on superseded token = %v", err)
	}
	otherReceipt, err := a.Receive(requestFor(b, "request-2", "second exchange"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.IssueDecision(otherReceipt.ExchangeID, "other-digest", "other-context", now.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	otherAccess := access
	otherAccess.ExchangeID, otherAccess.Token, otherAccess.ActionDigest, otherAccess.ApprovalContext = other.ExchangeID, other.Token, other.ActionDigest, other.ApprovalContext
	if _, err := a.SubmitAnswer(AnswerRequest{DecisionAccess: otherAccess, IdempotencyKey: "answer-1", Answer: "deny"}); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key on another valid exchange = %v", err)
	}
	if len(a.answers) != 1 {
		t.Fatalf("cross-exchange retry changed answer state: %d", len(a.answers))
	}
	now = now.Add(time.Hour)
	answer.IdempotencyKey = "answer-1"
	if again, err := a.SubmitAnswer(answer); err != nil || again != first {
		t.Fatalf("accepted retry after expiry = %+v, %v", again, err)
	}
	newBinding := b
	newBinding.Credential = "new-secret"
	if err := a.Rebind(newBinding); err != nil {
		t.Fatal(err)
	}
	answer.Credential = newBinding.Credential
	if _, err := a.SubmitAnswer(answer); !errors.Is(err, ErrDecisionSuperseded) {
		t.Fatalf("accepted retry after rebind = %v", err)
	}
}

func TestLocalTextDecisionExpiresAndSupersedes(t *testing.T) {
	a, b, _ := localTextFixture(t)
	now := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }
	receipt, err := a.Receive(requestFor(b, "request-1", "hello"))
	if err != nil {
		t.Fatal(err)
	}
	first, err := a.IssueDecision(receipt.ExchangeID, "digest-1", "context-1", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	access := DecisionAccess{ChannelID: b.ChannelID, ConversationID: b.ConversationID, PrincipalID: b.PrincipalID, Credential: b.Credential, ExchangeID: first.ExchangeID, Token: first.Token, ActionDigest: first.ActionDigest, ApprovalContext: first.ApprovalContext, BindingEpoch: first.BindingEpoch}
	now = now.Add(time.Minute)
	if _, err := a.SubmitAnswer(AnswerRequest{DecisionAccess: access, IdempotencyKey: "answer-1", Answer: "approve"}); !errors.Is(err, ErrDecisionExpired) {
		t.Fatalf("expired answer = %v", err)
	}
	if len(a.answers) != 0 {
		t.Fatal("expired rejection changed answer state")
	}
	second, err := a.IssueDecision(receipt.ExchangeID, "digest-2", "context-2", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if second.Token == first.Token {
		t.Fatal("reissued token was reused")
	}
	if _, err := a.ReadPendingDecision(access); !errors.Is(err, ErrDecisionSuperseded) {
		t.Fatalf("superseded poll = %v", err)
	}
	if _, err := a.SubmitAnswer(AnswerRequest{DecisionAccess: access, IdempotencyKey: "answer-1", Answer: "approve"}); !errors.Is(err, ErrDecisionSuperseded) {
		t.Fatalf("never-accepted superseded answer = %v", err)
	}
	if len(a.answers) != 0 {
		t.Fatal("superseded rejection changed answer state")
	}
	access.Token, access.ActionDigest, access.ApprovalContext = second.Token, second.ActionDigest, second.ApprovalContext
	if _, err := a.ReadPendingDecision(access); err != nil {
		t.Fatal(err)
	}
	newBinding := b
	newBinding.Credential = "new-secret"
	if err := a.Rebind(newBinding); err != nil {
		t.Fatal(err)
	}
	access.Credential = newBinding.Credential
	if _, err := a.SubmitAnswer(AnswerRequest{DecisionAccess: access, IdempotencyKey: "answer-2", Answer: "approve"}); !errors.Is(err, ErrDecisionSuperseded) {
		t.Fatalf("stale epoch = %v", err)
	}
	if len(a.answers) != 0 {
		t.Fatal("stale attempts changed answer state")
	}
}
