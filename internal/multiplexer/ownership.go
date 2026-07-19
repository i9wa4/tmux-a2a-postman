package multiplexer

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
)

const (
	SessionOwnerOptionPrefix = "@a2a_session_on_"
	PaneContextOption        = "@a2a_context_id"
)

type OwnershipBackend interface {
	Kind() BackendKind
	SessionOwnerMarker(ctx context.Context, sessionName string) (string, error)
	SetSessionOwnerMarker(ctx context.Context, contextID, sessionName string, pid int) error
	ClearSessionOwnerMarker(ctx context.Context, sessionName string) error
	PaneOwnerMarker(ctx context.Context, pane ResourceID) (string, error)
	SetPaneOwnerMarker(ctx context.Context, pane ResourceID, contextID string) error
	ClearPaneOwnerMarker(ctx context.Context, pane ResourceID) error
}

var registeredOwnershipBackends sync.Map

func BackendKindFromString(backend string) BackendKind {
	switch BackendKind(strings.TrimSpace(backend)) {
	case BackendKindHerdr:
		return BackendKindHerdr
	default:
		return BackendKindTmux
	}
}

func PaneIDForBackend(backend BackendKind, paneID string) ResourceID {
	switch backend {
	case BackendKindHerdr:
		return HerdrPaneID(paneID)
	default:
		return TmuxPaneID(paneID)
	}
}

func RegisterOwnershipBackend(backend OwnershipBackend) func() {
	if backend == nil {
		return func() {}
	}
	key := backend.Kind()
	registeredOwnershipBackends.Store(key, backend)
	return func() {
		registeredOwnershipBackends.Delete(key)
	}
}

func OwnershipBackendForKind(backend BackendKind) (OwnershipBackend, error) {
	switch backend {
	case BackendKindTmux, "":
		return TmuxBackend{}, nil
	case BackendKindHerdr:
		if registered, ok := registeredOwnershipBackends.Load(BackendKindHerdr); ok {
			if ownershipBackend, ok := registered.(OwnershipBackend); ok {
				return ownershipBackend, nil
			}
		}
		return nil, fmt.Errorf("herdr ownership backend not registered")
	default:
		return nil, fmt.Errorf("unsupported ownership backend %q", backend)
	}
}

func (b TmuxBackend) SessionOwnerMarker(_ context.Context, sessionName string) (string, error) {
	out, err := b.Runner.Output("show-options", "-gqv", SessionOwnerOptionPrefix+sessionName)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (b TmuxBackend) SetSessionOwnerMarker(_ context.Context, contextID, sessionName string, pid int) error {
	if contextID == "" {
		return fmt.Errorf("context ID is empty")
	}
	if pid <= 0 {
		pid = os.Getpid()
	}
	value := contextID + ":" + strconv.Itoa(pid)
	return b.Runner.Run("set-option", "-g", SessionOwnerOptionPrefix+sessionName, value)
}

func (b TmuxBackend) ClearSessionOwnerMarker(_ context.Context, sessionName string) error {
	return b.Runner.Run("set-option", "-gu", SessionOwnerOptionPrefix+sessionName)
}

func (b TmuxBackend) PaneOwnerMarker(_ context.Context, pane ResourceID) (string, error) {
	out, err := b.Runner.Output("show-options", "-p", "-v", "-t", pane.Native, PaneContextOption)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (b TmuxBackend) SetPaneOwnerMarker(_ context.Context, pane ResourceID, contextID string) error {
	return b.Runner.Run("set-option", "-p", "-t", pane.Native, PaneContextOption, contextID)
}

func (b TmuxBackend) ClearPaneOwnerMarker(_ context.Context, pane ResourceID) error {
	return b.Runner.Run("set-option", "-p", "-u", "-t", pane.Native, PaneContextOption)
}
