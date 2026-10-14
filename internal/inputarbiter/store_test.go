package inputarbiter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func fixture() (Store, ServerKey, Owner) {
	return Store{}, ServerKey{InstallationID: "host-a", SocketPath: "/tmp/tmux-a/socket"}, Owner{ServerIncarnation: "server-1", PaneID: "%3", PaneIncarnation: "pane-1", ConversationID: "conversation-1", Designation: "human"}
}

func TestConcurrentClaimHasOneOwner(t *testing.T) {
	store, key, owner := fixture()
	store.Root = t.TempDir()
	var wg sync.WaitGroup
	results := make(chan error, 24)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			candidate := owner
			if n%2 == 1 {
				candidate.PaneID = "%4"
			}
			_, err := store.Claim(key, candidate)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	accepted, rejected := 0, 0
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrOwned):
			rejected++
		default:
			t.Fatal(err)
		}
	}
	if accepted != 12 || rejected != 12 {
		t.Fatalf("accepted=%d rejected=%d", accepted, rejected)
	}
	state, err := store.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	if state.Owner.Epoch != 1 || state.Owner.LeaseID == "" {
		t.Fatalf("bad owner fence: %+v", state.Owner)
	}
}

func TestDistinctServersMayUseSameDesignation(t *testing.T) {
	store, key, owner := fixture()
	store.Root = t.TempDir()
	first, err := store.Claim(key, owner)
	if err != nil {
		t.Fatal(err)
	}
	other := key
	other.SocketPath = filepath.Join("/tmp", "tmux-b", "socket")
	second, err := store.Claim(other, owner)
	if err != nil {
		t.Fatal(err)
	}
	if first.LeaseID == second.LeaseID {
		t.Fatal("distinct servers share lease")
	}
}

func TestRebindFencesOldServerAndPane(t *testing.T) {
	store, key, owner := fixture()
	store.Root = t.TempDir()
	old, err := store.Claim(key, owner)
	if err != nil {
		t.Fatal(err)
	}
	replacement := owner
	replacement.ServerIncarnation = "server-2"
	replacement.PaneID = "%9"
	replacement.PaneIncarnation = "pane-2"
	replacement.ConversationID = "conversation-2"
	if _, err := store.Claim(key, replacement); !errors.Is(err, ErrOwned) {
		t.Fatalf("claim: %v", err)
	}
	current, err := store.Rebind(key, "rebind-1", old, replacement)
	if err != nil {
		t.Fatal(err)
	}
	if current.Epoch != 2 || current.LeaseID == old.LeaseID {
		t.Fatalf("rebind: %+v", current)
	}
	if _, err := store.Accept(key, old, OriginDirect, "direct-stale", "digest"); !errors.Is(err, ErrStale) {
		t.Fatalf("stale accept: %v", err)
	}
	if _, err := store.Rebind(key, "rebind-stale", old, replacement); !errors.Is(err, ErrStale) {
		t.Fatalf("stale rebind: %v", err)
	}
	state, err := store.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	if state.Owner != current || len(state.Inputs) != 0 {
		t.Fatalf("stale mutation: %+v", state)
	}
}

func TestConcurrentAcceptanceReplayAndPendingMail(t *testing.T) {
	store, key, owner := fixture()
	store.Root = t.TempDir()
	fence, err := store.Claim(key, owner)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			var err error
			if n%2 == 0 {
				_, err = store.Accept(key, fence, OriginMail, "mail-1", "digest-mail")
			} else {
				_, err = store.Accept(key, fence, OriginDirect, "direct-"+strconv.Itoa(n), "digest-direct")
			}
			if err != nil {
				t.Errorf("accept %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()
	// A new Store instance models replay after the accepting process exits.
	restarted := Store{Root: store.Root}
	mail, err := restarted.Accept(key, fence, OriginMail, "mail-1", "digest-mail")
	if err != nil {
		t.Fatal(err)
	}
	if !mail.Pending || mail.Origin != OriginMail {
		t.Fatalf("mail was submitted or relabeled: %+v", mail)
	}
	if _, err := restarted.Accept(key, fence, OriginMail, "mail-1", "different"); !errors.Is(err, ErrDuplicateConflict) {
		t.Fatalf("conflicting replay: %v", err)
	}
	state, err := restarted.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Inputs) != 21 {
		t.Fatalf("got %d inputs", len(state.Inputs))
	}
	mailCount := 0
	for i, input := range state.Inputs {
		if input.InputEventID != uint64(i+1) || input.ConversationID != fence.ConversationID {
			t.Fatalf("ordering: %+v", input)
		}
		if input.Origin == OriginMail {
			mailCount++
			if !input.Pending {
				t.Fatal("mail lost pending state")
			}
		}
		if !input.Pending {
			t.Fatal("accepted input was marked submitted without runtime acknowledgement")
		}
	}
	if mailCount != 1 {
		t.Fatalf("mail count=%d", mailCount)
	}
}

func TestPendingDirectSurvivesReopenAndRebind(t *testing.T) {
	store, key, owner := fixture()
	store.Root = t.TempDir()
	fence, err := store.Claim(key, owner)
	if err != nil {
		t.Fatal(err)
	}
	direct, err := store.Accept(key, fence, OriginDirect, "direct-1", "digest")
	if err != nil {
		t.Fatal(err)
	}
	if !direct.Pending {
		t.Fatal("direct input prematurely completed")
	}
	reopened := Store{Root: store.Root}
	replayed, err := reopened.Accept(key, fence, OriginDirect, "direct-1", "digest")
	if err != nil {
		t.Fatal(err)
	}
	if replayed != direct {
		t.Fatalf("duplicate direct input: %+v vs %+v", replayed, direct)
	}
	replacement := owner
	replacement.PaneIncarnation = "pane-2"
	replacement.ConversationID = "conversation-2"
	if _, err := reopened.Rebind(key, "rebind-1", fence, replacement); err != nil {
		t.Fatal(err)
	}
	state, err := reopened.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Inputs) != 1 || state.Inputs[0] != direct || !state.Inputs[0].Pending {
		t.Fatalf("lost unknown input: %+v", state.Inputs)
	}
}

func TestMailIDIsScopedToConversation(t *testing.T) {
	store, key, owner := fixture()
	store.Root = t.TempDir()
	firstOwner, err := store.Claim(key, owner)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Accept(key, firstOwner, OriginMail, "mail-1", "digest-a")
	if err != nil {
		t.Fatal(err)
	}
	replacement := owner
	replacement.PaneIncarnation = "pane-2"
	replacement.ConversationID = "conversation-2"
	secondOwner, err := store.Rebind(key, "rebind-1", firstOwner, replacement)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Accept(key, secondOwner, OriginMail, "mail-1", "digest-b")
	if err != nil {
		t.Fatal(err)
	}
	if first.InputEventID == second.InputEventID || second.ConversationID != secondOwner.ConversationID {
		t.Fatalf("cross-conversation dedupe: %+v %+v", first, second)
	}
	if _, err := store.Accept(key, secondOwner, OriginMail, "mail-1", "digest-c"); !errors.Is(err, ErrDuplicateConflict) {
		t.Fatalf("same-conversation conflict: %v", err)
	}
	replayed, err := store.Accept(key, secondOwner, OriginMail, "mail-1", "digest-b")
	if err != nil || replayed != second {
		t.Fatalf("same-conversation replay: %+v %v", replayed, err)
	}
}

func TestRetryAfterPostRenameErrorDoesNotDuplicateDirectInput(t *testing.T) {
	store, key, owner := fixture()
	store.Root = t.TempDir()
	fence, err := store.Claim(key, owner)
	if err != nil {
		t.Fatal(err)
	}
	uncertain := Store{Root: store.Root, afterRename: func() error { return errors.New("injected directory sync failure") }}
	if _, err := uncertain.Accept(key, fence, OriginDirect, "direct-1", "digest"); err == nil {
		t.Fatal("expected uncertain commit error")
	}
	directorySyncs := 0
	reopened := Store{Root: store.Root, directorySync: func(dir string) error {
		directorySyncs++
		return syncDirectory(dir)
	}}
	input, err := reopened.Accept(key, fence, OriginDirect, "direct-1", "digest")
	if err != nil {
		t.Fatal(err)
	}
	if !input.Pending || input.InputEventID != 1 {
		t.Fatalf("uncertain input: %+v", input)
	}
	if directorySyncs != 1 {
		t.Fatalf("replay missed directory durability barrier: %d", directorySyncs)
	}
	state, err := reopened.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Inputs) != 1 {
		t.Fatalf("duplicate after retry: %+v", state.Inputs)
	}
}

func TestRebindRetryAfterPostRenameErrorReturnsSameLease(t *testing.T) {
	store, key, owner := fixture()
	store.Root = t.TempDir()
	old, err := store.Claim(key, owner)
	if err != nil {
		t.Fatal(err)
	}
	replacement := owner
	replacement.PaneIncarnation = "pane-2"
	replacement.ConversationID = "conversation-2"
	uncertain := Store{Root: store.Root, afterRename: func() error { return errors.New("injected post-rename error") }}
	if _, err := uncertain.Rebind(key, "request-1", old, replacement); err == nil {
		t.Fatal("expected uncertain commit error")
	}
	reopened := Store{Root: store.Root}
	committed, err := reopened.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed.Rebindings) != 1 {
		t.Fatalf("first rebind receipt count: %d", len(committed.Rebindings))
	}
	firstReceipt := committed.Rebindings[0]
	if firstReceipt.RequestID != "request-1" || firstReceipt.From != old || firstReceipt.Proposed != replacement || firstReceipt.Result != committed.Owner {
		t.Fatalf("first rebind receipt: %+v, owner: %+v", firstReceipt, committed.Owner)
	}
	if firstReceipt.Result.Epoch != old.Epoch+1 || firstReceipt.Result.LeaseID == "" || firstReceipt.Result.LeaseID == old.LeaseID {
		t.Fatalf("first rebind fence: %+v", firstReceipt.Result)
	}
	path := statePath(store, key)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	retried, err := reopened.Rebind(key, "request-1", old, replacement)
	if err != nil || retried != firstReceipt.Result {
		t.Fatalf("rebind replay: owner=%+v state=%+v err=%v", retried, committed.Owner, err)
	}
	replayed, err := reopened.Read(key)
	if err != nil || len(replayed.Rebindings) != 1 || replayed.Rebindings[0] != firstReceipt || replayed.Owner != committed.Owner {
		t.Fatalf("rebind replay changed receipt: %+v %v", replayed, err)
	}
	afterReplay, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(afterReplay, before) {
		t.Fatalf("rebind replay changed persisted bytes: %v", err)
	}
	if _, err := reopened.Rebind(key, "request-1", old, owner); !errors.Is(err, ErrDuplicateConflict) {
		t.Fatalf("request ID conflict: %v", err)
	}
	final, err := reopened.Read(key)
	if err != nil || final.Owner != committed.Owner || len(final.Rebindings) != 1 || final.Rebindings[0] != firstReceipt {
		t.Fatalf("conflicting rebind changed receipt: %+v %v", final, err)
	}
	afterConflict, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(afterConflict, before) {
		t.Fatalf("conflicting rebind changed persisted bytes: %v", err)
	}
}

func TestRebindRejectsEpochExhaustionWithoutMutation(t *testing.T) {
	if next, err := nextEpoch(^uint64(0)); next != 0 || !errors.Is(err, ErrEpochExhausted) {
		t.Fatalf("overflow guard: next=%d err=%v", next, err)
	}
	store, key, owner := fixture()
	store.Root = t.TempDir()
	if _, err := store.Claim(key, owner); err != nil {
		t.Fatal(err)
	}
	state, err := store.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	state.Owner.Epoch = ^uint64(0)
	path := statePath(store, key)
	before := writeRawState(t, path, state)
	if _, err := store.Rebind(key, "request-max", state.Owner, owner); err == nil {
		t.Fatal("unanchored max-epoch state accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("exhaustion mutated state: %v", err)
	}
}

func TestCorruptLoadedStateFailsClosed(t *testing.T) {
	cases := map[string]func(*State){
		"owner lease": func(s *State) { s.Owner.LeaseID = "" },
		"owner epoch": func(s *State) { s.Owner.Epoch = 0 },
		"event gap":   func(s *State) { s.Inputs[0].InputEventID = 2 },
		"duplicate source": func(s *State) {
			duplicate := s.Inputs[0]
			duplicate.InputEventID = 2
			s.Inputs = append(s.Inputs, duplicate)
		},
		"unknown origin":   func(s *State) { s.Inputs[0].Origin = "unknown" },
		"missing source":   func(s *State) { s.Inputs[0].DirectSubmissionID = "" },
		"mixed sources":    func(s *State) { s.Inputs[0].MailMessageID = "mail-1" },
		"unanchored owner": func(s *State) { s.Owner.Epoch = 2 },
		"orphan input":     func(s *State) { s.Inputs[0].ConversationID = "never-owned" },
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			store, key, owner := fixture()
			store.Root = t.TempDir()
			fence, err := store.Claim(key, owner)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.Accept(key, fence, OriginDirect, "direct-1", "digest"); err != nil {
				t.Fatal(err)
			}
			state, err := store.Read(key)
			if err != nil {
				t.Fatal(err)
			}
			corrupt(&state)
			path := statePath(store, key)
			before := writeRawState(t, path, state)
			if _, err := store.Read(key); err == nil {
				t.Fatal("read accepted corrupt state")
			}
			if _, err := store.Claim(key, owner); err == nil {
				t.Fatal("claim accepted corrupt state")
			}
			if _, err := store.Accept(key, fence, OriginDirect, "direct-2", "digest"); err == nil {
				t.Fatal("accept accepted corrupt state")
			}
			if _, err := store.Rebind(key, "request-1", fence, owner); err == nil {
				t.Fatal("rebind accepted corrupt state")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("corrupt state mutated: %v", err)
			}
		})
	}
}

func TestClaimReplayCompletesDirectorySync(t *testing.T) {
	store, key, owner := fixture()
	store.Root = t.TempDir()
	uncertain := Store{Root: store.Root, afterRename: func() error { return errors.New("injected post-rename error") }}
	if _, err := uncertain.Claim(key, owner); err == nil {
		t.Fatal("expected uncertain commit error")
	}
	directorySyncs := 0
	reopened := Store{Root: store.Root, directorySync: func(dir string) error {
		directorySyncs++
		return syncDirectory(dir)
	}}
	claimed, err := reopened.Claim(key, owner)
	if err != nil || claimed.Epoch != 1 || directorySyncs != 1 {
		t.Fatalf("claim replay: %+v, syncs=%d, err=%v", claimed, directorySyncs, err)
	}
}

func TestExistingEmptyOrMissingKeyStateFailsClosed(t *testing.T) {
	store, key, owner := fixture()
	store.Root = t.TempDir()
	// ENOENT must still create the keyed Claim genesis state.
	fence, err := store.Claim(key, owner)
	if err != nil || fence.Epoch != 1 {
		t.Fatalf("ENOENT claim: %+v %v", fence, err)
	}
	state, err := store.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	missingKey := state
	missingKey.Key = ServerKey{}
	missingKeyJSON, err := json.Marshal(missingKey)
	if err != nil {
		t.Fatal(err)
	}
	keyOnlyJSON, err := json.Marshal(State{Key: key})
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{"empty object": []byte(`{}`), "missing key": missingKeyJSON, "key only": keyOnlyJSON}
	path := statePath(store, key)
	for name, before := range cases {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, before, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Read(key); err == nil {
				t.Fatal("read accepted existing invalid state")
			}
			if _, err := store.Claim(key, owner); err == nil {
				t.Fatal("claim accepted existing invalid state")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("invalid state mutated: %v", err)
			}
		})
	}
}

func TestRebindHistoryAnchorsOwnedConversations(t *testing.T) {
	store, key, owner := fixture()
	store.Root = t.TempDir()
	first, err := store.Claim(key, owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Accept(key, first, OriginDirect, "direct-1", "digest-1"); err != nil {
		t.Fatal(err)
	}
	secondProposal := owner
	secondProposal.PaneIncarnation = "pane-2"
	secondProposal.ConversationID = "conversation-2"
	second, err := store.Rebind(key, "request-1", first, secondProposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Accept(key, second, OriginMail, "mail-2", "digest-2"); err != nil {
		t.Fatal(err)
	}
	thirdProposal := owner
	thirdProposal.PaneIncarnation = "pane-3"
	thirdProposal.ConversationID = "conversation-3"
	third, err := store.Rebind(key, "request-2", second, thirdProposal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Accept(key, third, OriginDirect, "direct-3", "digest-3"); err != nil {
		t.Fatal(err)
	}
	valid, err := store.Read(key)
	if err != nil || len(valid.Rebindings) != 2 || len(valid.Inputs) != 3 {
		t.Fatalf("valid history: %+v %v", valid, err)
	}
	path := statePath(store, key)
	invalid := valid
	invalid.Rebindings = append([]RebindReceipt(nil), valid.Rebindings...)
	invalid.Rebindings[0].From.Epoch = 2
	before := writeRawState(t, path, invalid)
	if _, err := store.Read(key); err == nil {
		t.Fatal("unanchored receipt chain accepted")
	}
	if _, err := store.Accept(key, third, OriginDirect, "direct-4", "digest-4"); err == nil {
		t.Fatal("accept passed unanchored receipt chain")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("unanchored chain mutated: %v", err)
	}
}

func statePath(store Store, key ServerKey) string {
	encoded, _ := json.Marshal(key)
	hash := sha256.Sum256(encoded)
	return filepath.Join(store.Root, hex.EncodeToString(hash[:]), "state.json")
}

func writeRawState(t *testing.T, path string, state State) []byte {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}
