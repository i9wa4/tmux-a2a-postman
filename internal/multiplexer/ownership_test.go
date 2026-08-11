package multiplexer

import (
	"context"
	"reflect"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/tmuxtest"
)

func TestTmuxBackendSessionOwnerMarkerUsesGlobalOption(t *testing.T) {
	tmuxtest.Install(t, tmuxtest.WithCommand(tmuxtest.Command{
		Args:   []string{"show-options", "-gqv", "@a2a_session_on_managed"},
		Stdout: "ctx-owner:43210\n",
	}))

	got, err := (TmuxBackend{}).SessionOwnerMarker(context.Background(), "managed")
	if err != nil {
		t.Fatalf("SessionOwnerMarker() error = %v", err)
	}
	if got != "ctx-owner:43210" {
		t.Fatalf("SessionOwnerMarker() = %q, want ctx-owner:43210", got)
	}
}

func TestTmuxBackendSetAndClearSessionOwnerMarker(t *testing.T) {
	fake := tmuxtest.Install(
		t,
		tmuxtest.WithCommand(tmuxtest.Command{
			Args: []string{"set-option", "-g", "@a2a_session_on_managed", "ctx-owner:1234"},
		}),
		tmuxtest.WithCommand(tmuxtest.Command{
			Args: []string{"set-option", "-gu", "@a2a_session_on_managed"},
		}),
	)
	backend := TmuxBackend{}

	if err := backend.SetSessionOwnerMarker(context.Background(), "ctx-owner", "managed", 1234); err != nil {
		t.Fatalf("SetSessionOwnerMarker() error = %v", err)
	}
	if err := backend.ClearSessionOwnerMarker(context.Background(), "managed"); err != nil {
		t.Fatalf("ClearSessionOwnerMarker() error = %v", err)
	}
	want := []string{
		"set-option -g @a2a_session_on_managed ctx-owner:1234",
		"set-option -gu @a2a_session_on_managed",
	}
	if got := fake.Invocations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("invocations = %#v, want %#v", got, want)
	}
}

func TestTmuxBackendPaneOwnerMarkerUsesPaneOption(t *testing.T) {
	tmuxtest.Install(t, tmuxtest.WithCommand(tmuxtest.Command{
		Args:   []string{"show-options", "-p", "-v", "-t", "%9", "@a2a_context_id"},
		Stdout: "ctx-owner\n",
	}))

	got, err := (TmuxBackend{}).PaneOwnerMarker(context.Background(), TmuxPaneID("%9"))
	if err != nil {
		t.Fatalf("PaneOwnerMarker() error = %v", err)
	}
	if got != "ctx-owner" {
		t.Fatalf("PaneOwnerMarker() = %q, want ctx-owner", got)
	}
}

func TestTmuxBackendSetAndClearPaneOwnerMarker(t *testing.T) {
	fake := tmuxtest.Install(
		t,
		tmuxtest.WithCommand(tmuxtest.Command{
			Args: []string{"set-option", "-p", "-t", "%9", "@a2a_context_id", "ctx-owner"},
		}),
		tmuxtest.WithCommand(tmuxtest.Command{
			Args: []string{"set-option", "-p", "-u", "-t", "%9", "@a2a_context_id"},
		}),
	)
	backend := TmuxBackend{}

	if err := backend.SetPaneOwnerMarker(context.Background(), TmuxPaneID("%9"), "ctx-owner"); err != nil {
		t.Fatalf("SetPaneOwnerMarker() error = %v", err)
	}
	if err := backend.ClearPaneOwnerMarker(context.Background(), TmuxPaneID("%9")); err != nil {
		t.Fatalf("ClearPaneOwnerMarker() error = %v", err)
	}
	want := []string{
		"set-option -p -t %9 @a2a_context_id ctx-owner",
		"set-option -p -u -t %9 @a2a_context_id",
	}
	if got := fake.Invocations(); !reflect.DeepEqual(got, want) {
		t.Fatalf("invocations = %#v, want %#v", got, want)
	}
}
