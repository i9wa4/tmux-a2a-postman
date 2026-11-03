package config

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/i9wa4/tmux-a2a-postman/internal/helpdocs"
)

// Source pins selection, including the absent automatic file which selected
// the embedded default. An explicit file never falls back or merges defaults.
type Source struct {
	Path    string
	Builtin bool
}

func (s Source) Load(ctx context.Context) (WorkspaceTopology, error) {
	if !s.Builtin {
		return LoadWorkspaceTopology(ctx, s.Path)
	}
	if !filepath.IsAbs(s.Path) {
		return WorkspaceTopology{}, errors.New("default configuration path must be absolute")
	}
	if _, err := os.Lstat(s.Path); !errors.Is(err, os.ErrNotExist) {
		return WorkspaceTopology{}, errors.New("selected builtin configuration changed; select configuration again")
	}
	raw, err := fs.ReadFile(helpdocs.Assets(), "config/postman.md")
	if err != nil {
		return WorkspaceTopology{}, err
	}
	topology, err := parseWorkspaceTopology(ctx, string(raw))
	topology.builtin = true
	return topology, err
}

// PolicyIdentity describes current authored bytes; saved archives retain their
// original context bytes and digest across upgrades.
func (t WorkspaceTopology) PolicyIdentity() string {
	sum := sha256.Sum256([]byte(t.messageSource))
	identity := fmt.Sprintf("sha256:%x", sum)
	if t.builtin {
		identity += "; builtin bundle " + helpdocs.BundleID()
	}
	return identity
}

func (t WorkspaceTopology) BuiltinBundleID() string {
	if t.builtin {
		return helpdocs.BundleID()
	}
	return ""
}
