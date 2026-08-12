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

var (
	registeredOwnershipBackends   sync.Map
	registeredOwnershipBackendsMu sync.Mutex
)

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
	if key == BackendKindHerdr {
		registeredOwnershipBackendsMu.Lock()
		registeredOwnershipBackends.Store(key, append(registeredHerdrOwnershipBackends(), backend))
		registeredOwnershipBackendsMu.Unlock()
	} else {
		registeredOwnershipBackends.Store(key, backend)
	}
	return func() {
		if key != BackendKindHerdr {
			registeredOwnershipBackends.Delete(key)
			return
		}
		registeredOwnershipBackendsMu.Lock()
		defer registeredOwnershipBackendsMu.Unlock()
		registered := registeredHerdrOwnershipBackends()
		next := registered[:0]
		for _, registeredBackend := range registered {
			if registeredBackend != backend {
				next = append(next, registeredBackend)
			}
		}
		if len(next) == 0 {
			registeredOwnershipBackends.Delete(key)
			return
		}
		registeredOwnershipBackends.Store(key, append([]OwnershipBackend(nil), next...))
	}
}

func OwnershipBackendForKind(backend BackendKind) (OwnershipBackend, error) {
	switch backend {
	case BackendKindTmux, "":
		return TmuxBackend{}, nil
	case BackendKindHerdr:
		registeredOwnershipBackendsMu.Lock()
		backends := registeredHerdrOwnershipBackends()
		registeredOwnershipBackendsMu.Unlock()
		switch len(backends) {
		case 0:
			return nil, fmt.Errorf("herdr ownership backend not registered")
		case 1:
			return backends[0], nil
		default:
			return herdrOwnershipBackends(backends), nil
		}
	default:
		return nil, fmt.Errorf("unsupported ownership backend %q", backend)
	}
}

func registeredHerdrOwnershipBackends() []OwnershipBackend {
	registered, ok := registeredOwnershipBackends.Load(BackendKindHerdr)
	if !ok {
		return nil
	}
	switch backends := registered.(type) {
	case []OwnershipBackend:
		return append([]OwnershipBackend(nil), backends...)
	case OwnershipBackend:
		return []OwnershipBackend{backends}
	default:
		return nil
	}
}

type herdrOwnershipBackends []OwnershipBackend

func (h herdrOwnershipBackends) Kind() BackendKind {
	return BackendKindHerdr
}

func (h herdrOwnershipBackends) SessionOwnerMarker(ctx context.Context, sessionName string) (string, error) {
	var lastErr error
	foundEmpty := false
	for _, backend := range h {
		value, err := backend.SessionOwnerMarker(ctx, sessionName)
		if err == nil {
			if value != "" {
				return value, nil
			}
			foundEmpty = true
			continue
		}
		lastErr = err
	}
	if foundEmpty {
		return "", nil
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("herdr ownership backend not registered")
}

func (h herdrOwnershipBackends) SetSessionOwnerMarker(ctx context.Context, contextID, sessionName string, pid int) error {
	backends, err := h.sessionMutationBackends(ctx, sessionName)
	if err != nil {
		return err
	}
	var lastErr error
	for _, backend := range backends {
		if err := backend.SetSessionOwnerMarker(ctx, contextID, sessionName, pid); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("herdr ownership backend not registered")
}

func (h herdrOwnershipBackends) ClearSessionOwnerMarker(ctx context.Context, sessionName string) error {
	backends, err := h.sessionMutationBackends(ctx, sessionName)
	if err != nil {
		return err
	}
	var lastErr error
	for _, backend := range backends {
		if err := backend.ClearSessionOwnerMarker(ctx, sessionName); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("herdr ownership backend not registered")
}

func (h herdrOwnershipBackends) sessionMutationBackends(ctx context.Context, sessionName string) ([]OwnershipBackend, error) {
	var (
		nonEmpty []OwnershipBackend
		empty    []OwnershipBackend
		lastErr  error
	)
	for _, backend := range h {
		value, err := backend.SessionOwnerMarker(ctx, sessionName)
		if err != nil {
			lastErr = err
			continue
		}
		if value != "" {
			nonEmpty = append(nonEmpty, backend)
			continue
		}
		empty = append(empty, backend)
	}
	switch {
	case len(nonEmpty) > 0:
		return nonEmpty, nil
	case len(empty) > 0:
		return empty, nil
	case lastErr != nil:
		return nil, lastErr
	default:
		return nil, fmt.Errorf("herdr ownership backend not registered")
	}
}

func (h herdrOwnershipBackends) PaneOwnerMarker(ctx context.Context, pane ResourceID) (string, error) {
	var lastErr error
	for _, backend := range h {
		value, err := backend.PaneOwnerMarker(ctx, pane)
		if err == nil {
			return value, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("herdr ownership backend not registered")
}

func (h herdrOwnershipBackends) SetPaneOwnerMarker(ctx context.Context, pane ResourceID, contextID string) error {
	var lastErr error
	for _, backend := range h {
		if err := backend.SetPaneOwnerMarker(ctx, pane, contextID); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("herdr ownership backend not registered")
}

func (h herdrOwnershipBackends) ClearPaneOwnerMarker(ctx context.Context, pane ResourceID) error {
	var lastErr error
	for _, backend := range h {
		if err := backend.ClearPaneOwnerMarker(ctx, pane); err == nil {
			return nil
		} else {
			lastErr = err
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("herdr ownership backend not registered")
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
