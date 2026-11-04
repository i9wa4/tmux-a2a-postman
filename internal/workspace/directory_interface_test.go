package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
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
			childPane := f.run(t, "new-session", "-d", "-s", "child", "-c", childRoot,
				"-P", "-F", "#{pane_id}", "exec sh -i")
			f.run(t, "select-pane", "-t", childPane, "-T", "worker")
			ctx := context.Background()
			parent, err := f.adapter().NamedCaller(ctx)
			if err != nil {
				t.Fatal(err)
			}
			child, err := f.adapter().FindNamedPeer(ctx, "child", "worker")
			if err != nil {
				t.Fatal(err)
			}
			m := Mail{Store: Store{StateRoot: filepath.Join(f.home, "state")}}
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
			m.afterStep = func(step string) error {
				if step != "cross-before-publish" {
					return nil
				}
				switch tc.change {
				case "move":
					f.run(t, "send-keys", "-l", "-t", childPane, "cd "+otherRoot)
					f.run(t, "send-keys", "-t", childPane, "C-m")
					deadline := time.Now().Add(time.Second)
					for time.Now().Before(deadline) {
						if f.run(t, "display-message", "-p", "-t", childPane, "#{pane_current_path}") == otherRoot {
							return nil
						}
						time.Sleep(10 * time.Millisecond)
					}
					t.Fatal("owned child pane did not change root")
				case "middle":
					pane := f.run(t, "new-session", "-d", "-s", "middle", "-c", branchRoot,
						"-P", "-F", "#{pane_id}", "exec sleep 120")
					f.run(t, "select-pane", "-t", pane, "-T", "worker")
				}
				return nil
			}
			var rejected Message
			if tc.reply {
				rejected, err = m.SendCrossReply(ctx, source, destination, []string{request.ID}, "answer")
			} else {
				rejected, err = m.SendCrossWithReply(ctx, source, destination, "question", "", "", true)
			}
			if err == nil || rejected.ID == "" {
				t.Fatalf("changed relation published: %+v %v", rejected, err)
			}
			sourceRoot, destinationRoot := "~/repo", "~/repo/branch/leaf"
			if tc.reply {
				sourceRoot, destinationRoot = destinationRoot, sourceRoot
			}
			sourceName, _ := Name(sourceRoot)
			destinationName, _ := Name(destinationRoot)
			for _, path := range []string{
				filepath.Join(m.Store.StateRoot, sourceName, "agents", "worker", "sent", rejected.ID+".md"),
				filepath.Join(m.Store.StateRoot, destinationName, "agents", "worker", "inbox", rejected.ID+".md"),
			} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("rejected message was published at %s: %v", path, err)
				}
			}
		})
	}
}
