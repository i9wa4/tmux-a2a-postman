package projection

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

// CompactionObservationNode is the replayed state of one node/pane. Epoch
// advances on each new observed occurrence and authoritative closure.
type CompactionObservationNode struct {
	NodeKey                string
	PaneID                 string
	CanonicalLifecycle     string
	OccurrenceEpoch        uint64
	LastObservationOrdinal uint64
	LastObservationDigest  string
	ActivePredecessorKey   string
	MarkerFingerprint      string
	MarkerCount            int
}

type CompactionObservationState struct {
	Nodes map[string]CompactionObservationNode
}

const (
	maxCompactionAliasTokenBytes = 256
	maxCompactionAliasEntries    = 256
	maxCompactionAliasBytes      = 16 * 1024
)

type compactionObservationReducer struct {
	sessionKey string
	generation int
	nodes      map[string]CompactionObservationNode
	aliases    map[string]map[string]string
}

func compactionNodeID(nodeKey, paneID string) string {
	// Byte lengths make the tuple unambiguous even when either value contains
	// NULs, colons, or digits.
	return fmt.Sprintf("compaction-node-v1:%d:%s%d:%s", len(nodeKey), nodeKey, len(paneID), paneID)
}

// CompactionMarkerFingerprint uses a cryptographic fingerprint of the
// normalized marker line. The delivery key never relies on screen CRC32.
func CompactionMarkerFingerprint(markerLine string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(markerLine)))
	return hex.EncodeToString(sum[:])
}

// CompactionDeliveryKey is stable across process restarts within one session
// generation. Each field is length-prefixed to avoid ambiguous concatenation.
func CompactionDeliveryKey(sessionKey string, generation int, nodeKey, paneID, lifecycle, markerFingerprint string, markerCount int, epoch uint64) (string, error) {
	if sessionKey == "" || generation < 1 || nodeKey == "" || paneID == "" || lifecycle == "" ||
		len(markerFingerprint) != sha256.Size*2 || markerCount < 1 {
		return "", fmt.Errorf("invalid compaction delivery identity")
	}
	fingerprintBytes, err := hex.DecodeString(markerFingerprint)
	if err != nil {
		return "", fmt.Errorf("invalid marker fingerprint: %w", err)
	}
	markerFingerprint = hex.EncodeToString(fingerprintBytes)
	h := sha256.New()
	for _, part := range []string{"compaction-delivery-v1", sessionKey, fmt.Sprint(generation), nodeKey, paneID, lifecycle, markerFingerprint, fmt.Sprint(markerCount), fmt.Sprint(epoch)} {
		writeCompactionKeyPart(h, part)
	}
	return "compaction:v1:" + hex.EncodeToString(h.Sum(nil)), nil
}

func writeCompactionKeyPart(h hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = h.Write([]byte(value))
}

// ProjectCompactionObservation reads the current generation and replays its
// observation evidence. Unavailable or corrupt journal state fails closed.
func ProjectCompactionObservation(sessionDir string) (CompactionObservationState, error) {
	state, err := journal.ReadCurrentSessionState(sessionDir)
	if err != nil {
		return CompactionObservationState{}, err
	}
	events, err := journal.Replay(sessionDir)
	if err != nil {
		return CompactionObservationState{}, err
	}
	return ReduceCompactionObservationEvents(state.SessionKey, state.Generation, events)
}

// ReduceCompactionObservationEvents is a deterministic replay seam for tests
// and future hydration. It does not append events or perform delivery.
func ReduceCompactionObservationEvents(sessionKey string, generation int, events []journal.Event) (CompactionObservationState, error) {
	if sessionKey == "" || generation < 1 {
		return CompactionObservationState{}, fmt.Errorf("invalid compaction observation session")
	}
	r := compactionObservationReducer{
		sessionKey: sessionKey,
		generation: generation,
		nodes:      make(map[string]CompactionObservationNode),
		aliases:    make(map[string]map[string]string),
	}
	for _, event := range events {
		if event.SessionKey != sessionKey || event.Generation != generation {
			continue
		}
		switch event.Type {
		case journal.CompactionObservationPresentEventType, journal.CompactionObservationAbsentEventType, journal.CompactionLifecycleAliasedEventType:
		default:
			continue
		}
		if event.Visibility != journal.VisibilityControlPlaneOnly {
			return CompactionObservationState{}, fmt.Errorf("compaction observation sequence %d has invalid visibility", event.Sequence)
		}
		var payload journal.CompactionObservationPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return CompactionObservationState{}, fmt.Errorf("decoding compaction observation sequence %d: %w", event.Sequence, err)
		}
		if payload.Version != journal.CompactionObservationVersion || payload.NodeKey == "" || payload.PaneID == "" {
			return CompactionObservationState{}, fmt.Errorf("invalid compaction observation sequence %d", event.Sequence)
		}
		id := compactionNodeID(payload.NodeKey, payload.PaneID)
		var err error
		switch event.Type {
		case journal.CompactionLifecycleAliasedEventType:
			err = r.alias(id, payload)
		case journal.CompactionObservationPresentEventType:
			err = r.present(id, payload)
		case journal.CompactionObservationAbsentEventType:
			err = r.absent(id, payload)
		}
		if err != nil {
			return CompactionObservationState{}, fmt.Errorf("compaction observation sequence %d: %w", event.Sequence, err)
		}
	}
	return CompactionObservationState{Nodes: r.nodes}, nil
}

func compactionObservationDigest(eventType string, payload journal.CompactionObservationPayload) string {
	encoded, _ := json.Marshal(struct {
		Type    string
		Payload journal.CompactionObservationPayload
	}{eventType, payload})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (r *compactionObservationReducer) observeOrdinal(state CompactionObservationNode, ordinal uint64, digest string) (bool, error) {
	if ordinal < state.LastObservationOrdinal {
		return false, nil
	}
	if ordinal == state.LastObservationOrdinal {
		if digest != state.LastObservationDigest {
			return false, fmt.Errorf("conflicting compaction observation ordinal")
		}
		return false, nil
	}
	return true, nil
}

func (r *compactionObservationReducer) resolve(id, token string) (string, error) {
	if len(token) > maxCompactionAliasTokenBytes {
		return "", fmt.Errorf("lifecycle token exceeds byte budget")
	}
	seen := make(map[string]bool)
	for next := r.aliases[id][token]; next != ""; next = r.aliases[id][token] {
		if len(seen) >= maxCompactionAliasEntries {
			return "", fmt.Errorf("lifecycle alias resolution budget exceeded")
		}
		if seen[token] || next == token {
			return "", fmt.Errorf("cyclic lifecycle alias")
		}
		seen[token] = true
		token = next
	}
	return token, nil
}

func (r *compactionObservationReducer) alias(id string, payload journal.CompactionObservationPayload) error {
	from, to := payload.LifecycleToken, payload.CanonicalToken
	if len(from) > maxCompactionAliasTokenBytes || len(to) > maxCompactionAliasTokenBytes {
		return fmt.Errorf("lifecycle alias token exceeds byte budget")
	}
	if from == "" || to == "" || from == to {
		return fmt.Errorf("invalid lifecycle alias")
	}
	if r.aliases[id] == nil {
		r.aliases[id] = make(map[string]string)
	}
	if prior := r.aliases[id][from]; prior != "" && prior != to {
		return fmt.Errorf("conflicting lifecycle alias")
	}
	if r.aliases[id][from] == "" {
		if len(r.aliases[id]) >= maxCompactionAliasEntries {
			return fmt.Errorf("lifecycle alias entry budget exceeded")
		}
		bytes := len(from) + len(to)
		for existingFrom, existingTo := range r.aliases[id] {
			bytes += len(existingFrom) + len(existingTo)
		}
		if bytes > maxCompactionAliasBytes {
			return fmt.Errorf("lifecycle alias byte budget exceeded")
		}
	}
	resolved, err := r.resolve(id, to)
	if err != nil {
		return err
	}
	if resolved == from {
		return fmt.Errorf("cyclic lifecycle alias")
	}
	if state := r.nodes[id]; state.CanonicalLifecycle == from && state.ActivePredecessorKey != "" {
		return fmt.Errorf("lifecycle alias would change active predecessor identity")
	}
	r.aliases[id][from] = to
	return nil
}

func (r *compactionObservationReducer) present(id string, payload journal.CompactionObservationPayload) error {
	// A producer ordinal orders captures independently of journal append order.
	if !payload.CaptureSucceeded || !payload.PanePresent || payload.LifecycleToken == "" || payload.MarkerCount < 1 || payload.ObservationOrdinal == 0 {
		return fmt.Errorf("invalid compaction presence")
	}
	canonical, err := r.resolve(id, payload.LifecycleToken)
	if err != nil {
		return err
	}
	fingerprintBytes, err := hex.DecodeString(payload.MarkerFingerprint)
	if err != nil || len(fingerprintBytes) != sha256.Size {
		return fmt.Errorf("invalid marker fingerprint")
	}
	payload.MarkerFingerprint = hex.EncodeToString(fingerprintBytes)
	state := r.nodes[id]
	digest := compactionObservationDigest(journal.CompactionObservationPresentEventType, payload)
	newer, err := r.observeOrdinal(state, payload.ObservationOrdinal, digest)
	if err != nil || !newer {
		return err
	}
	if state.NodeKey == "" {
		state.NodeKey, state.PaneID = payload.NodeKey, payload.PaneID
	}
	if state.ActivePredecessorKey != "" && (state.CanonicalLifecycle != canonical || state.MarkerFingerprint != payload.MarkerFingerprint || state.MarkerCount != payload.MarkerCount) {
		if state.OccurrenceEpoch == ^uint64(0) {
			return fmt.Errorf("compaction occurrence epoch exhausted")
		}
		state.OccurrenceEpoch++
	}
	key, err := CompactionDeliveryKey(r.sessionKey, r.generation, payload.NodeKey, payload.PaneID, canonical, payload.MarkerFingerprint, payload.MarkerCount, state.OccurrenceEpoch)
	if err != nil {
		return err
	}
	state.CanonicalLifecycle = canonical
	state.ActivePredecessorKey = key
	state.MarkerFingerprint = payload.MarkerFingerprint
	state.MarkerCount = payload.MarkerCount
	state.LastObservationOrdinal = payload.ObservationOrdinal
	state.LastObservationDigest = digest
	r.nodes[id] = state
	return nil
}

func (r *compactionObservationReducer) absent(id string, payload journal.CompactionObservationPayload) error {
	// Only a successful full-history scan of a still-listed pane can close a
	// committed predecessor. Other gaps are evidence of uncertainty.
	if !payload.CaptureSucceeded || !payload.FullHistory || !payload.PanePresent {
		return nil
	}
	if payload.LifecycleToken == "" || payload.PredecessorKey == "" || payload.ObservationOrdinal == 0 {
		return fmt.Errorf("invalid authoritative compaction absence")
	}
	canonical, err := r.resolve(id, payload.LifecycleToken)
	if err != nil {
		return err
	}
	state := r.nodes[id]
	digest := compactionObservationDigest(journal.CompactionObservationAbsentEventType, payload)
	newer, err := r.observeOrdinal(state, payload.ObservationOrdinal, digest)
	if err != nil || !newer {
		return err
	}
	if state.NodeKey == "" {
		state.NodeKey, state.PaneID = payload.NodeKey, payload.PaneID
	}
	state.LastObservationOrdinal = payload.ObservationOrdinal
	state.LastObservationDigest = digest
	if state.CanonicalLifecycle == canonical && state.ActivePredecessorKey == payload.PredecessorKey && state.OccurrenceEpoch == payload.ExpectedEpoch {
		if state.OccurrenceEpoch == ^uint64(0) {
			return fmt.Errorf("compaction occurrence epoch exhausted")
		}
		state.OccurrenceEpoch++
		state.ActivePredecessorKey = ""
		state.MarkerFingerprint = ""
		state.MarkerCount = 0
	}
	r.nodes[id] = state
	return nil
}
