package testfixture

import (
	"errors"
	"strings"
	"testing"
)

const fakeMail = "Hello, worker! You've got mail: msg.md. Run `tmux-a2a-postman pop` to claim it and get the archived body path."

func TestFakeAgentWaitsForTerminalNotificationAndDeduplicates(t *testing.T) {
	var calls []string
	agent := FakeAgent{
		Node:     "worker",
		Terminal: strings.NewReader("unrelated terminal text\nHello, critic! You've got mail: other.md. Run `tmux-a2a-postman pop` to claim it and get the archived body path.\n" + fakeMail + "\n" + fakeMail + "\n"),
		Pop: func() (FakeMessage, error) {
			calls = append(calls, "pop")
			return FakeMessage{Filename: "msg.md", Body: "request"}, nil
		},
		Reply: func(message FakeMessage) error {
			if message.Body != "request" {
				t.Fatalf("reply body = %q", message.Body)
			}
			calls = append(calls, "reply")
			return nil
		},
	}
	if err := agent.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := strings.Join(calls, ","); got != "pop,reply" {
		t.Fatalf("calls = %q, want pop,reply", got)
	}
}

func TestFakeAgentDoesNotPopWithoutGenuineNotification(t *testing.T) {
	agent := FakeAgent{
		Node:     "worker",
		Terminal: strings.NewReader("mail exists in inbox\nRun tmux-a2a-postman pop.\nHello, worker! You've got mail: msg.md. Run tmux-a2a-postman pop.\n"),
		Pop: func() (FakeMessage, error) {
			t.Fatal("pop called without genuine terminal notification")
			return FakeMessage{}, nil
		},
		Reply: func(FakeMessage) error {
			t.Fatal("reply called without genuine terminal notification")
			return nil
		},
	}
	if err := agent.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestFakeAgentPropagatesPopAndReplyFailures(t *testing.T) {
	popFailure := errors.New("pop failed")
	replyFailure := errors.New("reply failed")
	for _, tc := range []struct {
		name      string
		pop       func() (FakeMessage, error)
		reply     func(FakeMessage) error
		wantError error
	}{
		{
			name: "pop",
			pop: func() (FakeMessage, error) {
				return FakeMessage{}, popFailure
			},
			reply: func(FakeMessage) error {
				t.Fatal("reply called after pop failed")
				return nil
			},
			wantError: popFailure,
		},
		{
			name: "reply",
			pop: func() (FakeMessage, error) {
				return FakeMessage{Filename: "msg.md"}, nil
			},
			reply: func(FakeMessage) error {
				return replyFailure
			},
			wantError: replyFailure,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := FakeAgent{
				Node:     "worker",
				Terminal: strings.NewReader(fakeMail + "\n"),
				Pop:      tc.pop,
				Reply:    tc.reply,
			}
			if err := agent.Run(); !errors.Is(err, tc.wantError) {
				t.Fatalf("Run error = %v, want %v", err, tc.wantError)
			}
		})
	}
}

func TestFakeAgentRejectsMismatchedPop(t *testing.T) {
	agent := FakeAgent{
		Node:     "worker",
		Terminal: strings.NewReader(fakeMail + "\n"),
		Pop: func() (FakeMessage, error) {
			return FakeMessage{Filename: "other.md"}, nil
		},
		Reply: func(FakeMessage) error {
			t.Fatal("reply called for unexpected message")
			return nil
		},
	}
	if err := agent.Run(); err == nil || !strings.Contains(err.Error(), "returned \"other.md\"") {
		t.Fatalf("Run error = %v, want filename mismatch", err)
	}
}
