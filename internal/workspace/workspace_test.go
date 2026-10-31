package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRootCanonicalizesAliasesAndHomeOnly(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "work", "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(home, "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	a, err := CanonicalRoot(alias, home)
	if err != nil {
		t.Fatal(err)
	}
	b, err := CanonicalRoot(repo, home)
	if err != nil || a != b || a != "~/work/repo" {
		t.Fatalf("roots %q %q: %v", a, b, err)
	}
	outside := t.TempDir()
	c, err := CanonicalRoot(outside, home)
	if err == nil || c != "" {
		t.Fatalf("outside accepted %q: %v", c, err)
	}
	if _, err := CanonicalRoot("relative", home); err == nil {
		t.Fatal("relative root accepted")
	}
}

func TestWorkspaceNamesCannotCollide(t *testing.T) {
	roots := []string{"~/a-b/c", "~/a/b-c", "~/a%2Db/c", "~/a/b", "~", "~/registry", "~/locks", "~/日本語/a", "~/a/.hidden", "~/A", "~/a", "~/%00"}
	seen := map[string]string{}
	for _, root := range roots {
		name, err := Name(root)
		if err != nil {
			t.Fatal(err)
		}
		if old, ok := seen[name]; ok {
			t.Fatalf("%q and %q collide at %q", root, old, name)
		}
		seen[name] = root
		if name == "registry" || name == "locks" || filepath.Base(name) != name {
			t.Fatalf("unsafe name %q", name)
		}
	}
	if name, _ := Name("~/a-b/c"); name != "a%2Db-c" {
		t.Fatalf("unreadable name %q", name)
	}
	for _, root := range []string{"", "relative", "~/a/../b", "~/a//b", "~/a/", "/outside"} {
		if _, err := Name(root); err == nil {
			t.Fatalf("invalid root accepted: %q", root)
		}
	}
}

func TestOwnershipFailsClosedAndRetainsHistory(t *testing.T) {
	old := Owner{Server: "socket-a", Incarnation: "boot-a", Session: "$1", Pane: "%1"}
	next := Owner{Server: "socket-b", Incarnation: "boot-b", Session: "$2", Pane: "%2"}
	calls := 0
	probe := func(got Owner) (Liveness, error) {
		calls++
		if got != old {
			t.Fatal("wrong owner probed")
		}
		return Alive, nil
	}
	current, changed, err := ResolveOwner(&old, old, probe)
	if err != nil || changed || current != old || calls != 0 {
		t.Fatalf("same owner: %v %v %v", current, changed, err)
	}
	if _, _, err := ResolveOwner(&old, next, probe); !errors.Is(err, ErrOwned) {
		t.Fatalf("live owner accepted: %v", err)
	}
	for _, state := range []Liveness{Unknown, Liveness(99)} {
		if _, _, err := ResolveOwner(&old, next, func(Owner) (Liveness, error) { return state, nil }); !errors.Is(err, ErrUnknown) {
			t.Fatalf("unknown accepted: %v", err)
		}
	}
	if _, _, err := ResolveOwner(&old, next, func(Owner) (Liveness, error) { return Dead, errors.New("probe failed") }); !errors.Is(err, ErrUnknown) {
		t.Fatalf("failed probe accepted: %v", err)
	}
	current, changed, err = ResolveOwner(&old, next, func(Owner) (Liveness, error) { return Dead, nil })
	if err != nil || !changed || current != next || old.Server != "socket-a" {
		t.Fatalf("takeover: %v %v %v", current, changed, err)
	}
	if _, _, err := ResolveOwner(&old, next, nil); !errors.Is(err, ErrUnknown) {
		t.Fatalf("nil probe accepted: %v", err)
	}
	if _, _, err := ResolveOwner(nil, Owner{}, probe); err == nil {
		t.Fatal("unverified candidate accepted")
	}
}

func TestBindingsRejectMissingAndAmbiguousNativeIdentity(t *testing.T) {
	owner := Owner{Server: "socket", Incarnation: "boot", Session: "$1", Pane: "%1"}
	second := owner
	second.Pane = "%2"
	if err := ValidateBindings([]Binding{{Role: "worker", Owner: owner}, {Role: "approver", Owner: second}}); err != nil {
		t.Fatal(err)
	}
	missing := owner
	missing.Pane = ""
	badPane := owner
	badPane.Pane = "1"
	badSession := owner
	badSession.Session = "session-name"
	otherRuntime := second
	otherRuntime.Incarnation = "other-boot"
	cases := [][]Binding{
		nil,
		{{Role: "worker", Owner: missing}},
		{{Role: "worker", Owner: badPane}},
		{{Role: "worker", Owner: badSession}},
		{{Role: "worker", Owner: owner}, {Role: "worker", Owner: second}},
		{{Role: "worker", Owner: owner}, {Role: "approver", Owner: owner}},
		{{Role: "worker", Owner: owner}, {Role: "approver", Owner: otherRuntime}},
		{{Role: "../escape", Owner: owner}},
	}
	for _, bindings := range cases {
		if err := ValidateBindings(bindings); err == nil {
			t.Fatalf("invalid bindings accepted: %#v", bindings)
		}
	}
	if _, _, err := ResolveOwner(nil, missing, nil); !errors.Is(err, ErrUnknown) {
		t.Fatalf("missing candidate pane accepted: %v", err)
	}
	if _, _, err := ResolveOwner(&missing, owner, nil); !errors.Is(err, ErrUnknown) {
		t.Fatalf("missing stored pane accepted: %v", err)
	}
	calls := 0
	got, changed, err := ResolveOwner(&owner, second, func(Owner) (Liveness, error) { calls++; return Unknown, nil })
	if err != nil || !changed || got != second || calls != 0 {
		t.Fatalf("another role in same verified runtime: %#v %v %v calls=%d", got, changed, err, calls)
	}
}
