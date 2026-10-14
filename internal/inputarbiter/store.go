// Package inputarbiter records ownership and accepted inputs for one human pane
// per tmux server. It does not submit text to a terminal or an agent runtime.
package inputarbiter

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

var (
	ErrOwned             = errors.New("server already has a different pane owner")
	ErrStale             = errors.New("owner fence is stale")
	ErrDuplicateConflict = errors.New("input source ID has conflicting content")
	ErrEpochExhausted    = errors.New("owner epoch exhausted")
)

// ServerKey identifies one tmux server, independent of its display name.
// SocketPath must be the canonical absolute socket path resolved by the caller.
type ServerKey struct {
	InstallationID string `json:"installation_id"`
	SocketPath     string `json:"socket_path"`
}

type Owner struct {
	ServerIncarnation string `json:"server_incarnation"`
	PaneID            string `json:"pane_id"`
	PaneIncarnation   string `json:"pane_incarnation"`
	ConversationID    string `json:"conversation_id"`
	Designation       string `json:"designation"`
	Epoch             uint64 `json:"epoch"`
	LeaseID           string `json:"lease_id"`
}

type Origin string

const (
	OriginDirect Origin = "direct"
	OriginMail   Origin = "mail"
)

// Input is accepted into a conversation before runtime submission. Every input
// remains pending until a separate runtime boundary integration acknowledges it.
type Input struct {
	InputEventID       uint64 `json:"input_event_id"`
	ConversationID     string `json:"conversation_id"`
	Origin             Origin `json:"origin"`
	DirectSubmissionID string `json:"direct_submission_id,omitempty"`
	MailMessageID      string `json:"mail_message_id,omitempty"`
	ContentDigest      string `json:"content_digest"`
	Pending            bool   `json:"pending"`
}

type State struct {
	Key        ServerKey       `json:"key"`
	Owner      Owner           `json:"owner"`
	Inputs     []Input         `json:"inputs"`
	Rebindings []RebindReceipt `json:"rebindings,omitempty"`
}

// RebindReceipt makes a committed rebind recoverable after an uncertain write.
type RebindReceipt struct {
	RequestID string `json:"request_id"`
	From      Owner  `json:"from"`
	Proposed  Owner  `json:"proposed"`
	Result    Owner  `json:"result"`
}

// Store uses one flock and atomic state file per server key. The lock file is
// never replaced, so processes cannot accidentally lock different inodes.
type Store struct {
	Root string
	// afterRename injects the uncertain-commit window in package tests.
	afterRename func() error
	// directorySync is replaced only by tests that observe the durability barrier.
	directorySync func(string) error
}

func (s Store) Claim(key ServerKey, proposed Owner) (Owner, error) {
	if err := validate(key, proposed); err != nil {
		return Owner{}, err
	}
	var result Owner
	err := s.change(key, func(state *State) (bool, error) {
		if state.Owner.Epoch != 0 {
			if samePane(state.Owner, proposed) {
				result = state.Owner
				return false, nil
			}
			return false, ErrOwned
		}
		proposed.Epoch = 1
		lease, err := newLease()
		if err != nil {
			return false, err
		}
		proposed.LeaseID = lease
		state.Owner = proposed
		result = proposed
		return true, nil
	})
	return result, err
}

// Rebind is an explicit compare-and-swap. Socket reuse and pane restart both
// require the caller to present the current owner fence before rotation.
func (s Store) Rebind(key ServerKey, requestID string, current Owner, proposed Owner) (Owner, error) {
	if err := validate(key, proposed); err != nil {
		return Owner{}, err
	}
	if requestID == "" || proposed.Epoch != 0 || proposed.LeaseID != "" {
		return Owner{}, errors.New("invalid rebind request")
	}
	var result Owner
	err := s.change(key, func(state *State) (bool, error) {
		for _, receipt := range state.Rebindings {
			if receipt.RequestID == requestID {
				if receipt.From != current || receipt.Proposed != proposed {
					return false, ErrDuplicateConflict
				}
				result = receipt.Result
				return false, nil
			}
		}
		if !sameFence(state.Owner, current) {
			return false, ErrStale
		}
		next, err := nextEpoch(current.Epoch)
		if err != nil {
			return false, err
		}
		requested := proposed
		lease, err := newLease()
		if err != nil {
			return false, err
		}
		proposed.Epoch = next
		proposed.LeaseID = lease
		state.Owner = proposed
		state.Rebindings = append(state.Rebindings, RebindReceipt{RequestID: requestID, From: current, Proposed: requested, Result: proposed})
		result = proposed
		return true, nil
	})
	return result, err
}

// sourceID is a stable direct submission ID or a Postman mail message ID.
// Retrying the same source ID in the same conversation returns its first event.
func (s Store) Accept(key ServerKey, fence Owner, origin Origin, sourceID, digest string) (Input, error) {
	if digest == "" || sourceID == "" || (origin != OriginDirect && origin != OriginMail) {
		return Input{}, errors.New("invalid input metadata")
	}
	var result Input
	err := s.change(key, func(state *State) (bool, error) {
		if !sameFence(state.Owner, fence) {
			return false, ErrStale
		}
		for _, input := range state.Inputs {
			if input.ConversationID != state.Owner.ConversationID || input.Origin != origin {
				continue
			}
			if (origin == OriginMail && input.MailMessageID == sourceID) || (origin == OriginDirect && input.DirectSubmissionID == sourceID) {
				if input.ContentDigest != digest {
					return false, ErrDuplicateConflict
				}
				result = input
				return false, nil
			}
		}
		result = Input{InputEventID: uint64(len(state.Inputs) + 1), ConversationID: state.Owner.ConversationID, Origin: origin, ContentDigest: digest, Pending: true}
		if origin == OriginMail {
			result.MailMessageID = sourceID
		} else {
			result.DirectSubmissionID = sourceID
		}
		state.Inputs = append(state.Inputs, result)
		return true, nil
	})
	return result, err
}

func (s Store) Read(key ServerKey) (State, error) {
	var snapshot State
	err := s.change(key, func(state *State) (bool, error) {
		snapshot = *state
		snapshot.Inputs = append([]Input(nil), state.Inputs...)
		snapshot.Rebindings = append([]RebindReceipt(nil), state.Rebindings...)
		return false, nil
	})
	return snapshot, err
}

func validate(key ServerKey, owner Owner) error {
	if key.InstallationID == "" || !filepath.IsAbs(key.SocketPath) || filepath.Clean(key.SocketPath) != key.SocketPath || owner.ServerIncarnation == "" || owner.PaneID == "" || owner.PaneIncarnation == "" || owner.ConversationID == "" {
		return errors.New("incomplete server or owner identity")
	}
	return nil
}

func samePane(a, b Owner) bool {
	return a.ServerIncarnation == b.ServerIncarnation && a.PaneID == b.PaneID && a.PaneIncarnation == b.PaneIncarnation && a.ConversationID == b.ConversationID
}

func sameFence(a, b Owner) bool {
	return a.Epoch != 0 && a.Epoch == b.Epoch && a.LeaseID != "" && a.LeaseID == b.LeaseID && samePane(a, b)
}

func validOwner(owner Owner) bool {
	return owner.Epoch != 0 && owner.LeaseID != "" && owner.ServerIncarnation != "" && owner.PaneID != "" && owner.PaneIncarnation != "" && owner.ConversationID != ""
}

func nextEpoch(current uint64) (uint64, error) {
	if current == ^uint64(0) {
		return 0, ErrEpochExhausted
	}
	return current + 1, nil
}

// validateState rejects parseable corruption before any operation sees it.
func validateState(state State) error {
	if state.Owner.Epoch == 0 {
		return errors.New("persisted state has no owner")
	}
	if !validOwner(state.Owner) {
		return errors.New("invalid owner fence")
	}
	if len(state.Rebindings) == 0 && state.Owner.Epoch != 1 {
		return errors.New("owner epoch lacks claim genesis")
	}
	ownedConversations := make(map[string]struct{}, len(state.Rebindings)+1)
	if len(state.Rebindings) == 0 {
		ownedConversations[state.Owner.ConversationID] = struct{}{}
	} else {
		ownedConversations[state.Rebindings[0].From.ConversationID] = struct{}{}
		for _, receipt := range state.Rebindings {
			ownedConversations[receipt.Result.ConversationID] = struct{}{}
		}
	}
	seenInputs := make(map[string]struct{}, len(state.Inputs))
	for i, input := range state.Inputs {
		if input.InputEventID != uint64(i)+1 || input.ConversationID == "" || input.ContentDigest == "" || !input.Pending {
			return errors.New("invalid input event")
		}
		if _, owned := ownedConversations[input.ConversationID]; !owned {
			return errors.New("input conversation was never owned")
		}
		var sourceID string
		switch input.Origin {
		case OriginDirect:
			if input.DirectSubmissionID == "" || input.MailMessageID != "" {
				return errors.New("invalid direct input source")
			}
			sourceID = input.DirectSubmissionID
		case OriginMail:
			if input.MailMessageID == "" || input.DirectSubmissionID != "" {
				return errors.New("invalid mail input source")
			}
			sourceID = input.MailMessageID
		default:
			return errors.New("invalid input origin")
		}
		encoded, _ := json.Marshal([3]string{input.ConversationID, string(input.Origin), sourceID})
		key := string(encoded)
		if _, exists := seenInputs[key]; exists {
			return errors.New("duplicate input source")
		}
		seenInputs[key] = struct{}{}
	}
	seenRequests := make(map[string]struct{}, len(state.Rebindings))
	for i, receipt := range state.Rebindings {
		if receipt.RequestID == "" || !validOwner(receipt.From) || !validOwner(receipt.Result) || receipt.Proposed.Epoch != 0 || receipt.Proposed.LeaseID != "" || receipt.Proposed.ServerIncarnation == "" || receipt.Proposed.PaneID == "" || receipt.Proposed.PaneIncarnation == "" || receipt.Proposed.ConversationID == "" || receipt.From.Epoch == ^uint64(0) || receipt.Result.Epoch != receipt.From.Epoch+1 || receipt.Result.LeaseID == receipt.From.LeaseID || !samePane(receipt.Result, receipt.Proposed) || receipt.Result.Designation != receipt.Proposed.Designation {
			return errors.New("invalid rebind receipt")
		}
		if _, exists := seenRequests[receipt.RequestID]; exists {
			return errors.New("duplicate rebind request")
		}
		seenRequests[receipt.RequestID] = struct{}{}
		if i == 0 && receipt.From.Epoch != 1 {
			return errors.New("rebind chain lacks claim genesis")
		}
		if i > 0 && receipt.From != state.Rebindings[i-1].Result {
			return errors.New("broken rebind chain")
		}
	}
	if len(state.Rebindings) > 0 && state.Owner != state.Rebindings[len(state.Rebindings)-1].Result {
		return errors.New("owner differs from final rebind")
	}
	return nil
}

func (s Store) change(key ServerKey, fn func(*State) (bool, error)) error {
	if s.Root == "" || key.InstallationID == "" || !filepath.IsAbs(key.SocketPath) || filepath.Clean(key.SocketPath) != key.SocketPath {
		return errors.New("invalid store or server key")
	}
	encoded, _ := json.Marshal(key)
	hash := sha256.Sum256(encoded)
	dir := filepath.Join(s.Root, hex.EncodeToString(hash[:]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "state.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	path := filepath.Join(dir, "state.json")
	var state State
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("decode arbiter state: %w", err)
		}
		if state.Key != key {
			return errors.New("server key mismatch")
		}
		if err := validateState(state); err != nil {
			return fmt.Errorf("invalid arbiter state: %w", err)
		}
	} else if errors.Is(err, os.ErrNotExist) {
		state.Key = key
	} else {
		return err
	}
	changed, err := fn(&state)
	if err != nil {
		return err
	}
	if !changed {
		if len(data) == 0 {
			return nil
		}
		return s.syncDirectory(dir)
	}
	return writeState(dir, path, state, s.afterRename, s.syncDirectory)
}

func writeState(dir, path string, state State, afterRename func() error, syncDirectory func(string) error) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	if afterRename != nil {
		if err := afterRename(); err != nil {
			return err
		}
	}
	return syncDirectory(dir)
}

func (s Store) syncDirectory(dir string) error {
	if s.directorySync != nil {
		return s.directorySync(dir)
	}
	return syncDirectory(dir)
}

func syncDirectory(dir string) error {
	parent, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

func newLease() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}
