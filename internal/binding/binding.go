// Package binding defines the Binding and BindingRegistry types
// for associating tmux panes with postman nodes. Issue #300.
package binding

import "fmt"

// Binding associates a tmux pane with a named node in the postman system.
type Binding struct {
	ChannelID        string   `toml:"channel_id"`
	NodeName         string   `toml:"node_name"`
	ContextID        string   `toml:"context_id"`
	SessionName      string   `toml:"session_name"`
	PaneTitle        string   `toml:"pane_title"`
	PaneNodeName     string   `toml:"pane_node_name"`
	Active           bool     `toml:"active"`
	PermittedSenders []string `toml:"permitted_senders"`
}

// BindingRegistry holds all Binding records loaded from a TOML file.
type BindingRegistry struct {
	Bindings []Binding
}

// Load reads a binding registry from the TOML file at path.
// Not yet implemented.
func Load(path string) (*BindingRegistry, error) {
	return nil, fmt.Errorf("not implemented")
}
