// Package plugin defines the Plugin interface for external-channel sidecars.
// Phase 1 (Issue #308): supervisory observation only — no autonomous action.
// The sidecar passively polls the phony inbox and records; Send/Ack are stubs.
//
// TODO(Phase 2): implement runtime e2e verification and real sidecar dispatch.
package plugin

// PluginEnvelope is the cross-boundary message type passed between the
// postman core and external-channel sidecar plugins.
// NOTE: intentionally NOT named Message to avoid collision with
// internal/message.Message — see Issue #308 Decision Log.
type PluginEnvelope struct {
	// ID is the message identifier, used for idempotent Ack calls.
	ID string
	// Body is the raw message payload.
	Body string
}

// Plugin is the interface that all external-channel sidecar plugins must
// implement. Phase 1 requires only the interface stub; the implementation
// is a no-op (supervisory observation only).
type Plugin interface {
	// Poll returns any pending envelopes from the external channel.
	Poll() ([]PluginEnvelope, error)
	// Ack acknowledges delivery of the envelope with the given ID.
	Ack(id string) error
	// Send delivers an envelope to the external channel.
	Send(env PluginEnvelope) error
}

// NoOpPlugin is the Phase 1 stub implementation of Plugin.
// It performs no I/O; all methods succeed silently.
// Replace with a real implementation in Phase 2.
type NoOpPlugin struct{}

// Poll returns an empty slice and no error.
func (NoOpPlugin) Poll() ([]PluginEnvelope, error) { return nil, nil }

// Ack accepts any id and succeeds silently.
func (NoOpPlugin) Ack(_ string) error { return nil }

// Send accepts any envelope and succeeds silently.
func (NoOpPlugin) Send(_ PluginEnvelope) error { return nil }
