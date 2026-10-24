package inputarbiter

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

type bindingObserverFunc func(context.Context, string) (Observation, error)

func (f bindingObserverFunc) Observe(ctx context.Context, socket string) (Observation, error) {
	return f(ctx, socket)
}

func bindingFixture(t *testing.T) (string, BindingSpec, Observation) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	idPath := filepath.Join(root, "installation-id")
	if err := os.WriteFile(idPath, []byte("installation-one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(root, "tmux.sock")
	// The fake observer supplies the socket attestation. These tests never
	// contact a live tmux server or write to its socket.
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	spec := BindingSpec{InstallationIDFile: idPath, SocketPath: socket, SessionName: "people", PaneTitle: "human"}
	obs := Observation{
		SocketPath: socket, ServerPID: 321, SocketDev: 5, SocketIno: 10,
		Panes: []ObservedPane{{ID: "%7", SessionName: "people", Title: "human"}},
	}
	return root, spec, obs
}

func TestConfiguredBindingOptInWithholdsAndRetryRecoversOwner(t *testing.T) {
	root, spec, obs := bindingFixture(t)
	var observations int
	observer := bindingObserverFunc(func(context.Context, string) (Observation, error) {
		observations++
		return obs, nil
	})
	conf := &config.Config{HumanPaneBinding: &config.HumanPaneBindingConfig{
		Enabled: true, InstallationIDFile: spec.InstallationIDFile, SocketPath: spec.SocketPath,
		SessionName: spec.SessionName, PaneTitle: spec.PaneTitle,
	}}
	nodes := map[string]discovery.NodeInfo{"people:human": {PaneID: "%7"}, "people:worker": {PaneID: "%8"}}
	if err := withholdConfiguredPane(context.Background(), nil, nodes, observer); err != nil {
		t.Fatal(err)
	}
	if observations != 0 || len(nodes) != 2 {
		t.Fatalf("legacy path changed: calls=%d nodes=%v", observations, nodes)
	}
	if err := withholdConfiguredPane(context.Background(), conf, nodes, observer); err != nil {
		t.Fatal(err)
	}
	if _, exists := nodes["people:human"]; exists || len(nodes) != 1 || observations != 3 {
		t.Fatalf("opted-in pane was published: calls=%d nodes=%v", observations, nodes)
	}
	key := ServerKey{InstallationID: "installation-one", SocketPath: spec.SocketPath}
	store := Store{Root: filepath.Join(root, "inputarbiter")}
	first, err := store.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	if first.Owner.ConversationID == "" || first.Owner.Epoch != 1 || first.Owner.LeaseID == "" {
		t.Fatalf("incomplete owner: %+v", first.Owner)
	}
	if err := withholdConfiguredPane(context.Background(), conf, nodes, observer); err != nil {
		t.Fatal(err)
	}
	second, err := store.Read(key)
	if err != nil {
		t.Fatal(err)
	}
	if second.Owner != first.Owner {
		t.Fatalf("retry changed owner: %+v vs %+v", second.Owner, first.Owner)
	}
}

func TestBindingRawCandidatesFailClosed(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Observation)
		want error
	}{
		{"missing", func(o *Observation) { o.Panes = nil }, ErrBindingMissing},
		{"duplicate", func(o *Observation) {
			o.Panes = append(o.Panes, ObservedPane{ID: "%8", SessionName: "people", Title: "human"})
		}, ErrBindingAmbiguous},
		{"malformed-match", func(o *Observation) { o.Panes[0].ID = "bad" }, ErrBindingInvalid},
		{"malformed-other", func(o *Observation) {
			o.Panes = append(o.Panes, ObservedPane{ID: "bad", SessionName: "other", Title: "worker"})
		}, ErrBindingInvalid},
		{"foreign-socket", func(o *Observation) { o.SocketPath += ".foreign" }, ErrBindingInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, spec, obs := bindingFixture(t)
			tc.edit(&obs)
			conf := &config.Config{HumanPaneBinding: &config.HumanPaneBindingConfig{
				Enabled: true, InstallationIDFile: spec.InstallationIDFile, SocketPath: spec.SocketPath,
				SessionName: spec.SessionName, PaneTitle: spec.PaneTitle,
			}}
			nodes := map[string]discovery.NodeInfo{"people:human": {PaneID: "%7"}}
			err := withholdConfiguredPane(context.Background(), conf, nodes, bindingObserverFunc(func(context.Context, string) (Observation, error) { return obs, nil }))
			if !errors.Is(err, tc.want) || len(nodes) != 0 {
				t.Fatalf("err=%v nodes=%v", err, nodes)
			}
		})
	}
}

func TestBindingRejectsStaleBeforeAndAfterClaim(t *testing.T) {
	for _, staleAt := range []int{2, 3} {
		t.Run(string(rune('0'+staleAt)), func(t *testing.T) {
			root, spec, obs := bindingFixture(t)
			calls := 0
			observer := bindingObserverFunc(func(context.Context, string) (Observation, error) {
				calls++
				current := obs
				if calls == staleAt {
					current.ServerPID++
				}
				return current, nil
			})
			nodes := map[string]discovery.NodeInfo{"people:human": {PaneID: "%7"}}
			conf := &config.Config{HumanPaneBinding: &config.HumanPaneBindingConfig{
				Enabled: true, InstallationIDFile: spec.InstallationIDFile, SocketPath: spec.SocketPath,
				SessionName: spec.SessionName, PaneTitle: spec.PaneTitle,
			}}
			if err := withholdConfiguredPane(context.Background(), conf, nodes, observer); !errors.Is(err, ErrBindingStale) {
				t.Fatalf("stale observation %d: %v", staleAt, err)
			}
			if len(nodes) != 0 {
				t.Fatalf("stale pane published: %v", nodes)
			}
			state, err := (Store{Root: filepath.Join(root, "inputarbiter")}).Read(ServerKey{InstallationID: "installation-one", SocketPath: spec.SocketPath})
			if staleAt == 2 && (err != nil || state.Owner.Epoch != 0) {
				t.Fatalf("pre-claim stale observation committed: %+v", state)
			}
			if staleAt == 3 && (err != nil || state.Owner.Epoch != 1) {
				t.Fatalf("post-claim stale observation should leave unpublished owner: %+v %v", state, err)
			}
		})
	}
}

func TestBindingDistinctSocketsAndChangedIncumbent(t *testing.T) {
	root, spec, obs := bindingFixture(t)
	store := Store{Root: root}
	bind := func(s BindingSpec, o Observation) (Binding, error) {
		return (Binder{Store: store, Observer: bindingObserverFunc(func(context.Context, string) (Observation, error) { return o, nil })}).Bind(context.Background(), s)
	}
	first, err := bind(spec, obs)
	if err != nil {
		t.Fatal(err)
	}
	other := spec
	other.SocketPath = filepath.Join(root, "other.sock")
	if err := os.WriteFile(other.SocketPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	otherObs := obs
	otherObs.SocketPath = other.SocketPath
	otherObs.SocketIno++
	second, err := bind(other, otherObs)
	if err != nil {
		t.Fatal(err)
	}
	if first.Key == second.Key || first.Owner.LeaseID == second.Owner.LeaseID {
		t.Fatalf("distinct socket shared owner: %+v %+v", first, second)
	}
	restarted := obs
	restarted.ServerPID++
	if _, err := bind(spec, restarted); !errors.Is(err, ErrOwned) {
		t.Fatalf("restarted server bypassed rebind: %v", err)
	}
}

func TestConcurrentConfiguredBindRecoversOneClaim(t *testing.T) {
	root, spec, obs := bindingFixture(t)
	binder := Binder{Store: Store{Root: root}, Observer: bindingObserverFunc(func(context.Context, string) (Observation, error) { return obs, nil })}
	var wg sync.WaitGroup
	owners := make(chan Owner, 12)
	for i := 0; i < cap(owners); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			binding, err := binder.Bind(context.Background(), spec)
			if err != nil {
				t.Errorf("bind: %v", err)
				return
			}
			owners <- binding.Owner
		}()
	}
	wg.Wait()
	close(owners)
	var first Owner
	count := 0
	for owner := range owners {
		if count == 0 {
			first = owner
		} else if owner != first {
			t.Fatalf("concurrent bind produced another owner: %+v vs %+v", owner, first)
		}
		count++
	}
	if count != 12 || first.Epoch != 1 {
		t.Fatalf("owners=%d first=%+v", count, first)
	}
}

func TestIncompleteOptInWithholdsAllLegacyRoutes(t *testing.T) {
	_, spec, obs := bindingFixture(t)
	nodes := map[string]discovery.NodeInfo{"people:human": {PaneID: "%7"}, "people:worker": {PaneID: "%8"}}
	conf := &config.Config{HumanPaneBinding: &config.HumanPaneBindingConfig{
		Enabled: true, InstallationIDFile: spec.InstallationIDFile, SocketPath: spec.SocketPath,
		SessionName: spec.SessionName,
	}}
	calls := 0
	err := withholdConfiguredPane(context.Background(), conf, nodes, bindingObserverFunc(func(context.Context, string) (Observation, error) {
		calls++
		return obs, nil
	}))
	if !errors.Is(err, ErrBindingInvalid) || len(nodes) != 0 || calls != 0 {
		t.Fatalf("incomplete opt-in: err=%v nodes=%v calls=%d", err, nodes, calls)
	}
}

func TestInstallationIDRecordMustBeOwnedRegularFile(t *testing.T) {
	_, spec, _ := bindingFixture(t)
	if err := os.Chmod(spec.InstallationIDFile, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateBindingSpec(spec); !errors.Is(err, ErrBindingInvalid) {
		t.Fatalf("writable installation record accepted: %v", err)
	}
	if err := os.Chmod(spec.InstallationIDFile, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(spec.InstallationIDFile), "installation-link")
	if err := os.Symlink(spec.InstallationIDFile, link); err != nil {
		t.Fatal(err)
	}
	spec.InstallationIDFile = link
	if _, _, err := validateBindingSpec(spec); !errors.Is(err, ErrBindingInvalid) {
		t.Fatalf("symlink installation record accepted: %v", err)
	}
}

func TestPaneRecordInjectionIsRejectedBeforeClaim(t *testing.T) {
	tests := []struct {
		name    string
		rawIDs  string
		rawPane string
	}{
		{"forged-list-row", "%7\n%999\tpeople\thuman\n", "%7\tpeople\tother\n"},
		{"forged-detail-row", "%7\n", "%7\tpeople\tother\n%999\tpeople\thuman\n"},
		{"tab-in-title", "%7\n", "%7\tpeople\thuman\textra\n"},
		{"newline-in-title", "%7\n", "%7\tpeople\thuman\nextra\n"},
		{"changed-target-id", "%7\n", "%999\tpeople\thuman\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, spec, obs := bindingFixture(t)
			conf := &config.Config{HumanPaneBinding: &config.HumanPaneBindingConfig{
				Enabled: true, InstallationIDFile: spec.InstallationIDFile, SocketPath: spec.SocketPath,
				SessionName: spec.SessionName, PaneTitle: spec.PaneTitle,
			}}
			nodes := map[string]discovery.NodeInfo{"people:human": {PaneID: "%7"}}
			observer := bindingObserverFunc(func(context.Context, string) (Observation, error) {
				ids, err := parsePaneIDs([]byte(tc.rawIDs))
				if err != nil {
					return Observation{}, err
				}
				if len(ids) != 1 {
					return Observation{}, ErrBindingInvalid
				}
				pane, err := parsePaneObservation([]byte(tc.rawPane), ids[0])
				if err != nil {
					return Observation{}, err
				}
				obs.Panes = []ObservedPane{pane}
				return obs, nil
			})
			if err := withholdConfiguredPane(context.Background(), conf, nodes, observer); !errors.Is(err, ErrBindingInvalid) {
				t.Fatalf("injected record accepted: %v", err)
			}
			if len(nodes) != 0 {
				t.Fatalf("injected record published route: %v", nodes)
			}
			state, err := (Store{Root: filepath.Join(root, "inputarbiter")}).Read(ServerKey{InstallationID: "installation-one", SocketPath: spec.SocketPath})
			if err != nil || state.Owner.Epoch != 0 {
				t.Fatalf("injected record committed Claim: %+v %v", state, err)
			}
		})
	}
}

func TestPaneRecordParsersAcceptOneValidPane(t *testing.T) {
	ids, err := parsePaneIDs([]byte("%7\n"))
	if err != nil || len(ids) != 1 || ids[0] != "%7" {
		t.Fatalf("valid ID list: %v %v", ids, err)
	}
	pane, err := parsePaneObservation([]byte("%7\tpeople\thuman\n"), "%7")
	if err != nil || pane != (ObservedPane{ID: "%7", SessionName: "people", Title: "human"}) {
		t.Fatalf("valid pane: %+v %v", pane, err)
	}
}
