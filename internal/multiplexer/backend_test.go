package multiplexer

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/tmuxtest"
)

func TestTmuxBackendCapturePaneUsesVisibleCaptureArgs(t *testing.T) {
	fake := tmuxtest.Install(t, tmuxtest.WithCommand(tmuxtest.Command{
		Args:   []string{"capture-pane", "-p", "-t", "%11"},
		Stdout: "pane content",
	}))

	got, err := (TmuxBackend{}).CapturePane(context.Background(), TmuxPaneID("%11"), CaptureOptions{})
	if err != nil {
		t.Fatalf("CapturePane() error = %v", err)
	}
	if got != "pane content" {
		t.Fatalf("CapturePane() = %q, want %q", got, "pane content")
	}
	if gotArgs := fake.Invocations(); !reflect.DeepEqual(gotArgs, []string{"capture-pane -p -t %11"}) {
		t.Fatalf("invocations = %#v", gotArgs)
	}
}

func TestTmuxBackendCapturePaneUsesTailAndHistoryArgs(t *testing.T) {
	tests := []struct {
		name string
		opts CaptureOptions
		want string
	}{
		{name: "tail", opts: CaptureOptions{TailLines: 100}, want: "capture-pane -p -t %11 -S -100"},
		{name: "history", opts: CaptureOptions{History: true}, want: "capture-pane -p -t %11 -S -"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := tmuxtest.Install(t, tmuxtest.WithCommand(tmuxtest.Command{
				Args: []string{"capture-pane", "-p", "-t", "%11", "-S", lastField(tt.want)},
			}))

			if _, err := (TmuxBackend{}).CapturePane(context.Background(), TmuxPaneID("%11"), tt.opts); err != nil {
				t.Fatalf("CapturePane() error = %v", err)
			}
			if got := fake.Invocations(); !reflect.DeepEqual(got, []string{tt.want}) {
				t.Fatalf("invocations = %#v, want %#v", got, []string{tt.want})
			}
		})
	}
}

func TestTmuxBackendPaneCurrentCommandTrimsOutput(t *testing.T) {
	tmuxtest.Install(t, tmuxtest.WithCommand(tmuxtest.Command{
		Args:   []string{"display-message", "-t", "%11", "-p", "#{pane_current_command}"},
		Stdout: "codex\n",
	}))

	got, err := (TmuxBackend{}).PaneCurrentCommand(context.Background(), TmuxPaneID("%11"))
	if err != nil {
		t.Fatalf("PaneCurrentCommand() error = %v", err)
	}
	if got != "codex" {
		t.Fatalf("PaneCurrentCommand() = %q, want codex", got)
	}
}

func TestTmuxBackendCapturePanePropagatesTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()

	_, err := (TmuxBackend{}).CapturePane(ctx, TmuxPaneID("%11"), CaptureOptions{})
	if err == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("CapturePane() error = nil after context deadline")
	}
}

func lastField(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ' ' {
			return s[i+1:]
		}
	}
	return s
}
