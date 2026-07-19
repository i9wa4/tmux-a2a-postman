package multiplexer

import (
	"context"
	"reflect"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/tmuxtest"
)

func TestTmuxBackendSessionLayoutReturnsOrderedWindowGroups(t *testing.T) {
	fake := tmuxtest.Install(
		t,
		tmuxtest.WithCommand(tmuxtest.Command{
			Args:   []string{"list-windows", "-t", "review", "-F", "#{window_index}"},
			Stdout: "1\n0\n",
		}),
		tmuxtest.WithCommand(tmuxtest.Command{
			Args:   []string{"list-panes", "-t", "review:1", "-F", "#{window_index}\t#{pane_index}\t#{pane_id}\t#{pane_title}\t#{pane_current_command}"},
			Stdout: "1\t1\t%14\tcritic\tclaude\n1\t0\t%13\tworker\tcodex\n",
		}),
		tmuxtest.WithCommand(tmuxtest.Command{
			Args:   []string{"list-panes", "-t", "review:0", "-F", "#{window_index}\t#{pane_index}\t#{pane_id}\t#{pane_title}\t#{pane_current_command}"},
			Stdout: "0\t0\t%11\torchestrator\tclaude\n",
		}),
	)

	got, err := (TmuxBackend{}).SessionLayout(context.Background(), "review")
	if err != nil {
		t.Fatalf("SessionLayout() error = %v", err)
	}

	if got.Backend != BackendKindTmux || got.SessionName != "review" {
		t.Fatalf("layout identity = %#v", got)
	}
	if len(got.Groups) != 2 {
		t.Fatalf("len(Groups) = %d, want 2: %#v", len(got.Groups), got.Groups)
	}
	if got.Groups[0].NativeIDs["window_index"] != "0" || got.Groups[1].NativeIDs["window_index"] != "1" {
		t.Fatalf("window order/native ids = %#v", got.Groups)
	}
	if got.Groups[1].ID.Kind != ResourceKindWindow || got.Groups[1].ID.Native != "review:1" {
		t.Fatalf("window group ID = %#v, want tmux window resource", got.Groups[1].ID)
	}
	if len(got.Groups[1].Items) != 2 {
		t.Fatalf("len(Groups[1].Items) = %d, want 2", len(got.Groups[1].Items))
	}
	if got.Groups[1].Items[0].ID.Native != "%13" || got.Groups[1].Items[0].LogicalName != "worker" || got.Groups[1].Items[0].CurrentCommand != "codex" {
		t.Fatalf("ordered pane item = %#v", got.Groups[1].Items[0])
	}

	wantInvocations := []string{
		"list-windows -t review -F #{window_index}",
		"list-panes -t review:1 -F #{window_index}\t#{pane_index}\t#{pane_id}\t#{pane_title}\t#{pane_current_command}",
		"list-panes -t review:0 -F #{window_index}\t#{pane_index}\t#{pane_id}\t#{pane_title}\t#{pane_current_command}",
	}
	if invocations := fake.Invocations(); !reflect.DeepEqual(invocations, wantInvocations) {
		t.Fatalf("invocations = %#v, want %#v", invocations, wantInvocations)
	}
}

func TestTmuxBackendSessionLayoutMissingSessionReturnsEmptyLayout(t *testing.T) {
	tmuxtest.Install(t, tmuxtest.WithCommand(tmuxtest.Command{
		Args:     []string{"list-windows", "-t", "missing", "-F", "#{window_index}"},
		Stderr:   "can't find session: missing",
		ExitCode: 1,
	}))

	got, err := (TmuxBackend{}).SessionLayout(context.Background(), "missing")
	if err != nil {
		t.Fatalf("SessionLayout() error = %v", err)
	}
	if got.Backend != BackendKindTmux || got.SessionName != "missing" || len(got.Groups) != 0 {
		t.Fatalf("SessionLayout() = %#v, want empty tmux layout", got)
	}
}

func TestTmuxBackendSessionLayoutServerStopsDuringPaneEnumerationReturnsEmptyLayout(t *testing.T) {
	tmuxtest.Install(
		t,
		tmuxtest.WithCommand(tmuxtest.Command{
			Args:   []string{"list-windows", "-t", "review", "-F", "#{window_index}"},
			Stdout: "0\n1\n",
		}),
		tmuxtest.WithCommand(tmuxtest.Command{
			Args:   []string{"list-panes", "-t", "review:0", "-F", "#{window_index}\t#{pane_index}\t#{pane_id}\t#{pane_title}\t#{pane_current_command}"},
			Stdout: "0\t0\t%11\tworker\tcodex\n",
		}),
		tmuxtest.WithCommand(tmuxtest.Command{
			Args:     []string{"list-panes", "-t", "review:1", "-F", "#{window_index}\t#{pane_index}\t#{pane_id}\t#{pane_title}\t#{pane_current_command}"},
			Stderr:   "no server running on /tmp/tmux-1000/default",
			ExitCode: 1,
		}),
	)

	got, err := (TmuxBackend{}).SessionLayout(context.Background(), "review")
	if err != nil {
		t.Fatalf("SessionLayout() error = %v", err)
	}
	if got.Backend != BackendKindTmux || got.SessionName != "review" || len(got.Groups) != 0 {
		t.Fatalf("SessionLayout() = %#v, want empty layout after tmux server disappears", got)
	}
}
