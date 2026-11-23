package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/helpdocs"
)

func TestStatusFingerprintSeparatesAuthoredPolicyFromRoutingTopology(t *testing.T) {
	path := filepath.Join(t.TempDir(), "postman.md")
	base := "---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- approver\n```\n## `worker`\nWorker instructions\n## `approver`\nApprover instructions\n"
	load := func(content string) config.WorkspaceTopology {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		topology, err := (config.Source{Path: path}).Load(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return topology
	}
	first := load(base)
	authoredChange := load(strings.Replace(base, "Worker instructions", "Updated worker instructions", 1))
	if StatusConfigFingerprint(first) != StatusConfigFingerprint(authoredChange) || first.PolicyIdentity() == authoredChange.PolicyIdentity() {
		t.Fatal("authored-only change altered routing identity or kept policy identity")
	}
	structuralChange := load(strings.Replace(base, "interface_node: worker", "interface_node: approver", 1))
	if StatusConfigFingerprint(first) == StatusConfigFingerprint(structuralChange) {
		t.Fatal("interface change retained routing identity")
	}
	returned, err := (config.Source{Path: filepath.Join(t.TempDir(), "absent.md"), Builtin: true}).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	bundled, err := fs.ReadFile(helpdocs.Assets(), "config/postman.md")
	if err != nil {
		t.Fatal(err)
	}
	customCopy := load(string(bundled))
	if StatusConfigFingerprint(returned) != StatusConfigFingerprint(customCopy) || returned.PolicyIdentity() == customCopy.PolicyIdentity() {
		t.Fatal("bundle provenance changed routing identity or lost authored identity")
	}
}

func TestCustomV1StatusRegistrationRequiresExactTopology(t *testing.T) {
	f := newTmuxFixture(t)
	m := mailFixture(t)
	rt := configuredStatusFixture(t, f, m)
	snapshot, err := rt.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	name, err := Name(snapshot.Root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.Store.StateRoot, name, "runtime", "status-config-p"+f.pane[1:]+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record statusConfigRecord
	if err := json.Unmarshal(data, &record); err != nil || record.Version != 1 || record.Builtin {
		t.Fatal("custom operation did not retain v1 registration", err, record)
	}
	original, err := os.ReadFile(rt.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rt.ConfigPath, append(append([]byte{}, original...), []byte("## `worker`\nUpdated instructions\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := m.Store.readStatusAttribution(context.Background(), snapshot.Root, snapshot.Caller.Owner, snapshot.Bindings); err != nil {
		t.Fatal("authored-only custom change altered v1 routing identity", err)
	}
	if err := os.WriteFile(rt.ConfigPath, []byte(strings.Replace(string(original), "worker --- approver", "worker --- third", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := m.Store.readStatusAttribution(context.Background(), snapshot.Root, snapshot.Caller.Owner, snapshot.Bindings); err == nil {
		t.Fatal("edited custom topology retained v1 status provenance")
	}
}

func TestDirectoryRouteAcceptsPinnedBuiltinParentAndRejectsStalePane(t *testing.T) {
	f := newTmuxFixture(t)
	childRoot := filepath.Join(f.repo, "child")
	if err := os.Mkdir(childRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	childPane := f.run(t, "new-session", "-d", "-s", "child", "-c", childRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	for _, pane := range []string{f.pane, childPane} {
		f.run(t, "select-pane", "-t", pane, "-T", "orchestrator")
	}
	ctx := context.Background()
	configPath := filepath.Join(f.home, "absent-postman.md")
	topology, err := (config.Source{Path: configPath, Builtin: true}).Load(ctx)
	if err != nil || topology.InterfaceNode != "orchestrator" {
		t.Fatalf("built-in directory interface: %q %v", topology.InterfaceNode, err)
	}
	adapter := f.adapter()
	adapter.Roles = topology.Nodes
	adapter.ConfigPath, adapter.BuiltinConfig = configPath, true
	adapter.ConfigFingerprint = StatusConfigFingerprint(topology)
	store := Store{StateRoot: filepath.Join(f.home, "state")}
	for _, pane := range []string{f.pane, childPane} {
		registerDirectoryPane(t, store, adapter, pane)
	}
	parentName, err := Name("~/repo")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.StateRoot, parentName, "runtime", "status-config-p"+f.pane[1:]+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record statusConfigRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if record.Version != 3 || record.Fingerprint != StatusConfigFingerprint(topology) {
		t.Fatal("normal operation did not write structural v3 registration", record)
	}
	write := func() {
		t.Helper()
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	record.Version = 2
	record.Fingerprint = "sha256:819dd379f45c6df17bc9a2129648c769e68449804a73ff16a51f3f59052aa3e4"
	write()
	adapter.Pane = childPane
	if _, err := store.VerifiedDirectoryInterfacesOnRoute(ctx, adapter, "orchestrator", "~/repo"); err == nil {
		t.Fatal("prior v2 built-in registration was accepted without normal re-registration")
	}
	record.Fingerprint = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	write()
	if _, err := store.VerifiedDirectoryInterfacesOnRoute(ctx, adapter, "orchestrator", "~/repo"); err == nil {
		t.Fatal("foreign v2 built-in registration was accepted")
	}
	record.Fingerprint = legacyBuiltinStatusFingerprint(topology)
	write()
	interfaces, err := store.VerifiedDirectoryInterfacesOnRoute(ctx, adapter, "orchestrator", "~/repo")
	if err != nil {
		t.Fatal(err)
	}
	selected, err := DirectDirectoryPeer(interfaces, "child", "one")
	if err != nil || selected.Root != "~/repo" {
		t.Fatalf("current-legacy v2 parent route: %+v %v", selected, err)
	}
	if selected.Runtime.tmux.ConfigFingerprint != StatusConfigFingerprint(topology) {
		t.Fatal("verified v2 route did not expose structural identity")
	}
	adapter.Pane = f.pane
	registerDirectoryPane(t, store, adapter, f.pane)
	data, err = os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &record) != nil || record.Version != 3 {
		t.Fatal("normal operation did not re-register built-in v3", err, record)
	}
	adapter.Pane = childPane
	record.Fingerprint = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	write()
	if _, err := store.VerifiedDirectoryInterfacesOnRoute(ctx, adapter, "orchestrator", "~/repo"); err == nil {
		t.Fatal("tampered v3 built-in registration was accepted")
	}
	record.Fingerprint = StatusConfigFingerprint(topology)
	write()
	child, err := adapter.NamedCaller(ctx)
	if err != nil {
		t.Fatal(err)
	}
	parent := selected.Runtime
	mail := Mail{Store: store}
	request, err := mail.SendCrossWithReply(ctx, child, parent.WithDirectoryRoute(), "handoff", "", "", true)
	if err != nil || request.ID == "" {
		t.Fatalf("pinned parent mail: %+v %v", request, err)
	}
	if _, err := mail.Claim(ctx, parent, request.ID); err != nil {
		t.Fatal(err)
	}
	record.Binding.PanePID = "999999"
	write()
	if _, err := store.VerifiedDirectoryInterfacesOnRoute(ctx, adapter, "orchestrator", "~/repo"); err == nil {
		t.Fatal("stale parent pane provenance admitted")
	}
}

func directoryConfiguredAdapter(t *testing.T, f tmuxFixture) Tmux {
	t.Helper()
	path := filepath.Join(f.home, "directory-postman.md")
	content := "---\ninterface_node: worker\n---\n## `edges`\n```mermaid\ngraph LR\nworker --- approver\n```\n## `worker`\nWorker\n## `approver`\nApprover\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	topology, err := (config.Source{Path: path}).Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	adapter := f.adapter()
	adapter.ConfigPath = path
	adapter.ConfigFingerprint = StatusConfigFingerprint(topology)
	return adapter
}

func registerDirectoryPane(t *testing.T, store Store, adapter Tmux, pane string) {
	t.Helper()
	adapter.Pane = pane
	if err := store.WithVerified(context.Background(), adapter, func(*os.Root, Snapshot) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func markFormerDirectoryRuntime(t *testing.T, store Store, home, root string) (string, []byte) {
	t.Helper()
	canonical, err := CanonicalRoot(root, home)
	if err != nil {
		t.Fatal(err)
	}
	name, err := Name(canonical)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.StateRoot, name, "runtime", "binding.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var former runtimeRecord
	if err := json.Unmarshal(data, &former); err != nil {
		t.Fatal(err)
	}
	former.Owner.Session = "$999"
	for i := range former.Bindings {
		former.Bindings[i].Owner.Session = "$999"
	}
	data, err = json.Marshal(former)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, data
}

func removeDirectoryProvenance(t *testing.T, store Store, home, root, pane string) {
	t.Helper()
	canonical, err := CanonicalRoot(root, home)
	if err != nil {
		t.Fatal(err)
	}
	name, err := Name(canonical)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.StateRoot, name, "runtime", "status-config-p"+strings.TrimPrefix(pane, "%")+".json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

func TestDirectoryRouteUnknownProvenanceIsScopedToAddressedCorridor(t *testing.T) {
	t.Run("off-route sibling allows exact exchange", func(t *testing.T) {
		f := newTmuxFixture(t)
		alphaRoot, betaRoot := filepath.Join(f.repo, "alpha"), filepath.Join(f.repo, "beta")
		for _, root := range []string{alphaRoot, betaRoot} {
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		alphaPane := f.run(t, "new-session", "-d", "-s", "alpha", "-c", alphaRoot,
			"-P", "-F", "#{pane_id}", "exec sleep 120")
		betaPane := f.run(t, "new-session", "-d", "-s", "beta", "-c", betaRoot,
			"-P", "-F", "#{pane_id}", "exec sleep 120")
		for _, pane := range []string{alphaPane, betaPane} {
			f.run(t, "select-pane", "-t", pane, "-T", "worker")
		}
		ctx := context.Background()
		adapter := directoryConfiguredAdapter(t, f)
		m := Mail{Store: Store{StateRoot: filepath.Join(f.home, "state")}}
		for _, pane := range []string{f.pane, alphaPane, betaPane} {
			registerDirectoryPane(t, m.Store, adapter, pane)
		}
		removeDirectoryProvenance(t, m.Store, f.home, betaRoot, betaPane)
		parent, err := adapter.NamedCaller(ctx)
		if err != nil {
			t.Fatal(err)
		}
		alpha, err := adapter.FindNamedPeer(ctx, "alpha", "worker")
		if err != nil {
			t.Fatal(err)
		}
		beta, err := adapter.FindNamedPeer(ctx, "beta", "worker")
		if err != nil {
			t.Fatal(err)
		}
		request, err := m.SendCrossWithReply(ctx, parent, alpha.WithDirectoryRoute(), "task", "", "", true)
		if err != nil || request.ID == "" {
			t.Fatal("off-route beta vetoed alpha", request, err)
		}
		if _, err := m.Claim(ctx, alpha, request.ID); err != nil {
			t.Fatal(err)
		}
		answer, err := m.SendCrossReply(ctx, alpha, parent.WithDirectoryRoute(), []string{request.ID}, "answer")
		if err != nil || len(answer.Replies) != 1 || answer.Replies[0].MessageID != request.ID {
			t.Fatal("exact alpha reply failed", answer, err)
		}
		if _, err := m.Claim(ctx, parent, answer.ID); err != nil {
			t.Fatal(err)
		}
		betaSnapshot, err := beta.Snapshot(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.Store.VerifiedDirectoryInterfacesOnRoute(ctx, adapter, "worker", betaSnapshot.Root); err == nil {
			t.Fatal("unknown beta became a verified route")
		}
	})
	t.Run("unknown intermediate rejects before durable mail", func(t *testing.T) {
		f := newTmuxFixture(t)
		middleRoot := filepath.Join(f.repo, "middle")
		alphaRoot := filepath.Join(middleRoot, "alpha")
		if err := os.MkdirAll(alphaRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		middlePane := f.run(t, "new-session", "-d", "-s", "middle", "-c", middleRoot,
			"-P", "-F", "#{pane_id}", "exec sleep 120")
		alphaPane := f.run(t, "new-session", "-d", "-s", "alpha", "-c", alphaRoot,
			"-P", "-F", "#{pane_id}", "exec sleep 120")
		for _, pane := range []string{middlePane, alphaPane} {
			f.run(t, "select-pane", "-t", pane, "-T", "worker")
		}
		ctx := context.Background()
		adapter := directoryConfiguredAdapter(t, f)
		m := Mail{Store: Store{StateRoot: filepath.Join(f.home, "state")}}
		for _, pane := range []string{f.pane, middlePane, alphaPane} {
			registerDirectoryPane(t, m.Store, adapter, pane)
		}
		removeDirectoryProvenance(t, m.Store, f.home, middleRoot, middlePane)
		parent, err := adapter.NamedCaller(ctx)
		if err != nil {
			t.Fatal(err)
		}
		alpha, err := adapter.FindNamedPeer(ctx, "alpha", "worker")
		if err != nil {
			t.Fatal(err)
		}
		before := directoryTestFiles(t, m.Store.StateRoot)
		rejected, err := m.SendCrossWithReply(ctx, parent, alpha.WithDirectoryRoute(), "shortcut", "", "", true)
		if err == nil || rejected.ID != "" {
			t.Fatal("unknown intermediate admitted route", rejected, err)
		}
		if after := directoryTestFiles(t, m.Store.StateRoot); !slices.Equal(before, after) {
			t.Fatal("pre-stage rejection changed durable files", before, after)
		}
	})
}

func TestDirectoryRouteIgnoresOrdinaryUnregisteredIntermediatePane(t *testing.T) {
	f := newTmuxFixture(t)
	middleRoot := filepath.Join(f.repo, "a")
	childRoot := filepath.Join(middleRoot, "b")
	if err := os.MkdirAll(childRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	middlePane := f.run(t, "new-session", "-d", "-s", "ordinary", "-c", middleRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	childPane := f.run(t, "new-session", "-d", "-s", "child", "-c", childRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", middlePane, "-T", "shell")
	f.run(t, "select-pane", "-t", childPane, "-T", "worker")
	ctx := context.Background()
	adapter := directoryConfiguredAdapter(t, f)
	m := Mail{Store: Store{StateRoot: filepath.Join(f.home, "state")}}
	for _, pane := range []string{f.pane, childPane} {
		registerDirectoryPane(t, m.Store, adapter, pane)
	}
	parent, err := adapter.NamedCaller(ctx)
	if err != nil {
		t.Fatal(err)
	}
	child, err := adapter.FindNamedPeer(ctx, "child", "worker")
	if err != nil {
		t.Fatal(err)
	}
	request, err := m.SendCrossWithReply(ctx, parent, child.WithDirectoryRoute(), "task", "", "", true)
	if err != nil || request.ID == "" {
		t.Fatal("ordinary unregistered pane blocked child task", request, err)
	}
	if _, err := m.Claim(ctx, child, request.ID); err != nil {
		t.Fatal(err)
	}
	answer, err := m.SendCrossReply(ctx, child, parent.WithDirectoryRoute(), []string{request.ID}, "answer")
	if err != nil || len(answer.Replies) != 1 || answer.Replies[0].MessageID != request.ID {
		t.Fatal("ordinary unregistered pane blocked exact reply", answer, err)
	}
	if _, err := m.Claim(ctx, parent, answer.ID); err != nil {
		t.Fatal(err)
	}

	f.run(t, "select-pane", "-t", middlePane, "-T", "worker")
	registerDirectoryPane(t, m.Store, adapter, middlePane)
	removeDirectoryProvenance(t, m.Store, f.home, middleRoot, middlePane)
	before := directoryTestFiles(t, m.Store.StateRoot)
	rejected, err := m.SendCrossWithReply(ctx, parent, child.WithDirectoryRoute(), "blocked", "", "", true)
	if err == nil || rejected.ID != "" {
		t.Fatal("registered but stale intermediate admitted mail", rejected, err)
	}
	if after := directoryTestFiles(t, m.Store.StateRoot); !slices.Equal(before, after) {
		t.Fatal("stale intermediate changed durable files", before, after)
	}
}

func TestRecipientDirectoryIgnoresDormantFormerHomeAncestor(t *testing.T) {
	f := newTmuxFixture(t)
	childRoot := filepath.Join(f.repo, "child")
	if err := os.Mkdir(childRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	homePane := f.run(t, "new-session", "-d", "-s", "home-shell", "-c", f.home,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	childPane := f.run(t, "new-session", "-d", "-s", "child", "-c", childRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", homePane, "-T", "worker")
	f.run(t, "select-pane", "-t", childPane, "-T", "worker")
	ctx := context.Background()
	adapter := directoryConfiguredAdapter(t, f)
	store := Store{StateRoot: filepath.Join(f.home, "state")}
	for _, pane := range []string{f.pane, childPane, homePane} {
		registerDirectoryPane(t, store, adapter, pane)
	}
	path, data := markFormerDirectoryRuntime(t, store, f.home, f.home)
	f.run(t, "select-pane", "-t", homePane, "-T", "shell")
	parent, err := adapter.NamedCaller(ctx)
	if err != nil {
		t.Fatal(err)
	}
	interfaces, err := store.VerifiedDirectoryInterfacesFrom(ctx, parent)
	if err != nil {
		t.Fatal("dormant HOME registration vetoed parent contacts", err)
	}
	if len(interfaces) != 2 {
		t.Fatalf("want parent and child, got %+v", interfaces)
	}
	if _, err := DirectDirectoryPeer(interfaces, "one", "child"); err != nil {
		t.Fatal("direct child contact missing", err)
	}
	child, err := adapter.FindNamedPeer(ctx, "child", "worker")
	if err != nil {
		t.Fatal(err)
	}
	mail := Mail{Store: store}
	request, err := mail.SendCrossWithReply(ctx, child, parent.WithDirectoryRoute(), "question", "", "", true)
	if err != nil || request.ID == "" {
		t.Fatal("dormant HOME registration blocked child request", request, err)
	}
	if _, err := mail.Claim(ctx, parent, request.ID); err != nil {
		t.Fatal(err)
	}
	answer, err := mail.SendCrossReply(ctx, parent, child.WithDirectoryRoute(), []string{request.ID}, "answer")
	if err != nil || len(answer.Replies) != 1 || answer.Replies[0].MessageID != request.ID {
		t.Fatal("dormant HOME registration blocked exact reply", answer, err)
	}
	if _, err := mail.Claim(ctx, child, answer.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(append([]byte{}, data...), []byte("{}")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.VerifiedDirectoryInterfacesFrom(ctx, parent); err == nil {
		t.Fatal("trailing JSON entered former-runtime exception")
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f.run(t, "select-pane", "-t", homePane, "-T", "worker")
	if _, err := store.VerifiedDirectoryInterfacesFrom(ctx, parent); err == nil {
		t.Fatal("plausible stale HOME interface was accepted")
	}
}

func TestRecipientDirectoryRejectsDormantNestedAncestorBeforeFartherParent(t *testing.T) {
	f := newTmuxFixture(t)
	middleRoot := filepath.Join(f.repo, "middle")
	leafRoot := filepath.Join(middleRoot, "leaf")
	if err := os.MkdirAll(leafRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	middlePane := f.run(t, "new-session", "-d", "-s", "middle", "-c", middleRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	leafPane := f.run(t, "new-session", "-d", "-s", "leaf", "-c", leafRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	for _, pane := range []string{middlePane, leafPane} {
		f.run(t, "select-pane", "-t", pane, "-T", "worker")
	}
	adapter := directoryConfiguredAdapter(t, f)
	store := Store{StateRoot: filepath.Join(f.home, "state")}
	for _, pane := range []string{f.pane, middlePane, leafPane} {
		registerDirectoryPane(t, store, adapter, pane)
	}
	markFormerDirectoryRuntime(t, store, f.home, middleRoot)
	f.run(t, "select-pane", "-t", middlePane, "-T", "shell")
	leaf, err := adapter.FindNamedPeer(context.Background(), "leaf", "worker")
	if err != nil {
		t.Fatal(err)
	}
	if contacts, err := store.VerifiedDirectoryInterfacesFrom(context.Background(), leaf); err == nil {
		t.Fatalf("nested former ancestor hid farther live parent: %+v", contacts)
	}
}

func TestDirectDirectoryPeerUsesComponentsAndNearestLiveLevel(t *testing.T) {
	base := []DirectoryInterface{
		{Session: "parent", Root: "~/github.com"},
		{Session: "child", Root: "~/github.com/repo"},
		{Session: "leaf", Root: "~/github.com/repo/sub"},
		{Session: "sibling", Root: "~/github.com/other"},
		{Session: "prefix", Root: "~/github.com/repository"},
	}
	for _, pair := range [][2]string{{"parent", "child"}, {"child", "parent"}, {"child", "leaf"}} {
		if got, err := DirectDirectoryPeer(base, pair[0], pair[1]); err != nil || got.Session != pair[1] {
			t.Fatalf("direct route %v: %+v %v", pair, got, err)
		}
	}
	for _, pair := range [][2]string{{"parent", "leaf"}, {"leaf", "parent"}, {"child", "sibling"}, {"child", "prefix"}, {"parent", "parent"}, {"parent", "missing"}} {
		if _, err := DirectDirectoryPeer(base, pair[0], pair[1]); err == nil {
			t.Fatalf("invalid route %v accepted", pair)
		}
	}
	withoutMiddle := append([]DirectoryInterface{}, base[0], base[2])
	if got, err := DirectDirectoryPeer(withoutMiddle, "parent", "leaf"); err != nil || got.Session != "leaf" {
		t.Fatalf("nearest live ancestor: %+v %v", got, err)
	}
	if !directoryContains("~", "~/github.com") || directoryContains("~/github.com", "~/github.com") || directoryContains("~/github.com/repo", "~/github.com/repository") {
		t.Fatal("directory component boundary was not respected")
	}
	for _, duplicate := range []DirectoryInterface{{Session: "child", Root: "~/elsewhere"}, {Session: "other", Root: "~/github.com/repo"}} {
		if _, err := DirectDirectoryPeer(append(append([]DirectoryInterface{}, base...), duplicate), "parent", "child"); err == nil {
			t.Fatal("ambiguous directory interface accepted", duplicate)
		}
	}
}

func TestDirectoryInterfacesVerifyNativeRootsAndRejectAmbiguity(t *testing.T) {
	f := newTmuxFixture(t)
	childRoot := filepath.Join(f.repo, "child")
	leafRoot := filepath.Join(childRoot, "leaf")
	for _, root := range []string{childRoot, leafRoot} {
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ name, root string }{{"child", childRoot}, {"leaf", leafRoot}} {
		pane := f.run(t, "new-session", "-d", "-s", item.name, "-c", item.root,
			"-P", "-F", "#{pane_id}", "exec sleep 120")
		f.run(t, "select-pane", "-t", pane, "-T", "worker")
	}
	ctx := context.Background()
	interfaces, err := f.adapter().DirectoryInterfaces(ctx, "worker")
	if err != nil || len(interfaces) != 3 {
		t.Fatalf("native interfaces: %+v %v", interfaces, err)
	}
	if _, err := DirectDirectoryPeer(interfaces, "one", "leaf"); err == nil {
		t.Fatal("grandchild route skipped a live interface")
	}
	selected, err := DirectDirectoryPeer(interfaces, "one", "child")
	if err != nil || selected.Root != "~/repo/child" {
		t.Fatalf("verified child route: %+v %v", selected, err)
	}
	unrelated := f.run(t, "new-session", "-d", "-s", "unrelated space", "-c", f.home,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", unrelated, "-T", "ordinary")
	interfaces, err = f.adapter().DirectoryInterfaces(ctx, "worker")
	if err != nil || len(interfaces) != 3 {
		t.Fatalf("unrelated native name blocked valid interfaces: %+v %v", interfaces, err)
	}
	if selected, err := DirectDirectoryPeer(interfaces, "one", "child"); err != nil || selected.Session != "child" {
		t.Fatalf("unrelated native name blocked direct route: %+v %v", selected, err)
	}
	if _, err := f.adapter().FindNamedPeer(ctx, "child", "worker"); err != nil {
		t.Fatalf("unrelated native name blocked target lookup: %v", err)
	}
	if _, err := f.adapter().FindNamedPeer(ctx, "unrelated space", "worker"); err == nil {
		t.Fatal("Postman-disallowed target address accepted")
	}
	// Older tmux versions can expose tabs in session names. This version
	// rejects them on creation, so substitute that one native name lookup.
	tabSession := f.run(t, "display-message", "-p", "-t", unrelated, "#{session_id}")
	realTmux, err := exec.LookPath("tmux")
	if err != nil {
		t.Fatal(err)
	}
	proxyDir := t.TempDir()
	proxy := "#!/bin/sh\ncase \"$*\" in\n" +
		"  *list-sessions*session_name*) exit 91 ;;\n" +
		"  *display-message*\"$POSTMAN_TAB_SESSION\"*session_name*) printf '%s\\tunrelated\\tname\\n' \"$POSTMAN_TAB_SESSION\"; exit 0 ;;\n" +
		"esac\nexec \"$POSTMAN_REAL_TMUX\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(proxyDir, "tmux"), []byte(proxy), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("POSTMAN_TAB_SESSION", tabSession)
	t.Setenv("POSTMAN_REAL_TMUX", realTmux)
	t.Setenv("PATH", proxyDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if interfaces, err := f.adapter().DirectoryInterfaces(ctx, "worker"); err != nil || len(interfaces) != 3 {
		t.Fatalf("unrelated tabbed native name blocked valid interfaces: %+v %v", interfaces, err)
	}
	if _, err := f.adapter().FindNamedPeer(ctx, "child", "worker"); err != nil {
		t.Fatalf("unrelated tabbed native name blocked target lookup: %v", err)
	}
	f.run(t, "select-pane", "-t", unrelated, "-T", "worker")
	if _, err := f.adapter().DirectoryInterfaces(ctx, "worker"); err == nil {
		t.Fatal("tabbed native name with selected interface was accepted")
	}
	f.run(t, "select-pane", "-t", unrelated, "-T", "ordinary")
	alias := filepath.Join(f.home, "alias")
	if err := os.Symlink(childRoot, alias); err != nil {
		t.Fatal(err)
	}
	duplicate := f.run(t, "new-session", "-d", "-s", "duplicate", "-c", alias,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", duplicate, "-T", "worker")
	if _, err := f.adapter().DirectoryInterfaces(ctx, "worker"); err == nil {
		t.Fatal("symlink alias of canonical interface root accepted")
	}
	f.run(t, "kill-session", "-t", "duplicate")
	second := f.run(t, "split-window", "-d", "-t", "child", "-c", childRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", second, "-T", "worker")
	if _, err := f.adapter().DirectoryInterfaces(ctx, "worker"); err == nil {
		t.Fatal("ambiguous live interface role accepted")
	}
}

func TestDirectoryRouteRecheckedBeforePublication(t *testing.T) {
	for _, tc := range []struct {
		name, change string
		reply        bool
	}{
		{"new endpoint moved", "move", false},
		{"new intermediate appeared", "middle", false},
		{"reply endpoint moved", "move", true},
		{"reply intermediate appeared", "middle", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTmuxFixture(t)
			branchRoot := filepath.Join(f.repo, "branch")
			childRoot := filepath.Join(branchRoot, "leaf")
			otherRoot := filepath.Join(f.repo, "other")
			for _, root := range []string{childRoot, otherRoot} {
				if err := os.MkdirAll(root, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			canonicalOtherRoot, err := filepath.EvalSymlinks(otherRoot)
			if err != nil {
				t.Fatal(err)
			}
			childPane := f.run(t, "new-session", "-d", "-s", "child", "-c", childRoot,
				"-P", "-F", "#{pane_id}", "exec env HISTFILE=/dev/null sh -i")
			f.run(t, "select-pane", "-t", childPane, "-T", "worker")
			ctx := context.Background()
			adapter := directoryConfiguredAdapter(t, f)
			parent, err := adapter.NamedCaller(ctx)
			if err != nil {
				t.Fatal(err)
			}
			child, err := adapter.FindNamedPeer(ctx, "child", "worker")
			if err != nil {
				t.Fatal(err)
			}
			m := Mail{Store: Store{StateRoot: filepath.Join(f.home, "state")}}
			registerDirectoryPane(t, m.Store, adapter, f.pane)
			registerDirectoryPane(t, m.Store, adapter, childPane)
			source, destination := parent, child.WithDirectoryRoute()
			var request Message
			if tc.reply {
				request, err = m.SendCrossWithReply(ctx, source, destination, "question", "", "", true)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := m.Claim(ctx, child, request.ID); err != nil {
					t.Fatal(err)
				}
				source, destination = child, parent.WithDirectoryRoute()
			}
			var beforeFiles []string
			m.afterStep = func(step string) error {
				if step != "cross-before-publish" {
					return nil
				}
				beforeFiles = directoryTestFiles(t, m.Store.StateRoot)
				switch tc.change {
				case "move":
					f.run(t, "send-keys", "-l", "-t", childPane, "cd "+otherRoot)
					f.run(t, "send-keys", "-t", childPane, "C-m")
					deadline := time.Now().Add(time.Second)
					for time.Now().Before(deadline) {
						if f.run(t, "display-message", "-p", "-t", childPane, "#{pane_current_path}") == canonicalOtherRoot {
							return nil
						}
						time.Sleep(10 * time.Millisecond)
					}
					t.Fatalf("owned child pane did not change root: got %q, want %q",
						f.run(t, "display-message", "-p", "-t", childPane, "#{pane_current_path}"), canonicalOtherRoot)
				case "middle":
					pane := f.run(t, "new-session", "-d", "-s", "middle", "-c", branchRoot,
						"-P", "-F", "#{pane_id}", "exec sleep 120")
					f.run(t, "select-pane", "-t", pane, "-T", "worker")
					registerDirectoryPane(t, m.Store, adapter, pane)
					beforeFiles = directoryTestFiles(t, m.Store.StateRoot)
				}
				return nil
			}
			var rejected Message
			if tc.reply {
				rejected, err = m.SendCrossReply(ctx, source, destination, []string{request.ID}, "answer")
			} else {
				rejected, err = m.SendCrossWithReply(ctx, source, destination, "question", "", "", true)
			}
			if err == nil || rejected.ID != "" {
				t.Fatalf("pre-stage rejection exposed a recovery ID: %+v %v", rejected, err)
			}
			if afterFiles := directoryTestFiles(t, m.Store.StateRoot); !slices.Equal(beforeFiles, afterFiles) {
				t.Fatalf("pre-stage rejection changed durable files: before=%v after=%v", beforeFiles, afterFiles)
			}
			if tc.reply {
				childName, err := Name("~/repo/branch/leaf")
				if err != nil {
					t.Fatal(err)
				}
				for _, suffix := range []string{".md", ".pop.json"} {
					path := filepath.Join(m.Store.StateRoot, childName, "agents", "worker", "read", request.ID+suffix)
					if _, err := os.Stat(path); err != nil {
						t.Fatalf("original claimed reply obligation missing at %s: %v", path, err)
					}
				}
			}
		})
	}
}

func directoryTestFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestDirectoryRouteRetainsRecoveryIdentityAfterStage(t *testing.T) {
	f := newTmuxFixture(t)
	childRoot := filepath.Join(f.repo, "child")
	if err := os.Mkdir(childRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	childPane := f.run(t, "new-session", "-d", "-s", "child", "-c", childRoot,
		"-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", childPane, "-T", "worker")
	ctx := context.Background()
	adapter := directoryConfiguredAdapter(t, f)
	parent, err := adapter.NamedCaller(ctx)
	if err != nil {
		t.Fatal(err)
	}
	child, err := adapter.FindNamedPeer(ctx, "child", "worker")
	if err != nil {
		t.Fatal(err)
	}
	m := Mail{Store: Store{StateRoot: filepath.Join(f.home, "state")}}
	registerDirectoryPane(t, m.Store, adapter, f.pane)
	registerDirectoryPane(t, m.Store, adapter, childPane)
	m.afterStep = func(step string) error {
		if step == "cross-intent" {
			return errors.New("injected interruption after durable intent")
		}
		return nil
	}
	interrupted, err := m.SendCrossWithReply(ctx, parent, child.WithDirectoryRoute(), "question", "", "", true)
	if err == nil || interrupted.ID == "" {
		t.Fatalf("post-stage interruption lost recovery identity: %+v %v", interrupted, err)
	}
	sourceName, err := Name("~/repo")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(m.Store.StateRoot, sourceName, "agents", "worker", "draft", interrupted.ID+".md"),
		filepath.Join(m.Store.StateRoot, sourceName, "transactions", "cross_send-"+interrupted.ID+".json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("recovery identity lacks durable artifact %s: %v", path, err)
		}
	}
}
