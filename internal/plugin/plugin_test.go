package plugin

import "testing"

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
