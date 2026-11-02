package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestHomeCreatedSessionsUseAgentPaneDirectoriesWithoutBootPreparation(t *testing.T) {
	f := newTmuxFixture(t)
	other := filepath.Join(f.home, "dotfiles")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	type team struct {
		runtime  Tmux
		receiver string
	}
	boot := func(name, directory string) team {
		// Match direct vde-layout new-window after navigation: session creation
		// happened in HOME; the agent consoles run in the selected repository.
		shell := f.run(t, "new-session", "-d", "-s", name, "-c", f.home, "-P", "-F", "#{pane_id}", "exec sleep 120")
		f.run(t, "select-pane", "-t", shell, "-T", "pane")
		worker := f.run(t, "new-window", "-d", "-t", name, "-c", directory, "-P", "-F", "#{pane_id}", "exec sleep 120")
		receiver := f.run(t, "split-window", "-d", "-t", worker, "-c", directory, "-P", "-F", "#{pane_id}", "exec sleep 120")
		f.run(t, "select-pane", "-t", worker, "-T", "worker")
		f.run(t, "select-pane", "-t", receiver, "-T", "approver")
		if got := f.run(t, "display-message", "-p", "-t", worker, "#{session_path}"); got != f.home {
			t.Fatal("fixture changed native session creation path", got)
		}
		return team{Tmux{Socket: f.socket, Pane: worker, Home: f.home, Roles: []string{"worker", "approver"}}, receiver}
	}
	first, second := boot("repo-team", f.repo), boot("dotfiles-team", other)
	m := mailFixture(t)
	for i, entry := range []struct {
		team team
		root string
	}{{first, "~/repo"}, {second, "~/dotfiles"}} {
		snapshot, err := entry.team.runtime.Snapshot(context.Background())
		if err != nil || snapshot.Root != entry.root {
			t.Fatal(i, snapshot, err)
		}
		msg, err := m.Send(context.Background(), entry.team.runtime, "approver", "distinct native workspace", "", true)
		if err != nil {
			t.Fatal(i, err)
		}
		receiver := entry.team.runtime
		receiver.Pane = entry.team.receiver
		claimed, err := m.Claim(context.Background(), receiver, "")
		if err != nil || claimed == nil || claimed.Message.ID != msg.ID {
			t.Fatal(i, claimed, err)
		}
	}
	if _, err := os.Stat(filepath.Join(m.Store.StateRoot, "%00")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("HOME mailbox invented", err)
	}
	// Correct directory selection does not weaken a foreign live owner's lock.
	contender := boot("other-repo-team", f.repo)
	if _, err := m.Send(context.Background(), contender.runtime, "approver", "must not take over", "", true); !errors.Is(err, ErrOwned) {
		t.Fatal("live owner bypassed", err)
	}
}

func TestForeignNativeRoleDirectoryCannotBecomeAbsentApprover(t *testing.T) {
	f := newTmuxFixture(t)
	other := filepath.Join(f.home, "other")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	peer := f.run(t, "new-window", "-d", "-t", "one", "-c", other, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", peer, "-T", "approver")
	rt := f.adapter()
	m := mailFixture(t)
	snapshot, err := rt.Snapshot(context.Background())
	if err != nil || snapshot.Root != "~/repo" || len(snapshot.Bindings) != 1 {
		t.Fatal(snapshot, err)
	}
	for _, role := range snapshot.AbsentRoles {
		if role == "approver" {
			t.Fatal("present foreign approver became verified absent")
		}
	}
	config := filepath.Join(t.TempDir(), "postman.md")
	if err := os.WriteFile(config, []byte("## `edges`\n```mermaid\ngraph LR\nworker --- approver:::command_approver_node\n```\n## `worker`\nWorker\n## `approver`\nApprover\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ran := false
	e := Executor{Mail: m, Resolve: ConfiguredApprovalResolver(config), run: func(context.Context, string, io.Writer, io.Writer) (int, error) { ran = true; return 0, nil }}
	result, err := e.Execute(context.Background(), rt, "must not run")
	if err == nil || ran || result.Executed == nil || *result.Executed {
		t.Fatal("mixed directory downgraded approval", result, err, ran)
	}
	rows, err := m.OperatorStatus(context.Background(), rt)
	if err != nil || len(rows) != 2 {
		t.Fatal(rows, err)
	}
	for _, row := range rows {
		if row.Status.UnreadCount != nil || row.Status.Reason == "" {
			t.Fatal("mixed role directory looked healthy", row)
		}
	}
	if _, err := os.Stat(m.Store.StateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read-only diagnosis created state", err)
	}
}
