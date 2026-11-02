package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
)

type statusConfigRecord struct {
	Version     int     `json:"version"`
	Binding     Binding `json:"binding"`
	Path        string  `json:"path"`
	Fingerprint string  `json:"fingerprint"`
	Builtin     bool    `json:"builtin,omitempty"`
}

func StatusConfigFingerprint(topology config.WorkspaceTopology) string {
	raw, _ := json.Marshal(topology)
	if id := topology.BuiltinBundleID(); id != "" {
		raw = append(raw, []byte("\x00builtin:"+id)...)
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// This is registration provenance, written only inside verified normal operation
// admission. refresh-status neither invents registrations nor trusts its own
// caller's --config to define server membership.
func saveStatusConfig(ws *os.Root, snapshot Snapshot) error {
	if snapshot.ConfigPath == "" {
		return nil
	}
	if !filepath.IsAbs(snapshot.ConfigPath) || snapshot.ConfigFingerprint == "" {
		return ErrUnknown
	}
	dir, err := privateDir(ws, "runtime", false)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	name := "status-config-p" + snapshot.Caller.Owner.Pane[1:] + ".json"
	record := statusConfigRecord{Version: 1, Binding: snapshot.Caller, Path: snapshot.ConfigPath, Fingerprint: snapshot.ConfigFingerprint, Builtin: snapshot.BuiltinConfig}
	if record.Builtin {
		record.Version = 2
	}
	var previous statusConfigRecord
	if readJSON(dir, name, &previous) == nil && previous == record {
		return nil
	}
	return replaceJSON(dir, name, record)
}

func readStatusScope(ctx context.Context, ws *os.Root, bindings []Binding) (*StatusScope, []string, error) {
	dir, err := privateDir(ws, "runtime", false)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = dir.Close() }()
	var chosen *StatusScope
	var nodes []string
	for _, b := range bindings {
		var record statusConfigRecord
		err := readJSON(dir, "status-config-p"+b.Owner.Pane[1:]+".json", &record)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		if record.Binding.Owner != b.Owner || record.Binding.PanePID != b.PanePID {
			continue
		} // Former processes cannot supply provenance.
		if (record.Version != 1 && record.Version != 2) || record.Version == 1 && record.Builtin || record.Version == 2 && !record.Builtin || !filepath.IsAbs(record.Path) {
			return nil, nil, ErrUnknown
		}
		topology, err := (config.Source{Path: record.Path, Builtin: record.Builtin}).Load(ctx)
		if err != nil {
			return nil, nil, err
		}
		fingerprint := StatusConfigFingerprint(topology)
		if fingerprint != record.Fingerprint {
			return nil, nil, errors.New("registered configuration changed; a normal operation must verify it again")
		}
		if chosen != nil && chosen.Fingerprint != fingerprint {
			return nil, nil, errors.New("conflicting registered workspace configurations")
		}
		if chosen == nil {
			chosen = &StatusScope{Config: record.Path, Builtin: record.Builtin, Fingerprint: fingerprint, Edges: topology.Edges, Interface: topology.InterfaceNode, Sessions: []StatusRoute{}}
			nodes = topology.Nodes
			for _, s := range topology.Sessions {
				chosen.Sessions = append(chosen.Sessions, StatusRoute{s.Name, s.Parent, s.InterfaceNode})
			}
		}
	}
	if chosen == nil {
		return nil, nil, errors.New("workspace configuration provenance unavailable; run a normal configured operation")
	}
	return chosen, nodes, nil
}

func (m Mail) aggregateStatus(ctx context.Context, snapshot *ServerStatus) {
	type location struct{ session, window, pane int }
	groups := map[string][]location{}
	order := []string{}
	for si, s := range snapshot.Sessions {
		for wi, w := range s.Windows {
			for pi, p := range w.Panes {
				if p.Workspace == "" {
					continue
				}
				key := s.ID + "\x00" + p.Workspace
				if _, ok := groups[key]; !ok {
					order = append(order, key)
				}
				groups[key] = append(groups[key], location{si, wi, pi})
			}
		}
	}
	for _, key := range order {
		locations := groups[key]
		first := locations[0]
		root := snapshot.Sessions[first.session].Windows[first.window].Panes[first.pane].Workspace
		owner := Owner{Server: snapshot.Server.CanonicalSocket, Incarnation: snapshot.Server.Incarnation, Session: snapshot.Sessions[first.session].ID}
		provenance := []Binding{}
		titles := map[string]int{}
		for _, l := range locations {
			p := snapshot.Sessions[l.session].Windows[l.window].Panes[l.pane]
			titles[p.Title]++
			if !p.Dead && !p.Linked {
				provenance = append(provenance, Binding{Role: p.Title, Owner: Owner{owner.Server, owner.Incarnation, owner.Session, p.ID}, PanePID: p.PID})
			}
		}
		saved, scope, nodes, scopeErr := m.Store.readStatusAttribution(ctx, root, owner, provenance)
		configured := map[string]bool{}
		for _, node := range nodes {
			configured[node] = true
		}
		eligible := map[location]Binding{}
		for _, l := range locations {
			pane := &snapshot.Sessions[l.session].Windows[l.window].Panes[l.pane]
			current := Binding{Owner: Owner{owner.Server, owner.Incarnation, owner.Session, pane.ID}, PanePID: pane.PID}
			bound := false
			for _, b := range saved.Bindings {
				if b.Owner == current.Owner && b.PanePID == current.PanePID {
					current.Role = b.Role
					bound = true
				}
			}
			if !bound {
				if configured[pane.Title] {
					pane.Status = &NodeStatus{Node: pane.Title, Reason: "native pane lacks a matching saved binding"}
				} else if scopeErr != nil && pane.Reason == "" {
					pane.Reason = "configuration attribution unavailable: " + scopeErr.Error()
				}
				continue
			}
			pane.Status = &NodeStatus{Node: current.Role}
			if pane.Capture != nil {
				pane.Status.LastCaptureAt = &pane.Capture.At
				pane.Status.LastScreenChangeAt = pane.Capture.ChangedAt
			}
			if scopeErr != nil {
				pane.Status.Reason = scopeErr.Error()
				continue
			}
			if pane.Dead || pane.Linked || current.Role != pane.Title || !configured[current.Role] || titles[current.Role] != 1 {
				pane.Status.Reason = "dead, linked, renamed, unconfigured or ambiguous native role pane"
				continue
			}
			pane.Scope = scope
			eligible[l] = current
		}
		if len(eligible) == 0 {
			continue
		}
		// Mail counts require the shared mailbox lock. Configuration attribution
		// uses only atomic private registration records, so a busy mail writer
		// does not erase a verified interface's configured scope.
		err := m.Store.withReadWorkspace(ctx, root, func(ws *os.Root) error {
			fresh, err := readOperatorRuntime(ws, owner)
			if err != nil {
				return err
			}
			bindings := map[Binding]bool{}
			for _, b := range fresh.Bindings {
				bindings[b] = true
			}
			for _, l := range locations {
				current, ok := eligible[l]
				if !ok {
					continue
				}
				pane := &snapshot.Sessions[l.session].Windows[l.window].Panes[l.pane]
				if !bindings[current] {
					pane.Scope = nil
					pane.Status.Reason = "saved binding changed before mail aggregation"
					continue
				}
				status := nodeSummary(ctx, ws, current.Role)
				if pane.Capture != nil {
					status.LastCaptureAt = &pane.Capture.At
					status.LastScreenChangeAt = pane.Capture.ChangedAt
				}
				pane.Status = &status
			}
			return nil
		})
		if err != nil {
			for l := range eligible {
				p := &snapshot.Sessions[l.session].Windows[l.window].Panes[l.pane]
				p.Status.Reason = "mail aggregation unavailable: " + err.Error()
			}
		}

	}
}

// readStatusAttribution opens only existing owned metadata. It does not open
// mailboxes, initialize state or acquire a mailbox lock. Individual registration
// files are atomic, all bindings/config sources are checked, and the collector
// rechecks full native identity before publication. Mail reads separately recheck
// their binding while holding the shared workspace lock.
func (s Store) readStatusAttribution(ctx context.Context, root string, owner Owner, provenance []Binding) (runtimeRecord, *StatusScope, []string, error) {
	var saved runtimeRecord
	if err := ctx.Err(); err != nil {
		return saved, nil, nil, err
	}
	name, err := Name(root)
	if err != nil {
		return saved, nil, nil, err
	}
	info, err := os.Lstat(s.StateRoot)
	if err != nil {
		return saved, nil, nil, err
	}
	if !stateRootInfo(info) {
		return saved, nil, nil, ErrUnknown
	}
	base, err := os.OpenRoot(s.StateRoot)
	if err != nil {
		return saved, nil, nil, err
	}
	defer func() { _ = base.Close() }()
	opened, err := base.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return saved, nil, nil, ErrUnknown
	}
	ws, err := privateDir(base, name, false)
	if err != nil {
		return saved, nil, nil, err
	}
	defer func() { _ = ws.Close() }()
	var identity workspaceRecord
	if readJSON(ws, "workspace.json", &identity) != nil || identity != (workspaceRecord{1, root, name}) {
		return saved, nil, nil, ErrUnknown
	}
	saved, err = readOperatorRuntime(ws, owner)
	if err != nil {
		return saved, nil, nil, err
	}
	scope, nodes, err := readStatusScope(ctx, ws, provenance)
	return saved, scope, nodes, err
}

// readCachedPaneTimes retains operation-local self semantics: the operation's
// verified binding must match the saved native process before using its clock.
func (m Mail) readCachedPaneTimes(snapshot Snapshot, role string) (*string, *string) {
	var b Binding
	for _, candidate := range snapshot.Bindings {
		if candidate.Role == role {
			b = candidate
		}
	}
	if !b.Owner.valid() {
		return nil, nil
	}
	pid := 0
	parts := strings.Split(b.Owner.Incarnation, ":")
	if len(parts) > 0 {
		pid, _ = strconv.Atoi(parts[0])
	}
	_, status, err := m.Store.ReadServerStatus(ServerContext{Socket: b.Owner.Server, PID: pid})
	if err != nil || status.Server.Incarnation != b.Owner.Incarnation {
		return nil, nil
	}
	for _, s := range status.Sessions {
		if s.ID != b.Owner.Session {
			continue
		}
		for _, w := range s.Windows {
			for _, p := range w.Panes {
				if p.ID == b.Owner.Pane && p.PID == b.PanePID && p.Title == role && p.Workspace == snapshot.Root && p.Capture != nil && !p.Dead && !p.Linked {
					at, e := time.Parse(time.RFC3339Nano, p.Capture.At)
					if e == nil && !at.After(time.Now().UTC()) {
						return &p.Capture.At, p.Capture.ChangedAt
					}
				}
			}
		}
	}
	return nil, nil
}

// Keep configuration-save failure supplemental to authoritative mail mutations.
func noteStatusConfigError(ctx context.Context, err error) {
	if err != nil {
		diagnostic.From(ctx).MarkFailed()
	}
}
