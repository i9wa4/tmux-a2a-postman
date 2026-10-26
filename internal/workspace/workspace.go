// Package workspace defines stable file-first workspace identity and ownership.
// These helpers validate supplied identities. A runtime adapter must verify
// native IDs and persist decisions under a lock before mailbox mutations.
package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
)

var (
	ErrOwned   = errors.New("workspace has another live owner")
	ErrUnknown = errors.New("workspace owner liveness is unknown")
)

// CanonicalRoot resolves the trusted initial session root without consulting
// the current working directory. Only roots within canonical HOME are allowed.
func CanonicalRoot(root, home string) (string, error) {
	if !filepath.IsAbs(root) || !filepath.IsAbs(home) {
		return "", errors.New("workspace root and home must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace root is not a directory")
	}
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return "", fmt.Errorf("resolve home: %w", err)
	}
	rel, err := filepath.Rel(resolvedHome, resolved)
	if err == nil && rel == "." {
		return "~", nil
	}
	if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "~/" + filepath.ToSlash(rel), nil
	}
	return "", errors.New("workspace root is outside canonical home")
}

// Name flattens canonical HOME-relative components without collisions.
// Percent and dash are escaped, leaving dash exclusively as the separator.
// Uppercase bytes are escaped to remain distinct on case-insensitive volumes.
// Reserved names escape their first byte; literal percent is already escaped.
func Name(root string) (string, error) {
	tail := ""
	switch {
	case root == "~":
		return "%00", nil
	case strings.HasPrefix(root, "~/"):
		tail = root[2:]
	default:
		return "", errors.New("root is not canonical")
	}
	parts := strings.Split(tail, "/")
	for i, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsRune(part, 0) {
			return "", errors.New("root contains a noncanonical component")
		}
		var encoded strings.Builder
		for _, b := range []byte(part) {
			if b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '_' || b == '.' {
				encoded.WriteByte(b)
			} else {
				fmt.Fprintf(&encoded, "%%%02X", b)
			}
		}
		parts[i] = encoded.String()
	}
	name := strings.Join(parts, "-")
	switch name {
	case "registry":
		name = "%72egistry"
	case "locks":
		name = "%6Cocks"
	}
	return name, nil
}

// Owner holds supplied server namespace/incarnation and native session/pane.
// Structural validity does not prove that this owner exists in tmux.
type Owner struct {
	Server      string `json:"server"`
	Incarnation string `json:"incarnation"`
	Session     string `json:"session"`
	Pane        string `json:"pane"`
}

func (o Owner) valid() bool {
	return strings.TrimSpace(o.Server) != "" &&
		strings.TrimSpace(o.Incarnation) != "" &&
		nativeID(o.Session, '$') &&
		nativeID(o.Pane, '%')
}

func nativeID(id string, prefix byte) bool {
	if len(id) < 2 || id[0] != prefix {
		return false
	}
	for _, c := range id[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func sameRuntime(a, b Owner) bool {
	return a.Server == b.Server &&
		a.Incarnation == b.Incarnation &&
		a.Session == b.Session
}

// Binding associates one configured role with a supplied native pane.
type Binding struct {
	Role    string `json:"role"`
	Owner   Owner  `json:"owner"`
	PanePID string `json:"pane_pid,omitempty"`
}

// ValidateBindings rejects missing identities, ambiguous roles, aliased panes,
// and mixed runtimes. It does not query or mutate any runtime.
func ValidateBindings(bindings []Binding) error {
	if len(bindings) == 0 {
		return ErrUnknown
	}
	roles, panes := map[string]bool{}, map[string]bool{}
	for _, b := range bindings {
		if !binding.ValidateNodeName(b.Role) || !b.Owner.valid() {
			return ErrUnknown
		}
		if !sameRuntime(bindings[0].Owner, b.Owner) {
			return ErrUnknown
		}
		if roles[b.Role] || panes[b.Owner.Pane] {
			return errors.New("ambiguous role or pane binding")
		}
		roles[b.Role], panes[b.Owner.Pane] = true, true
	}
	return nil
}

type Liveness uint8

const (
	Unknown Liveness = iota
	Alive
	Dead
)

// ResolveOwner requires runtime-verified inputs from its caller. It rejects
// conflicting live owners and unknown/failed probes. A different role pane in
// the same verified runtime is allowed. No record or mailbox is modified.
func ResolveOwner(previous *Owner, candidate Owner, probe func(Owner) (Liveness, error)) (Owner, bool, error) {
	if !candidate.valid() {
		return Owner{}, false, ErrUnknown
	}
	if previous == nil {
		return candidate, true, nil
	}
	if !previous.valid() {
		return Owner{}, false, ErrUnknown
	}
	if *previous == candidate {
		return candidate, false, nil
	}
	if sameRuntime(*previous, candidate) {
		return candidate, true, nil
	}
	if probe == nil {
		return Owner{}, false, ErrUnknown
	}
	state, err := probe(*previous)
	if err != nil {
		return Owner{}, false, fmt.Errorf("%w: %v", ErrUnknown, err)
	}
	switch state {
	case Alive:
		return Owner{}, false, ErrOwned
	case Dead:
		return candidate, true, nil
	default:
		return Owner{}, false, ErrUnknown
	}
}
