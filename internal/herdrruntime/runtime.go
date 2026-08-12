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

	mu             sync.Mutex
	closed         bool
	generation     uint64
	cleanups       []func()
	adapterCleanup func()
}

type FinalPublication struct {
	ownershipBackend *ownershipMux
	runtime          *Runtime
}

func (p *FinalPublication) Kind() multiplexer.BackendKind {
	return multiplexer.BackendKindHerdr
}

func (p *FinalPublication) SessionOwnerMarker(ctx context.Context, sessionName string) (string, error) {
	if p == nil || p.ownershipBackend == nil {
		return "", fmt.Errorf("herdr final publication missing")
	}
	return p.ownershipBackend.SessionOwnerMarker(ctx, sessionName)
}

func (p *FinalPublication) SetSessionEnabledMarker(ctx context.Context, contextID, sessionName string, enabled bool) error {
	if p == nil || p.ownershipBackend == nil || p.runtime == nil || !p.runtime.OwnsSession(sessionName) {
		return config.SetSessionEnabledMarker(contextID, sessionName, enabled)
	}
	if enabled {
		return p.ownershipBackend.SetSessionOwnerMarker(ctx, contextID, sessionName, 0)
	}
	return p.ownershipBackend.ClearSessionOwnerMarker(ctx, sessionName)
}

func (p *FinalPublication) SetSessionOwnerMarker(ctx context.Context, contextID, sessionName string, pid int) error {
	if p == nil || p.ownershipBackend == nil {
		return fmt.Errorf("herdr final publication missing")
	}
	return p.ownershipBackend.SetSessionOwnerMarker(ctx, contextID, sessionName, pid)
}

func (p *FinalPublication) ClearSessionOwnerMarker(ctx context.Context, sessionName string) error {
	if p == nil || p.ownershipBackend == nil {
		return fmt.Errorf("herdr final publication missing")
	}
	return p.ownershipBackend.ClearSessionOwnerMarker(ctx, sessionName)
}

func (p *FinalPublication) PaneOwnerMarker(ctx context.Context, pane multiplexer.ResourceID) (string, error) {
	if p == nil || p.ownershipBackend == nil {
		return "", fmt.Errorf("herdr final publication missing")
	}
	return p.ownershipBackend.PaneOwnerMarker(ctx, pane)
}

func (p *FinalPublication) SetPaneOwnerMarker(ctx context.Context, pane multiplexer.ResourceID, contextID string) error {
	if p == nil || p.ownershipBackend == nil || pane.Backend != multiplexer.BackendKindHerdr {
		backend, err := multiplexer.OwnershipBackendForKind(pane.Backend)
		if err != nil {
			return err
		}
		return backend.SetPaneOwnerMarker(ctx, pane, contextID)
	}
	return p.ownershipBackend.SetPaneOwnerMarker(ctx, pane, contextID)
}

func (p *FinalPublication) ClearPaneOwnerMarker(ctx context.Context, pane multiplexer.ResourceID) error {
	if p == nil || p.ownershipBackend == nil {
		return fmt.Errorf("herdr final publication missing")
	}
	return p.ownershipBackend.ClearPaneOwnerMarker(ctx, pane)
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
	nodes, collisions, generation, err := rt.DiscoverForReconcile(ctx, baseDir, contextID)
	if err == nil && !rt.discoverStillCurrent(generation) {
		return nil, nil, fmt.Errorf("stale Herdr discovery token")
	}
	return nodes, collisions, err
}

func (rt *Runtime) DiscoverForReconcile(ctx context.Context, baseDir, contextID string) (map[string]discovery.NodeInfo, []discovery.CollisionReport, uint64, error) {
	if rt == nil {
		return nil, nil, 0, nil
	}
	generation, closed := rt.beginDiscover()
	if closed {
		return nil, nil, generation, fmt.Errorf("herdr runtime closed")
	}
	readConfig := rt.cfg.ReadConfig()
	readConfig.Policy.ReadScope = multiplexer.HerdrReadScopeDiscovery
	backend, err := multiplexer.NewHerdrBackend(readConfig, rt.client)
	if err != nil {
		rt.ClearPaneRoutesForToken(generation)
		return nil, nil, generation, err
	}
	result, err := backend.Discover(ctx, rt.cfg.SessionName)
	if err != nil {
		rt.ClearPaneRoutesForToken(generation)
		return nil, nil, generation, err
	}
	if !rt.discoverStillOpen() {
		return nil, nil, generation, fmt.Errorf("herdr runtime closed")
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
	collidedNodeKeys := make(map[string]bool, len(result.Collisions))
	for _, collision := range result.Collisions {
		if strings.TrimSpace(collision.SessionName) == "" || strings.TrimSpace(collision.NodeName) == "" {
			continue
		}
		collidedNodeKeys[collision.SessionName+":"+collision.NodeName] = true
	}

	for _, group := range result.Layout.Groups {
		tabID := group.ID.Native
		for _, item := range group.Items {
			if item.LogicalName == "" || item.ID.Native == "" {
				continue
			}
			nodeKey := rt.cfg.SessionName + ":" + item.LogicalName
			if collidedNodeKeys[nodeKey] {
				continue
			}
			nodes[nodeKey] = discovery.NodeInfo{
				PaneID:           item.ID.Native,
				SessionName:      rt.cfg.SessionName,
				SessionDir:       sessionDir,
				Backend:          string(multiplexer.BackendKindHerdr),
				Runtime:          item.CurrentCommand,
				HerdrSocketPath:  rt.cfg.SocketPath,
				HerdrWorkspaceID: rt.cfg.WorkspaceID,
				HerdrTabID:       tabID,
			}
		}
	}
	return nodes, collisions, generation, nil
}

func (rt *Runtime) beginDiscover() (uint64, bool) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		return rt.generation, true
	}
	rt.generation++
	return rt.generation, rt.closed
}

func (rt *Runtime) discoverStillCurrent(generation uint64) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return !rt.closed && rt.generation == generation
}

func (rt *Runtime) discoverStillOpen() bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return !rt.closed
}

func (rt *Runtime) ReconcileFinalNodes(nodes map[string]discovery.NodeInfo) bool {
	return rt.ReconcileFinalNodesForToken(0, nodes)
}

func (rt *Runtime) ReconcileFinalNodesForToken(generation uint64, nodes map[string]discovery.NodeInfo) bool {
	return rt.ReconcileFinalNodesForTokenAndPublish(generation, nodes, nil) == nil
}

func (rt *Runtime) ReconcileFinalNodesForTokenAndPublish(generation uint64, nodes map[string]discovery.NodeInfo, publish func() error) error {
	var prepare func(*FinalPublication) error
	if publish != nil {
		prepare = func(*FinalPublication) error {
			return publish()
		}
	}
	return rt.ReconcileFinalNodesForTokenAndCommit(generation, nodes, prepare, nil)
}

func (rt *Runtime) ReconcileFinalNodesForTokenAndCommit(generation uint64, nodes map[string]discovery.NodeInfo, prepare func(*FinalPublication) error, commit func()) error {
	if rt == nil {
		return fmt.Errorf("herdr runtime missing")
	}
	paneBackends := make(map[string]multiplexer.HerdrBackend)
	handAdapters := make(map[string]controlplane.HerdrHandAdapter)
	for _, nodeInfo := range nodes {
		if multiplexer.BackendKindFromString(nodeInfo.Backend) != multiplexer.BackendKindHerdr || nodeInfo.PaneID == "" {
			continue
		}
		backend := rt.backendForPane(nodeInfo.HerdrTabID, nodeInfo.PaneID)
		paneBackends[nodeInfo.PaneID] = backend
		handAdapters[nodeInfo.PaneID] = controlplane.HerdrHandAdapter{
			HerdrInteractiveDeliveryAdapter: controlplane.HerdrInteractiveDeliveryAdapter{
				Backend:        backend,
				InputSanitizer: notification.PrepareInteractivePaneMessage,
			},
		}
	}
	workspaceBackend := rt.backendForWorkspace()
	stagedOwnership := newOwnershipMux(rt.cfg.SessionName)
	stagedOwnership.replaceSnapshot(paneBackends, workspaceBackend)

	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed || (generation != 0 && rt.generation != generation) {
		return fmt.Errorf("stale Herdr final reconcile token")
	}
	if prepare != nil {
		if err := prepare(&FinalPublication{ownershipBackend: stagedOwnership, runtime: rt}); err != nil {
			return err
		}
	}
	multiplexer.LockHerdrPublicationWrite()
	defer multiplexer.UnlockHerdrPublicationWrite()
	rt.ownershipBackend.replaceSnapshot(paneBackends, workspaceBackend)
	rt.adapterCleanup = controlplane.ReplaceHerdrHandAdaptersForOwner(rt.handAdapterOwnerID(), handAdapters)
	if commit != nil {
		commit()
	}
	return nil
}

func (rt *Runtime) ClearPaneRoutes() {
	rt.ClearPaneRoutesForToken(0)
}

func (rt *Runtime) ClearPaneRoutesForToken(generation uint64) {
	if rt == nil {
		return
	}
	rt.reconcilePaneBackends(generation, nil)
}

func (rt *Runtime) Close() {
	if rt == nil {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		return
	}
	rt.closed = true
	rt.generation++
	multiplexer.LockHerdrPublicationWrite()
	defer multiplexer.UnlockHerdrPublicationWrite()
	if rt.adapterCleanup != nil {
		rt.adapterCleanup()
		rt.adapterCleanup = nil
	}
	rt.ownershipBackend.clear()
	for i := len(rt.cleanups) - 1; i >= 0; i-- {
		rt.cleanups[i]()
	}
	rt.cleanups = nil
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

func (rt *Runtime) backendForWorkspace() multiplexer.HerdrBackend {
	readConfig := rt.cfg.ReadConfig()
	readConfig.Policy.ReadScope = multiplexer.HerdrReadScopeDiscovery
	return multiplexer.HerdrBackend{
		Config:         readConfig,
		Client:         rt.client,
		InputSanitizer: notification.PrepareInteractivePaneMessage,
	}
}

func (rt *Runtime) reconcilePaneBackends(generation uint64, livePanes map[string]bool) bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed || (generation != 0 && rt.generation != generation) {
		return false
	}
	if livePanes == nil {
		multiplexer.LockHerdrPublicationWrite()
		defer multiplexer.UnlockHerdrPublicationWrite()
		rt.ownershipBackend.replaceSnapshot(nil, multiplexer.HerdrBackend{})
		if rt.adapterCleanup != nil {
			rt.adapterCleanup()
			rt.adapterCleanup = nil
		}
	}
	return true
}

func (rt *Runtime) handAdapterOwnerID() string {
	runtime := rt.cfg.ReadConfig().Runtime
	return strings.TrimSpace(runtime.SocketPath) + "\x00" + strings.TrimSpace(runtime.SessionName) + "\x00" + strings.TrimSpace(runtime.WorkspaceID)
}

type ownershipSnapshot struct {
	byPane         map[herdrOwnershipKey]multiplexer.HerdrBackend
	sessionBackend *multiplexer.HerdrBackend
	clearBackend   *multiplexer.HerdrBackend
}

type herdrOwnershipKey struct {
	SocketPath  string
	SessionName string
	WorkspaceID string
	PaneID      string
}

type ownershipMux struct {
	sessionName string
	runtime     multiplexer.HerdrRuntimeIdentity
	mu          sync.RWMutex
	snapshot    ownershipSnapshot
}

func newOwnershipMux(sessionName string) *ownershipMux {
	return &ownershipMux{sessionName: sessionName}
}

func (m *ownershipMux) Kind() multiplexer.BackendKind {
	return multiplexer.BackendKindHerdr
}

func (m *ownershipMux) HerdrRuntimeIdentity() multiplexer.HerdrRuntimeIdentity {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.runtime
}

func (m *ownershipMux) clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.snapshot = ownershipSnapshot{}
	m.runtime = multiplexer.HerdrRuntimeIdentity{}
}

func (m *ownershipMux) replaceSnapshot(panes map[string]multiplexer.HerdrBackend, workspaceBackend multiplexer.HerdrBackend) {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := ownershipSnapshot{byPane: make(map[herdrOwnershipKey]multiplexer.HerdrBackend, len(panes))}
	keys := make([]string, 0, len(panes))
	for paneID := range panes {
		keys = append(keys, paneID)
	}
	sort.Strings(keys)
	for _, paneID := range keys {
		backend := panes[paneID]
		snapshot.byPane[herdrOwnershipKeyForBackend(backend, paneID)] = backend
	}
	if len(panes) == 0 {
		sessionBackend := workspaceBackend
		clearBackend := workspaceBackend
		snapshot.sessionBackend = &sessionBackend
		snapshot.clearBackend = &clearBackend
		m.runtime = workspaceBackend.Config.Runtime
		m.snapshot = snapshot
		return
	}
	firstBackend := panes[keys[0]]
	sessionBackend := firstBackend
	clearBackend := firstBackend
	snapshot.sessionBackend = &sessionBackend
	snapshot.clearBackend = &clearBackend
	m.runtime = firstBackend.Config.Runtime
	m.snapshot = snapshot
}

func (m *ownershipMux) backendForSession(sessionName string) (multiplexer.HerdrBackend, error) {
	if strings.TrimSpace(sessionName) != strings.TrimSpace(m.sessionName) {
		return multiplexer.HerdrBackend{}, multiplexer.ErrHerdrSessionNameMismatch
	}
	multiplexer.LockHerdrPublicationRead()
	defer multiplexer.UnlockHerdrPublicationRead()
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snapshot.sessionBackend != nil {
		return *m.snapshot.sessionBackend, nil
	}
	return multiplexer.HerdrBackend{}, fmt.Errorf("%w: no herdr pane backend registered for session %q", multiplexer.ErrHerdrReadClientMissing, sessionName)
}

func (m *ownershipMux) backendForSessionClear(sessionName string) (multiplexer.HerdrBackend, error) {
	if strings.TrimSpace(sessionName) != strings.TrimSpace(m.sessionName) {
		return multiplexer.HerdrBackend{}, multiplexer.ErrHerdrSessionNameMismatch
	}
	multiplexer.LockHerdrPublicationRead()
	defer multiplexer.UnlockHerdrPublicationRead()
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.snapshot.clearBackend != nil {
		return *m.snapshot.clearBackend, nil
	}
	return multiplexer.HerdrBackend{}, fmt.Errorf("%w: no herdr session clear backend registered for session %q", multiplexer.ErrHerdrReadClientMissing, sessionName)
}

func (m *ownershipMux) backendForPane(pane multiplexer.ResourceID) (multiplexer.HerdrBackend, error) {
	if pane.Backend != multiplexer.BackendKindHerdr {
		return multiplexer.HerdrBackend{}, fmt.Errorf("herdr ownership requires herdr pane resource: %#v", pane)
	}
	multiplexer.LockHerdrPublicationRead()
	defer multiplexer.UnlockHerdrPublicationRead()
	m.mu.RLock()
	defer m.mu.RUnlock()
	backend, ok := m.snapshot.byPane[herdrOwnershipKeyForResource(pane)]
	if !ok && pane.HerdrRuntime.SocketPath == "" && pane.HerdrRuntime.WorkspaceID == "" {
		backend, ok = m.backendForLegacyPaneLocked(pane.Native)
	}
	if !ok {
		return multiplexer.HerdrBackend{}, fmt.Errorf("herdr pane backend not registered for %q", pane.Native)
	}
	return backend, nil
}

func (m *ownershipMux) backendForLegacyPaneLocked(paneID string) (multiplexer.HerdrBackend, bool) {
	var (
		backend multiplexer.HerdrBackend
		found   bool
	)
	for key, candidate := range m.snapshot.byPane {
		if key.PaneID != paneID {
			continue
		}
		if found {
			return multiplexer.HerdrBackend{}, false
		}
		backend = candidate
		found = true
	}
	return backend, found
}

func herdrOwnershipKeyForBackend(backend multiplexer.HerdrBackend, paneID string) herdrOwnershipKey {
	runtime := backend.Config.Runtime
	if runtime.PaneID == "" {
		runtime.PaneID = paneID
	}
	return herdrOwnershipKey{
		SocketPath:  strings.TrimSpace(runtime.SocketPath),
		SessionName: strings.TrimSpace(runtime.SessionName),
		WorkspaceID: strings.TrimSpace(runtime.WorkspaceID),
		PaneID:      strings.TrimSpace(runtime.PaneID),
	}
}

func herdrOwnershipKeyForResource(pane multiplexer.ResourceID) herdrOwnershipKey {
	runtime := pane.HerdrRuntime
	if runtime.PaneID == "" {
		runtime.PaneID = pane.Native
	}
	return herdrOwnershipKey{
		SocketPath:  strings.TrimSpace(runtime.SocketPath),
		SessionName: strings.TrimSpace(runtime.SessionName),
		WorkspaceID: strings.TrimSpace(runtime.WorkspaceID),
		PaneID:      strings.TrimSpace(runtime.PaneID),
	}
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
	backend, err := m.backendForSessionClear(sessionName)
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
