package autoping

import (
	"strings"
	"sync"

	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

type WakeIdentity struct {
	SessionDir  string
	NodeKey     string
	PaneID      string
	Reason      string
	TriggeredAt string
	NotBeforeAt string
}

type Reservation struct {
	key string
}

var wakeReservations = struct {
	sync.Mutex
	active map[string]struct{}
}{
	active: make(map[string]struct{}),
}

func IdentityFromPending(sessionDir, nodeKey string, pending projection.AutoPingNodeState) WakeIdentity {
	return WakeIdentity{
		SessionDir:  sessionDir,
		NodeKey:     nodeKey,
		PaneID:      pending.PaneID,
		Reason:      pending.Reason,
		TriggeredAt: pending.TriggeredAt,
		NotBeforeAt: pending.NotBeforeAt,
	}
}

func CurrentPendingIdentity(sessionDir, nodeKey, paneID string) (WakeIdentity, bool, error) {
	state, ok, err := projection.ProjectAutoPingState(sessionDir)
	if err != nil || !ok {
		return WakeIdentity{}, false, err
	}
	current, exists := state.Nodes[nodeKey]
	if !exists || !current.Pending || current.PaneID != paneID {
		return WakeIdentity{}, false, nil
	}
	return IdentityFromPending(sessionDir, nodeKey, current), true, nil
}

func TryReserve(identity WakeIdentity) (*Reservation, bool) {
	key := identity.key()
	if key == "" {
		return nil, true
	}

	wakeReservations.Lock()
	defer wakeReservations.Unlock()
	if _, exists := wakeReservations.active[key]; exists {
		return nil, false
	}
	wakeReservations.active[key] = struct{}{}
	return &Reservation{key: key}, true
}

func (r *Reservation) Release() {
	if r == nil || r.key == "" {
		return
	}
	wakeReservations.Lock()
	delete(wakeReservations.active, r.key)
	wakeReservations.Unlock()
}

func (identity WakeIdentity) MatchesPending(current projection.AutoPingNodeState) bool {
	return current.Pending &&
		current.PaneID == identity.PaneID &&
		current.Reason == identity.Reason &&
		current.TriggeredAt == identity.TriggeredAt &&
		current.NotBeforeAt == identity.NotBeforeAt
}

func (identity WakeIdentity) key() string {
	if identity.SessionDir == "" || identity.NodeKey == "" || identity.PaneID == "" || identity.TriggeredAt == "" || identity.NotBeforeAt == "" {
		return ""
	}
	return strings.Join([]string{
		identity.SessionDir,
		identity.NodeKey,
		identity.PaneID,
		identity.Reason,
		identity.TriggeredAt,
		identity.NotBeforeAt,
	}, "\x00")
}
