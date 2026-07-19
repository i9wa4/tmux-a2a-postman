package herdrruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/multiplexer"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
)

type ClientFactory func(config.HerdrConfig) (multiplexer.HerdrReadClient, error)

type Runtime struct {
	cfg              config.HerdrConfig
	client           multiplexer.HerdrReadClient
	ownershipBackend *ownershipMux

	mu       sync.Mutex
	cleanups []func()
}

func New(cfg *config.Config, factory ClientFactory) (*Runtime, error) {
	if cfg == nil || !cfg.Herdr.Enabled {
		return nil, nil
	}
	if factory == nil {
		return nil, fmt.Errorf("%w: herdr client factory not configured", multiplexer.ErrHerdrBackendUnavailable)
	}
	client, err := factory(cfg.Herdr)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, multiplexer.ErrHerdrReadClientMissing
	}
	rt := &Runtime{
		cfg:              cfg.Herdr,
		client:           client,
		ownershipBackend: newOwnershipMux(cfg.Herdr.SessionName),
	}
	rt.cleanups = append(rt.cleanups, multiplexer.RegisterOwnershipBackend(rt.ownershipBackend))
	return rt, nil
}

func (rt *Runtime) Enabled() bool {
	return rt != nil
}

func (rt *Runtime) OwnsSession(sessionName string) bool {
	return rt != nil && strings.TrimSpace(sessionName) == strings.TrimSpace(rt.cfg.SessionName)
}

func (rt *Runtime) OwnershipBackend() multiplexer.OwnershipBackend {
	if rt == nil {
		return nil
	}
	return rt.ownershipBackend
}

func (rt *Runtime) SetSessionEnabledMarker(ctx context.Context, contextID, sessionName string, enabled bool) error {
	if rt == nil || !rt.OwnsSession(sessionName) {
		return config.SetSessionEnabledMarker(contextID, sessionName, enabled)
	}
	if enabled {
		return rt.ownershipBackend.SetSessionOwnerMarker(ctx, contextID, sessionName, 0)
	}
	return rt.ownershipBackend.ClearSessionOwnerMarker(ctx, sessionName)
}

func (rt *Runtime) SessionOwnerMarker(ctx context.Context, sessionName string) (string, error) {
	if rt == nil || !rt.OwnsSession(sessionName) {
		return "", nil
	}
	return rt.ownershipBackend.SessionOwnerMarker(ctx, sessionName)
}

func (rt *Runtime) Discover(ctx context.Context, baseDir, contextID string) (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
	if rt == nil {
		return nil, nil, nil
	}
	readConfig := rt.cfg.ReadConfig()
	readConfig.Policy.ReadScope = multiplexer.HerdrReadScopeDiscovery
	backend, err := multiplexer.NewHerdrBackend(readConfig, rt.client)
	if err != nil {
		return nil, nil, err
	}
	result, err := backend.Discover(ctx, rt.cfg.SessionName)
	if err != nil {
		return nil, nil, err
	}
	nodes := make(map[string]discovery.NodeInfo)
	var collisions []discovery.CollisionReport
	sessionDir := filepath.Join(baseDir, contextID, rt.cfg.SessionName)
	for _, collision := range result.Collisions {
		paneIDs := append([]string(nil), collision.PaneIDs...)
		sort.Strings(paneIDs)
		if len(paneIDs) < 2 {
			continue
		}
		for _, loser := range paneIDs[:len(paneIDs)-1] {
			collisions = append(collisions, discovery.CollisionReport{
				NodeKey:      collision.SessionName + ":" + collision.NodeName,
				WinnerPaneID: paneIDs[len(paneIDs)-1],
				LoserPaneID:  loser,
			})
		}
	}

	for _, group := range result.Layout.Groups {
		tabID := group.ID.Native
		for _, item := range group.Items {
			if item.LogicalName == "" || item.ID.Native == "" {
				continue
			}
			nodeKey := rt.cfg.SessionName + ":" + item.LogicalName
			paneBackend := rt.backendForPane(tabID, item.ID.Native)
			rt.registerPaneBackend(item.ID.Native, paneBackend)
			nodes[nodeKey] = discovery.NodeInfo{
				PaneID:      item.ID.Native,
				SessionName: rt.cfg.SessionName,
				SessionDir:  sessionDir,
				Backend:     string(multiplexer.BackendKindHerdr),
				Runtime:     item.CurrentCommand,
			}
		}
	}
	return nodes, collisions, nil
}

func (rt *Runtime) Close() {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	for i := len(rt.cleanups) - 1; i >= 0; i-- {
		rt.cleanups[i]()
	}
	rt.cleanups = nil
	rt.ownershipBackend.clear()
}

func (rt *Runtime) backendForPane(tabID, paneID string) multiplexer.HerdrBackend {
	readConfig := rt.cfg.ReadConfig()
	readConfig.Runtime.TabID = tabID
	readConfig.Runtime.PaneID = paneID
	readConfig.Policy.ReadScope = multiplexer.HerdrReadScopePane
	return multiplexer.HerdrBackend{
		Config:         readConfig,
		Client:         rt.client,
		InputSanitizer: notification.PrepareInteractivePaneMessage,
	}
}

func (rt *Runtime) registerPaneBackend(paneID string, backend multiplexer.HerdrBackend) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.ownershipBackend.setPaneBackend(paneID, backend)
	rt.cleanups = append(rt.cleanups, controlplane.RegisterHerdrHandAdapter(paneID, controlplane.HerdrHandAdapter{
		HerdrInteractiveDeliveryAdapter: controlplane.HerdrInteractiveDeliveryAdapter{
			Backend:        backend,
			InputSanitizer: notification.PrepareInteractivePaneMessage,
		},
	}))
}

type ownershipMux struct {
	sessionName string
	mu          sync.RWMutex
	byPane      map[string]multiplexer.HerdrBackend
}

func newOwnershipMux(sessionName string) *ownershipMux {
	return &ownershipMux{sessionName: sessionName, byPane: make(map[string]multiplexer.HerdrBackend)}
}

func (m *ownershipMux) Kind() multiplexer.BackendKind {
	return multiplexer.BackendKindHerdr
}

func (m *ownershipMux) setPaneBackend(paneID string, backend multiplexer.HerdrBackend) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byPane[paneID] = backend
}

func (m *ownershipMux) clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byPane = make(map[string]multiplexer.HerdrBackend)
}

func (m *ownershipMux) backendForSession(sessionName string) (multiplexer.HerdrBackend, error) {
	if strings.TrimSpace(sessionName) != strings.TrimSpace(m.sessionName) {
		return multiplexer.HerdrBackend{}, multiplexer.ErrHerdrSessionNameMismatch
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0, len(m.byPane))
	for paneID := range m.byPane {
		keys = append(keys, paneID)
	}
	sort.Strings(keys)
	for _, paneID := range keys {
		return m.byPane[paneID], nil
	}
	return multiplexer.HerdrBackend{}, fmt.Errorf("%w: no herdr pane backend registered for session %q", multiplexer.ErrHerdrReadClientMissing, sessionName)
}

func (m *ownershipMux) backendForPane(pane multiplexer.ResourceID) (multiplexer.HerdrBackend, error) {
	if pane.Backend != multiplexer.BackendKindHerdr {
		return multiplexer.HerdrBackend{}, fmt.Errorf("herdr ownership requires herdr pane resource: %#v", pane)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	backend, ok := m.byPane[pane.Native]
	if !ok {
		return multiplexer.HerdrBackend{}, fmt.Errorf("herdr pane backend not registered for %q", pane.Native)
	}
	return backend, nil
}

func (m *ownershipMux) SessionOwnerMarker(ctx context.Context, sessionName string) (string, error) {
	backend, err := m.backendForSession(sessionName)
	if err != nil {
		return "", err
	}
	return backend.SessionOwnerMarker(ctx, sessionName)
}

func (m *ownershipMux) SetSessionOwnerMarker(ctx context.Context, contextID, sessionName string, pid int) error {
	backend, err := m.backendForSession(sessionName)
	if err != nil {
		return err
	}
	return backend.SetSessionOwnerMarker(ctx, contextID, sessionName, pid)
}

func (m *ownershipMux) ClearSessionOwnerMarker(ctx context.Context, sessionName string) error {
	backend, err := m.backendForSession(sessionName)
	if err != nil {
		return err
	}
	return backend.ClearSessionOwnerMarker(ctx, sessionName)
}

func (m *ownershipMux) PaneOwnerMarker(ctx context.Context, pane multiplexer.ResourceID) (string, error) {
	backend, err := m.backendForPane(pane)
	if err != nil {
		return "", err
	}
	return backend.PaneOwnerMarker(ctx, pane)
}

func (m *ownershipMux) SetPaneOwnerMarker(ctx context.Context, pane multiplexer.ResourceID, contextID string) error {
	backend, err := m.backendForPane(pane)
	if err != nil {
		return err
	}
	return backend.SetPaneOwnerMarker(ctx, pane, contextID)
}

func (m *ownershipMux) ClearPaneOwnerMarker(ctx context.Context, pane multiplexer.ResourceID) error {
	backend, err := m.backendForPane(pane)
	if err != nil {
		return err
	}
	return backend.ClearPaneOwnerMarker(ctx, pane)
}
