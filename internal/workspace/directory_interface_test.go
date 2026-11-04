package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

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
