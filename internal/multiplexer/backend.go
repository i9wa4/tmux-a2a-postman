package multiplexer

import (
	"context"
	"fmt"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/tmuxrunner"
)

type BackendKind string

const (
	BackendKindTmux BackendKind = "tmux"
)

type ResourceKind string

const (
	ResourceKindPane    ResourceKind = "pane"
	ResourceKindSession ResourceKind = "session"
	ResourceKindNode    ResourceKind = "node"
)

type ResourceID struct {
	Backend BackendKind
	Kind    ResourceKind
	Native  string
}

func TmuxPaneID(paneID string) ResourceID {
	return ResourceID{Backend: BackendKindTmux, Kind: ResourceKindPane, Native: paneID}
}

type CaptureOptions struct {
	TailLines int
	History   bool
}

type PaneBackend interface {
	Kind() BackendKind
	CapturePane(ctx context.Context, pane ResourceID, opts CaptureOptions) (string, error)
	PaneCurrentCommand(ctx context.Context, pane ResourceID) (string, error)
}

type TmuxBackend struct {
	Runner tmuxrunner.Command
}

func (TmuxBackend) Kind() BackendKind {
	return BackendKindTmux
}

func (b TmuxBackend) CapturePane(_ context.Context, pane ResourceID, opts CaptureOptions) (string, error) {
	args := []string{"capture-pane", "-p", "-t", pane.Native}
	switch {
	case opts.History:
		args = append(args, "-S", "-")
	case opts.TailLines > 0:
		args = append(args, "-S", fmt.Sprintf("-%d", opts.TailLines))
	}
	out, err := b.Runner.CombinedOutput(args...)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (b TmuxBackend) PaneCurrentCommand(_ context.Context, pane ResourceID) (string, error) {
	out, err := b.Runner.Output("display-message", "-t", pane.Native, "-p", "#{pane_current_command}")
	return strings.TrimSpace(string(out)), err
}
