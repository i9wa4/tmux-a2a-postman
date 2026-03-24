package plugin

import (
	"strings"
	"testing"
)

func TestPluginEnvelope_Fields(t *testing.T) {
	env := PluginEnvelope{ID: "msg-01", Body: "hello"}
	if env.ID != "msg-01" {
		t.Errorf("ID: got %q, want %q", env.ID, "msg-01")
	}
	if env.Body != "hello" {
		t.Errorf("Body: got %q, want %q", env.Body, "hello")
	}
}

func TestNoOpPlugin_ImplementsPlugin(t *testing.T) {
	var _ Plugin = NoOpPlugin{}
}

func TestNoOpPlugin_Poll(t *testing.T) {
	p := NoOpPlugin{}
	envs, err := p.Poll()
	if err != nil {
		t.Fatalf("Poll returned error: %v", err)
	}
	if len(envs) != 0 {
		t.Errorf("Poll returned %d envelopes, want 0", len(envs))
	}
}

func TestNoOpPlugin_Ack(t *testing.T) {
	p := NoOpPlugin{}
	if err := p.Ack("any-id"); err != nil {
		t.Fatalf("Ack returned error: %v", err)
	}
}

func TestNoOpPlugin_Send(t *testing.T) {
	p := NoOpPlugin{}
	env := PluginEnvelope{ID: "x", Body: "payload"}
	if err := p.Send(env); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}
}

func TestValidateSendBody(t *testing.T) {
	t.Run("exactly 280 chars is valid", func(t *testing.T) {
		body := strings.Repeat("a", 280)
		if err := ValidateSendBody(body); err != nil {
			t.Errorf("expected valid, got error: %v", err)
		}
	})

	t.Run("281 chars is invalid", func(t *testing.T) {
		body := strings.Repeat("a", 281)
		if err := ValidateSendBody(body); err == nil {
			t.Error("expected error for 281-char body, got nil")
		}
	})

	t.Run("newline is invalid", func(t *testing.T) {
		if err := ValidateSendBody("hello\nworld"); err == nil {
			t.Error("expected error for body with newline, got nil")
		}
	})

	t.Run("special char outside allowed set is invalid", func(t *testing.T) {
		if err := ValidateSendBody("hello@world"); err == nil {
			t.Error("expected error for body with '@', got nil")
		}
	})

	t.Run("empty string is invalid (+ quantifier requires at least one char)", func(t *testing.T) {
		if err := ValidateSendBody(""); err == nil {
			t.Error("expected error for empty body, got nil")
		}
	})

	t.Run("valid body with allowed characters", func(t *testing.T) {
		body := "Decided X - vault/decisions/uma-pending/2026-03-24-example.md"
		if err := ValidateSendBody(body); err != nil {
			t.Errorf("expected valid, got error: %v", err)
		}
	})
}
