package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type tmuxFixture struct {
	socket string
	home   string
	repo   string
	pane   string
}

func newTmuxFixture(t *testing.T) tmuxFixture {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	// Keep the Unix socket below macOS's path-length limit. This server has a
	// unique private socket and contains only owned sleep fixtures, no models.
	dir, err := os.MkdirTemp("/tmp", "pm937-")
	if err != nil {
		t.Fatal(err)
	}
	f := tmuxFixture{socket: filepath.Join(dir, "s"), home: t.TempDir()}
	f.repo = filepath.Join(f.home, "repo")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, _ = tmuxOutput(ctx, f.socket, "kill-server")
		if err := os.RemoveAll(dir); err != nil {
			t.Error(err)
		}
	})
	if err := os.Mkdir(f.repo, 0o700); err != nil {
		t.Fatal(err)
	}
	f.pane = f.run(t, "-f", "/dev/null", "new-session", "-d", "-s", "one",
		"-c", f.repo, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", f.pane, "-T", "worker")
	t.Logf("owned fixture identity: %q", f.run(t, "display-message", "-p", "-t", f.pane,
		"#{socket_path}\t#{pid}\t#{start_time}\t#{session_id}\t#{pane_id}\t#{session_path}\t#{pane_dead}"))
	return f
}

func (f tmuxFixture) run(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := tmuxOutput(ctx, f.socket, args...)
	if err != nil {
		t.Fatalf("owned tmux fixture %v: %v", args, err)
	}
	return strings.TrimSpace(out)
}

func (f tmuxFixture) adapter() Tmux {
	return Tmux{Socket: f.socket, Pane: f.pane, Home: f.home, Roles: []string{"worker", "approver"}}
}

func TestTmuxSendLeavesPendingRecipientInputUntouched(t *testing.T) {
	f := newTmuxFixture(t)
	peer := f.run(t, "new-window", "-d", "-t", "one", "-n", "peer", "-c", f.repo,
		"-P", "-F", "#{pane_id}", "exec sh -i")
	f.run(t, "select-pane", "-t", peer, "-T", "peer")
	adapter := f.adapter()
	adapter.Roles = []string{"worker", "peer"}
	marker := filepath.Join(filepath.Dir(f.socket), "submitted")
	pending := "touch " + marker + " "
	f.run(t, "send-keys", "-l", "-t", peer, pending)
	deadline := time.Now().Add(2 * time.Second)
	var before string
	for time.Now().Before(deadline) {
		before = f.run(t, "capture-pane", "-p", "-t", peer)
		if strings.Contains(before, "touch "+marker) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(before, "touch "+marker) {
		t.Fatal("owned recipient fixture never showed pending input")
	}
	m := mailFixture(t)
	sent, err := m.SendWithState(context.Background(), adapter, "peer", "body", "", "", false, []string{"peer"})
	if err != nil {
		t.Fatal(err)
	}
	notice := NotifyCommitted(context.Background(), adapter, sent.Message)
	if notice.Status != "unavailable" || !errors.Is(adapter.NotifyPane(context.Background(), Binding{}, ""), ErrPaneNoticeUnsupported) {
		t.Fatalf("native pane notification was not disabled: %+v", notice)
	}
	after := f.run(t, "capture-pane", "-p", "-t", peer)
	if after != before {
		t.Fatalf("ordinary send changed pending recipient input: before %q, after %q", before, after)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ordinary send submitted pending command: %v", err)
	}
	if _, err := os.Stat(mailPath(m, "peer", "inbox", sent.Message.ID)); err != nil {
		t.Fatalf("safe notification lost committed mail: %v", err)
	}
}

func TestTmuxSnapshotUsesNativeIdentityAndStableSessionRoot(t *testing.T) {
	f := newTmuxFixture(t)
	adapter := f.adapter()
	first, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Root != "~/repo" || first.Caller.Role != "worker" ||
		first.Caller.Owner.Pane != f.pane || len(first.Bindings) != 1 {
		t.Fatalf("unexpected snapshot: %#v", first)
	}
	if !filepath.IsAbs(first.Caller.Owner.Server) ||
		len(strings.Split(first.Caller.Owner.Incarnation, ":")) != 5 {
		t.Fatalf("missing server incarnation: %#v", first.Caller.Owner)
	}
	other := filepath.Join(f.home, "elsewhere")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	f.run(t, "respawn-pane", "-k", "-t", f.pane, "-c", other, "exec sleep 120")
	if path := f.run(t, "display-message", "-p", "-t", f.pane, "#{pane_current_path}"); path == f.repo {
		t.Fatal("fixture did not change pane directory")
	}
	second, err := adapter.Snapshot(context.Background())
	if err != nil || second.Root != first.Root || second.Caller.Role != first.Caller.Role || second.Caller.Owner != first.Caller.Owner || second.Caller.PanePID == first.Caller.PanePID {
		t.Fatalf("pane cd redirected workspace: %#v %v", second, err)
	}
	adapter.Home = t.TempDir()
	if _, err := adapter.Snapshot(context.Background()); err == nil {
		t.Fatal("outside-HOME session root accepted")
	}
}

func TestTmuxNamedPeerRequiresLiveIndependentSessionAndRole(t *testing.T) {
	f := newTmuxFixture(t)
	childRoot := filepath.Join(f.home, "child")
	if err := os.Mkdir(childRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	childPane := f.run(t, "new-session", "-d", "-s", "child", "-c", childRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", childPane, "-T", "worker")
	adapter := f.adapter()
	parent, err := adapter.NamedCaller(context.Background())
	if err != nil || parent.Name() != "one" {
		t.Fatalf("named caller: %+v %v", parent, err)
	}
	child, err := adapter.FindNamedPeer(context.Background(), "child", "worker")
	if err != nil || child.Name() != "child" {
		t.Fatalf("named peer: %+v %v", child, err)
	}
	parentSnapshot, err := parent.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	childSnapshot, err := child.Snapshot(context.Background())
	if err != nil || childSnapshot.Root != "~/child" ||
		childSnapshot.Caller.Owner.Session == parentSnapshot.Caller.Owner.Session ||
		childSnapshot.Caller.Owner.Incarnation != parentSnapshot.Caller.Owner.Incarnation {
		t.Fatalf("independent peer identity: %+v %v", childSnapshot, err)
	}
	if _, err := adapter.FindNamedPeer(context.Background(), "one", "worker"); err == nil {
		t.Fatal("caller session selected as a peer")
	}
	if _, err := adapter.FindNamedPeer(context.Background(), "child", "approver"); err == nil {
		t.Fatal("absent peer role selected")
	}
	f.run(t, "rename-session", "-t", "child", "renamed")
	if _, err := child.Snapshot(context.Background()); !errors.Is(err, ErrUnknown) {
		t.Fatalf("renamed peer still authorized: %v", err)
	}
	if _, err := adapter.FindNamedPeer(context.Background(), "child", "worker"); err == nil {
		t.Fatal("old peer name still selected")
	}
}

func TestTmuxCrossSessionMailSendPopReplyAndState(t *testing.T) {
	f := newTmuxFixture(t)
	childRoot := filepath.Join(f.home, "child")
	if err := os.Mkdir(childRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	childPane := f.run(t, "new-session", "-d", "-s", "child", "-c", childRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", childPane, "-T", "worker")
	ctx := context.Background()
	adapter := f.adapter()
	parent, err := adapter.NamedCaller(ctx)
	if err != nil {
		t.Fatal(err)
	}
	child, err := adapter.FindNamedPeer(ctx, "child", "worker")
	if err != nil {
		t.Fatal(err)
	}
	m := Mail{Store: Store{StateRoot: filepath.Join(f.home, "state")}}
	request, err := m.SendCrossWithReply(ctx, parent, child, "question", "", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if request.FromSession != "one" || request.ToSession != "child" ||
		request.From != "worker" || request.To != "worker" || !request.NeedsReply {
		t.Fatalf("wrong cross-session request identity: %+v", request)
	}
	parentState := m.State(ctx, parent, nil)
	childState := m.State(ctx, child, nil)
	if !parentState.Known || len(parentState.Outbound) != 1 || parentState.Outbound[0].Peer != "child:worker" ||
		!childState.Known || childState.Unread == nil || *childState.Unread != 1 ||
		len(childState.Inbound) != 1 || childState.Inbound[0].Peer != "one:worker" {
		t.Fatalf("cross-session request state: parent=%+v child=%+v", parentState, childState)
	}
	claimed, err := m.Claim(ctx, child, request.ID)
	if err != nil || claimed == nil || claimed.Message != request {
		t.Fatalf("child POP: %+v %v", claimed, err)
	}
	if _, err := os.Stat(claimed.Path); err != nil {
		t.Fatalf("child read archive absent: %v", err)
	}
	childState = m.State(ctx, child, nil)
	if !childState.Known || len(childState.Inbound) != 1 || !childState.Inbound[0].Claimed {
		t.Fatalf("child claimed obligation: %+v", childState)
	}
	reply, err := m.SendCrossWithReply(ctx, child, parent, "answer", request.ID, request.InputRequestID, false)
	if err != nil {
		t.Fatal(err)
	}
	if !exactAnswer(request, reply) || reply.FromSession != "child" || reply.ToSession != "one" {
		t.Fatalf("cross-session reply correlation: %+v", reply)
	}
	parentState = m.State(ctx, parent, nil)
	childState = m.State(ctx, child, nil)
	if !parentState.Known || parentState.Unread == nil || *parentState.Unread != 1 ||
		len(parentState.Outbound) != 0 || !childState.Known || len(childState.Inbound) != 0 {
		t.Fatalf("cross-session reply state: parent=%+v child=%+v", parentState, childState)
	}
	answer, err := m.Claim(ctx, parent, reply.ID)
	if err != nil || answer == nil || answer.Message != reply {
		t.Fatalf("parent POP: %+v %v", answer, err)
	}
	for _, item := range []struct {
		root string
		id   string
	}{
		{parentState.Workspace, request.ID},
		{childState.Workspace, reply.ID},
	} {
		name, err := Name(item.root)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(m.Store.StateRoot, name, "agents", "worker", "sent", item.id+".md")
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("sender sent copy %s: %v", path, err)
		}
	}
}

func TestTmuxCrossSessionRecoveryAfterRecipientClaim(t *testing.T) {
	f := newTmuxFixture(t)
	childRoot := filepath.Join(f.home, "child")
	if err := os.Mkdir(childRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	childPane := f.run(t, "new-session", "-d", "-s", "child", "-c", childRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", childPane, "-T", "worker")
	ctx := context.Background()
	adapter := f.adapter()
	parent, err := adapter.NamedCaller(ctx)
	if err != nil {
		t.Fatal(err)
	}
	child, err := adapter.FindNamedPeer(ctx, "child", "worker")
	if err != nil {
		t.Fatal(err)
	}
	m := Mail{Store: Store{StateRoot: filepath.Join(f.home, "state")}}
	interrupted := m
	interrupted.afterStep = func(step string) error {
		if step == "cross-inbox" {
			return errors.New("injected interruption after recipient publication")
		}
		return nil
	}
	first, err := interrupted.SendCrossWithReply(ctx, parent, child, "first", "", "", false)
	if err == nil || first.ID == "" {
		t.Fatalf("expected interrupted first publication with candidate ID: %+v %v", first, err)
	}
	claimed, err := m.Claim(ctx, child, first.ID)
	if err != nil || claimed == nil || claimed.Message != first {
		t.Fatalf("recipient could not claim interrupted publication: %+v %v", claimed, err)
	}
	if state := m.State(ctx, parent, nil); state.Known {
		t.Fatalf("pending sender intent reported as settled: %+v", state)
	}
	second, err := m.SendCrossWithReply(ctx, parent, child, "second", "", "", false)
	if err != nil || second.ID == first.ID {
		t.Fatalf("recovery and next publication: %+v %v", second, err)
	}
	if state := m.State(ctx, parent, nil); !state.Known {
		t.Fatalf("source state did not recover: %+v", state)
	}
	parentSnapshot, err := parent.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	parentName, err := Name(parentSnapshot.Root)
	if err != nil {
		t.Fatal(err)
	}
	firstSent := filepath.Join(m.Store.StateRoot, parentName, "agents", "worker", "sent", first.ID+".md")
	if _, err := os.Stat(firstSent); err != nil {
		t.Fatalf("recovered sender copy absent: %v", err)
	}
	childSnapshot, err := child.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	childName, err := Name(childSnapshot.Root)
	if err != nil {
		t.Fatal(err)
	}
	firstInbox := filepath.Join(m.Store.StateRoot, childName, "agents", "worker", "inbox", first.ID+".md")
	if _, err := os.Stat(firstInbox); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovered delivery recreated an already claimed inbox message: %v", err)
	}
}

func TestTmuxInterruptedCrossSendDoesNotBlockOtherRoutes(t *testing.T) {
	for _, step := range []string{"cross-intent", "cross-inbox"} {
		t.Run(step, func(t *testing.T) {
			f := newTmuxFixture(t)
			localPane := f.run(t, "new-window", "-d", "-t", "one", "-n", "local", "-c", f.repo,
				"-P", "-F", "#{pane_id}", "exec sleep 120")
			f.run(t, "select-pane", "-t", localPane, "-T", "approver")
			for _, name := range []string{"child", "sibling"} {
				root := filepath.Join(f.home, name)
				if err := os.Mkdir(root, 0o700); err != nil {
					t.Fatal(err)
				}
				pane := f.run(t, "new-session", "-d", "-s", name, "-c", root,
					"-P", "-F", "#{pane_id}", "exec sleep 120")
				f.run(t, "select-pane", "-t", pane, "-T", "worker")
			}
			ctx := context.Background()
			adapter := f.adapter()
			parent, err := adapter.NamedCaller(ctx)
			if err != nil {
				t.Fatal(err)
			}
			child, err := adapter.FindNamedPeer(ctx, "child", "worker")
			if err != nil {
				t.Fatal(err)
			}
			sibling, err := adapter.FindNamedPeer(ctx, "sibling", "worker")
			if err != nil {
				t.Fatal(err)
			}
			local := adapter
			local.Pane = localPane
			m := Mail{Store: Store{StateRoot: filepath.Join(f.home, "state")}}
			request, err := m.SendCrossWithReply(ctx, parent, child, "question", "", "", true)
			if err != nil {
				t.Fatal(err)
			}
			if claimed, err := m.Claim(ctx, child, request.ID); err != nil || claimed == nil {
				t.Fatalf("seed request POP: %+v %v", claimed, err)
			}

			interrupted := m
			interrupted.afterStep = func(current string) error {
				if current == step {
					return errors.New("injected cross-session interruption")
				}
				return nil
			}
			pending, err := interrupted.SendCrossWithReply(ctx, parent, child, "pending", "", "", false)
			if err == nil || pending.ID == "" {
				t.Fatalf("expected interrupted send: %+v %v", pending, err)
			}
			parentSnapshot, err := parent.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			parentName, err := Name(parentSnapshot.Root)
			if err != nil {
				t.Fatal(err)
			}
			intentPath := filepath.Join(m.Store.StateRoot, parentName, "transactions", "cross_send-"+pending.ID+".json")
			intentBefore, err := os.ReadFile(intentPath)
			if err != nil {
				t.Fatal(err)
			}
			if state := m.State(ctx, parent, nil); state.Known {
				t.Fatalf("pending source intent reported settled: %+v", state)
			}
			if step == "cross-inbox" {
				if claimed, err := m.Claim(ctx, child, pending.ID); err != nil || claimed == nil || claimed.Message != pending {
					t.Fatalf("child POP before source recovery: %+v %v", claimed, err)
				}
			}

			other, err := m.SendCrossWithReply(ctx, parent, sibling, "other peer", "", "", false)
			if err != nil {
				t.Fatalf("A to C blocked by B intent: %v", err)
			}
			if claimed, err := m.Claim(ctx, sibling, other.ID); err != nil || claimed == nil || claimed.Message != other {
				t.Fatalf("C POP: %+v %v", claimed, err)
			}
			reply, err := m.SendCrossWithReply(ctx, child, parent, "answer", request.ID, request.InputRequestID, false)
			if err != nil || !exactAnswer(request, reply) {
				t.Fatalf("B to A exact reply blocked: %+v %v", reply, err)
			}
			if claimed, err := m.Claim(ctx, parent, reply.ID); err != nil || claimed == nil || claimed.Message != reply {
				t.Fatalf("A reply POP: %+v %v", claimed, err)
			}
			outgoing, err := m.Send(ctx, parent, "approver", "local outgoing", "", false)
			if err != nil {
				t.Fatalf("local A send blocked: %v", err)
			}
			if claimed, err := m.Claim(ctx, local, outgoing.ID); err != nil || claimed == nil || claimed.Message != outgoing {
				t.Fatalf("local recipient POP: %+v %v", claimed, err)
			}
			incoming, err := m.Send(ctx, local, "worker", "local incoming", "", false)
			if err != nil {
				t.Fatalf("local A inbound send blocked: %v", err)
			}
			if claimed, err := m.Claim(ctx, parent, incoming.ID); err != nil || claimed == nil || claimed.Message != incoming {
				t.Fatalf("local A POP: %+v %v", claimed, err)
			}
			intentAfter, err := os.ReadFile(intentPath)
			if err != nil || string(intentAfter) != string(intentBefore) {
				t.Fatalf("unrelated traffic altered B intent: %v", err)
			}
			if state := m.State(ctx, parent, nil); state.Known {
				t.Fatalf("unrecovered B intent reported settled: %+v", state)
			}

			if _, err := m.SendCrossWithReply(ctx, parent, child, "resume exact peer", "", "", false); err != nil {
				t.Fatalf("exact B recovery: %v", err)
			}
			if step == "cross-intent" {
				if claimed, err := m.Claim(ctx, child, pending.ID); err != nil || claimed == nil || claimed.Message != pending {
					t.Fatalf("B delivery after recovery: %+v %v", claimed, err)
				}
			}
			if _, err := os.Stat(intentPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("recovered intent still present: %v", err)
			}
			childSnapshot, err := child.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			childName, err := Name(childSnapshot.Root)
			if err != nil {
				t.Fatal(err)
			}
			childBox := filepath.Join(m.Store.StateRoot, childName, "agents", "worker")
			if _, err := os.Stat(filepath.Join(childBox, "inbox", pending.ID+".md")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("recovery duplicated B inbox delivery: %v", err)
			}
			if _, err := os.Stat(filepath.Join(childBox, "read", pending.ID+".md")); err != nil {
				t.Fatalf("B read archive absent: %v", err)
			}
			if _, err := os.Stat(filepath.Join(m.Store.StateRoot, parentName, "agents", "worker", "sent", pending.ID+".md")); err != nil {
				t.Fatalf("one sender copy absent: %v", err)
			}
			if state := m.State(ctx, parent, nil); !state.Known {
				t.Fatalf("source state did not settle: %+v", state)
			}
		})
	}
}

func TestTmuxSnapshotRejectsAmbiguousAndUnboundPanes(t *testing.T) {
	f := newTmuxFixture(t)
	second := f.run(t, "split-window", "-d", "-t", f.pane, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", second, "-T", "approver")
	adapter := f.adapter()
	snapshot, err := adapter.Snapshot(context.Background())
	if err != nil || len(snapshot.Bindings) != 2 {
		t.Fatalf("distinct native roles: %#v %v", snapshot, err)
	}
	f.run(t, "select-pane", "-t", second, "-T", "worker")
	if _, err := adapter.Snapshot(context.Background()); err == nil {
		t.Fatal("duplicate role selected an arbitrary pane")
	}
	f.run(t, "select-pane", "-t", second, "-T", "unconfigured")
	adapter.Pane = second
	if _, err := adapter.Snapshot(context.Background()); !errors.Is(err, ErrUnknown) {
		t.Fatalf("unbound caller accepted: %v", err)
	}
	adapter = f.adapter()
	adapter.Roles = []string{"worker", "worker"}
	if _, err := adapter.Snapshot(context.Background()); !errors.Is(err, ErrUnknown) {
		t.Fatalf("ambiguous configuration accepted: %v", err)
	}
	adapter = f.adapter()
	adapter.Pane = "one"
	if _, err := adapter.Snapshot(context.Background()); !errors.Is(err, ErrUnknown) {
		t.Fatalf("session name used as pane identity: %v", err)
	}
}

func TestTmuxOwnershipVerifiesLiveAndRemovedSessions(t *testing.T) {
	f := newTmuxFixture(t)
	adapter := f.adapter()
	old, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second := f.run(t, "new-session", "-d", "-s", "two", "-c", f.repo, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", second, "-T", "worker")
	adapter.Pane = second
	next, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if next.Root != old.Root || sameRuntime(next.Caller.Owner, old.Caller.Owner) {
		t.Fatal("independent sessions aliased")
	}
	probe := func(owner Owner) (Liveness, error) { return adapter.Probe(context.Background(), owner) }
	if state, err := probe(old.Caller.Owner); err != nil || state != Alive {
		t.Fatalf("live session: %v %v", state, err)
	}
	if _, _, err := ResolveOwner(&old.Caller.Owner, next.Caller.Owner, probe); !errors.Is(err, ErrOwned) {
		t.Fatalf("live independent session accepted: %v", err)
	}
	f.run(t, "kill-session", "-t", old.Caller.Owner.Session)
	if state, err := probe(old.Caller.Owner); err != nil || state != Dead {
		t.Fatalf("verified removed session: %v %v", state, err)
	}
	if got, changed, err := ResolveOwner(&old.Caller.Owner, next.Caller.Owner, probe); err != nil || !changed || got != next.Caller.Owner {
		t.Fatalf("verified takeover: %#v %v %v", got, changed, err)
	}
	other := newTmuxFixture(t)
	otherSnapshot, err := other.adapter().Snapshot(context.Background())
	if err != nil || otherSnapshot.Caller.Owner.Incarnation == next.Caller.Owner.Incarnation ||
		otherSnapshot.Caller.Owner.Server == next.Caller.Owner.Server {
		t.Fatalf("independent servers aliased: %#v %v", otherSnapshot, err)
	}
}

func TestTmuxUnknownServerCannotAuthorizeTakeover(t *testing.T) {
	owner := Owner{
		Server: filepath.Join(t.TempDir(), "missing"), Session: "$1", Pane: "%1",
		Incarnation: fmt.Sprintf("%d:1:1:1:1", os.Getpid()),
	}
	state, err := (Tmux{}).Probe(context.Background(), owner)
	if state != Unknown || !errors.Is(err, ErrUnknown) {
		t.Fatalf("failed lookup mistaken for death: %v %v", state, err)
	}
	if processLiveness(os.Getpid()) != Alive || processLiveness(0) != Unknown {
		t.Fatal("portable process probe returned an unsupported state")
	}
	owner.Incarnation = "invalid"
	if state, err := (Tmux{}).Probe(context.Background(), owner); state != Unknown || !errors.Is(err, ErrUnknown) {
		t.Fatalf("invalid incarnation accepted: %v %v", state, err)
	}
	var output tmuxBuffer
	if _, err := output.Write(make([]byte, 64*1024+1)); err == nil {
		t.Fatal("oversized identity silently truncated")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tmuxOutput(ctx, filepath.Join(t.TempDir(), strconv.Itoa(os.Getpid())), "list-sessions"); err == nil {
		t.Fatal("cancelled lookup succeeded")
	}
}

func TestTmuxCLocaleWithoutParentPreservesIdentity(t *testing.T) {
	for _, key := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		t.Setenv(key, "C")
	}
	// A parent tmux otherwise implicitly enables UTF-8 client output.
	t.Setenv("TMUX", "")
	if err := os.Unsetenv("TMUX"); err != nil {
		t.Fatal(err)
	}
	f := newTmuxFixture(t)
	snapshot, err := f.adapter().Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Root != "~/repo" || snapshot.Caller.Owner.Pane != f.pane ||
		snapshot.Caller.Role != "worker" || len(snapshot.Bindings) != 1 {
		t.Fatalf("C locale corrupted native identity: %#v", snapshot)
	}
}

func TestTmuxCapturePaneRequiresExactNativeIncarnation(t *testing.T) {
	f := newTmuxFixture(t)
	adapter := f.adapter()
	snapshot, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if text, err := adapter.CapturePane(context.Background(), snapshot.Caller); !errors.Is(err, ErrUnknown) || text != "" {
		t.Fatalf("unregistered native capture returned text: %q %v", text, err)
	}
	old := snapshot.Caller
	old.Owner.Incarnation = "different"
	if _, err := adapter.CapturePane(context.Background(), old); !errors.Is(err, ErrUnknown) {
		t.Fatalf("wrong incarnation read pane: %v", err)
	}
}

func TestTmuxCaptureRejectsRetitleAndRespawnAfterSelection(t *testing.T) {
	f := newTmuxFixture(t)
	adapter := f.adapter()
	selected, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.run(t, "select-pane", "-t", f.pane, "-T", "unrelated")
	if text, err := adapter.CapturePane(context.Background(), selected.Caller); !errors.Is(err, ErrUnknown) || text != "" {
		t.Fatalf("retitled pane content escaped: %q %v", text, err)
	}
	f.run(t, "select-pane", "-t", f.pane, "-T", "worker")
	f.run(t, "respawn-pane", "-k", "-t", f.pane, "exec sleep 120")
	if text, err := adapter.CapturePane(context.Background(), selected.Caller); !errors.Is(err, ErrUnknown) || text != "" {
		t.Fatalf("respawned pane content escaped: %q %v", text, err)
	}
}

func TestTmuxCaptureFailsClosedOnRecentVisibleLines(t *testing.T) {
	f := newTmuxFixture(t)
	f.run(t, "respawn-pane", "-k", "-t", f.pane, `sh -c 'i=1; while [ $i -le 120 ]; do printf "line-%03d\n" $i; i=$((i+1)); done; exec sleep 120'`)
	deadline := time.Now().Add(2 * time.Second)
	for {
		visible := f.run(t, "capture-pane", "-p", "-t", f.pane, "-S", "-80")
		if strings.Contains(visible, "line-120") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture never produced its unrelated visible marker")
		}
		time.Sleep(10 * time.Millisecond)
	}
	adapter := f.adapter()
	snapshot, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if captured, err := adapter.CapturePane(context.Background(), snapshot.Caller); !errors.Is(err, ErrUnknown) || captured != "" {
		t.Fatalf("visible pane text escaped fail-closed capture: %q %v", captured, err)
	}
}

func TestTmuxCaptureRejectsOversizedNativeOutput(t *testing.T) {
	f := newTmuxFixture(t)
	f.run(t, "resize-window", "-t", "one", "-x", "400", "-y", "24")
	f.run(t, "respawn-pane", "-k", "-t", f.pane, `sh -c 'i=1; while [ $i -le 120 ]; do printf "%0300d\n" $i; i=$((i+1)); done; exec sleep 120'`)
	adapter := f.adapter()
	snapshot, err := adapter.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out, err := tmuxOutput(context.Background(), f.socket, "capture-pane", "-p", "-t", f.pane, "-S", "-80")
		if err != nil {
			t.Fatal(err)
		}
		if len(out) > 16*1024 {
			if _, err := tmuxOutputLimited(context.Background(), 16*1024, f.socket, "capture-pane", "-p", "-t", f.pane, "-S", "-80"); err == nil {
				t.Fatal("oversized native output bypassed limit")
			}
			if captured, err := adapter.CapturePane(context.Background(), snapshot.Caller); !errors.Is(err, ErrUnknown) || captured != "" {
				t.Fatalf("oversized native read escaped: %d bytes, %v", len(captured), err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fixture never produced oversized native output")
}
