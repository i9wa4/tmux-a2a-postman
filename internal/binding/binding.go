// Package binding defines the Binding and BindingRegistry types
// for associating tmux panes with postman nodes. Issue #300, #303.
package binding

import (
	"fmt"
	"log"
	"os"
	"regexp"

	"github.com/BurntSushi/toml"
)

// validNodeNameRe validates node_name and pane_node_name fields.
// Identical bound to internal/message.validNodeNameRe (A-1 / #299).
// Duplicated here to avoid an import cycle with internal/message.
var validNodeNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,63}$`)

// validIDRe validates channel_id and context_id fields.
// Same pattern as validNodeNameRe; separate var clarifies intent (ID vs node).
var validIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{0,63}$`)

// LoadOption is a functional option for Load.
type LoadOption func(*loadOptions)

type loadOptions struct {
	allowEmptySenders bool
}

// AllowEmptySenders changes the hard error on empty permitted_senders to a WARNING.
// Exit code remains zero.
func AllowEmptySenders() LoadOption {
	return func(o *loadOptions) { o.allowEmptySenders = true }
}

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

// tomlFile is the top-level TOML structure for bindings.toml.
// Uses [[binding]] array-of-tables.
type tomlFile struct {
	Binding []Binding `toml:"binding"`
}

// Load reads a binding registry from the TOML file at path.
// It checks file permissions, parses the TOML, validates every field,
// and enforces the seven-row state validity table and duplicate constraints.
// Load never acquires a .lock file.
func Load(path string, opts ...LoadOption) (*BindingRegistry, error) {
	o := &loadOptions{}
	for _, opt := range opts {
		opt(o)
	}

	// Milestone 1: permission check — 0600 or stricter required.
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	mode := info.Mode().Perm()
	if mode&0o044 != 0 {
		return nil, fmt.Errorf(
			"bindings.toml must not be group- or world-readable (got %04o): %s",
			mode, path,
		)
	}

	// Milestone 2: TOML parse.
	var raw tomlFile
	if _, err := toml.DecodeFile(path, &raw); err != nil {
		return nil, fmt.Errorf("parse bindings.toml: %w", err)
	}

	// Milestone 4: duplicate detection (Constraint 12).
	seenChannels := make(map[string]struct{}, len(raw.Binding))
	seenNodes := make(map[string]struct{}, len(raw.Binding))

	for i, b := range raw.Binding {
		// Duplicate channel_id.
		if _, dup := seenChannels[b.ChannelID]; dup {
			return nil, fmt.Errorf("binding[%d]: duplicate channel_id %q", i, b.ChannelID)
		}
		seenChannels[b.ChannelID] = struct{}{}

		// Duplicate node_name.
		if _, dup := seenNodes[b.NodeName]; dup {
			return nil, fmt.Errorf("binding[%d]: duplicate node_name %q", i, b.NodeName)
		}
		seenNodes[b.NodeName] = struct{}{}

		// Milestone 3 + Milestone 2: field validation + state table.
		if err := validateBinding(b, i, o); err != nil {
			return nil, err
		}
	}

	return &BindingRegistry{Bindings: raw.Binding}, nil
}

// validateBinding validates a single Binding entry.
// Enforces the seven-row state validity table and all field constraints.
func validateBinding(b Binding, idx int, o *loadOptions) error {
	tag := fmt.Sprintf("binding[%d] (node_name=%q)", idx, b.NodeName)

	// --- Milestone 3: field regex validation ---

	if !validIDRe.MatchString(b.ChannelID) {
		return fmt.Errorf("%s: invalid channel_id %q", tag, b.ChannelID)
	}
	if !validNodeNameRe.MatchString(b.NodeName) {
		return fmt.Errorf("%s: invalid node_name %q", tag, b.NodeName)
	}
	if !validIDRe.MatchString(b.ContextID) {
		return fmt.Errorf("%s: invalid context_id %q (path traversal prevention)", tag, b.ContextID)
	}
	if b.PaneNodeName != "" && !validNodeNameRe.MatchString(b.PaneNodeName) {
		return fmt.Errorf("%s: invalid pane_node_name %q", tag, b.PaneNodeName)
	}
	for _, sender := range b.PermittedSenders {
		if !validNodeNameRe.MatchString(sender) {
			return fmt.Errorf("%s: invalid permitted_senders entry %q", tag, sender)
		}
	}
	if len(b.PermittedSenders) == 0 {
		if o.allowEmptySenders {
			log.Printf("WARNING: %s has empty permitted_senders", tag)
		} else {
			return fmt.Errorf("%s: permitted_senders must not be empty", tag)
		}
	}

	// --- Milestone 2: seven-row state validity table ---
	if err := validateState(b, tag); err != nil {
		return err
	}

	return nil
}

// validateState enforces the seven-row state validity table.
//
// Valid combinations of (active, session_name, pane_title, pane_node_name):
//
//	Row 1: false, "",  "",  ""   — unassigned
//	Row 2: true,  set, "",  set  — assigned, match by pane_node_name
//	Row 3: true,  set, set, ""   — assigned, match by pane_title
//	Row 4: true,  set, set, set  — assigned, both matchers
//	Row 5: false, set, "",  set  — inactive, was node_name-matched
//	Row 6: false, set, set, ""   — inactive, was title-matched
//	Row 7: false, set, set, set  — inactive, was both-matched
func validateState(b Binding, tag string) error {
	active := b.Active
	hasSession := b.SessionName != ""
	hasTitle := b.PaneTitle != ""
	hasNode := b.PaneNodeName != ""

	switch {
	// Row 1: unassigned
	case !active && !hasSession && !hasTitle && !hasNode:
		return nil
	// Rows 2-4: active assigned
	case active && hasSession && !hasTitle && hasNode:
		return nil // row 2
	case active && hasSession && hasTitle && !hasNode:
		return nil // row 3
	case active && hasSession && hasTitle && hasNode:
		return nil // row 4
	// Rows 5-7: inactive with session
	case !active && hasSession && !hasTitle && hasNode:
		return nil // row 5
	case !active && hasSession && hasTitle && !hasNode:
		return nil // row 6
	case !active && hasSession && hasTitle && hasNode:
		return nil // row 7
	// All other combinations are invalid.
	default:
		return fmt.Errorf(
			"%s: invalid state combination (active=%v session_name=%q pane_title=%q pane_node_name=%q)",
			tag, active, b.SessionName, b.PaneTitle, b.PaneNodeName,
		)
	}
}
