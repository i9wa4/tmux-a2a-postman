package multiplexer

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/tmuxtest"
)

func TestTmuxBackendCurrentIdentityUsesTargetPane(t *testing.T) {
	fake := tmuxtest.Install(
		t,
		tmuxtest.WithCommand(tmuxtest.Command{
			Args:   []string{"display-message", "-t", "%9", "-p", "#{session_name}"},
			Stdout: "postman\n",
		}),
		tmuxtest.WithCommand(tmuxtest.Command{
			Args:   []string{"display-message", "-t", "%9", "-p", "#{pane_title}"},
			Stdout: "worker\n",
		}),
	)

	identity, err := (TmuxBackend{}).CurrentIdentity(context.Background(), "%9")
	if err != nil {
		t.Fatalf("CurrentIdentity() error = %v", err)
	}
	if identity.Backend != BackendKindTmux {
		t.Fatalf("Backend = %q, want %q", identity.Backend, BackendKindTmux)
	}
	if identity.SessionName != "postman" || identity.NodeName != "worker" {
		t.Fatalf("identity session/node = %q/%q, want postman/worker", identity.SessionName, identity.NodeName)
	}
	if identity.Pane != TmuxPaneID("%9") {
		t.Fatalf("Pane = %#v, want %#v", identity.Pane, TmuxPaneID("%9"))
	}
	wantNative := map[string]string{
		"pane_id":      "%9",
		"session_name": "postman",
		"pane_title":   "worker",
	}
	if !reflect.DeepEqual(identity.NativeIDs, wantNative) {
		t.Fatalf("NativeIDs = %#v, want %#v", identity.NativeIDs, wantNative)
	}
	wantInvocations := []string{
		"display-message -t %9 -p #{session_name}",
		"display-message -t %9 -p #{pane_title}",
	}
	if got := fake.Invocations(); !reflect.DeepEqual(got, wantInvocations) {
		t.Fatalf("invocations = %#v, want %#v", got, wantInvocations)
	}
}

func TestTmuxBackendCurrentIdentityUsesUntargetedFallback(t *testing.T) {
	fake := tmuxtest.Install(
		t,
		tmuxtest.WithCommand(tmuxtest.Command{
			Args:   []string{"display-message", "-p", "#{pane_id}"},
			Stdout: "%11\n",
		}),
		tmuxtest.WithCommand(tmuxtest.Command{
			Args:   []string{"display-message", "-t", "%11", "-p", "#{session_name}"},
			Stdout: "postman\n",
		}),
		tmuxtest.WithCommand(tmuxtest.Command{
			Args:   []string{"display-message", "-t", "%11", "-p", "#{pane_title}"},
			Stdout: "orchestrator\n",
		}),
	)

	identity, err := (TmuxBackend{}).CurrentIdentity(context.Background(), "")
	if err != nil {
		t.Fatalf("CurrentIdentity() error = %v", err)
	}
	if identity.Pane.Native != "%11" || identity.SessionName != "postman" || identity.NodeName != "orchestrator" {
		t.Fatalf("identity = %#v, want pane/session/node %q/%q/%q", identity, "%11", "postman", "orchestrator")
	}
	wantInvocations := []string{
		"display-message -p #{pane_id}",
		"display-message -t %11 -p #{session_name}",
		"display-message -t %11 -p #{pane_title}",
	}
	if got := fake.Invocations(); !reflect.DeepEqual(got, wantInvocations) {
		t.Fatalf("invocations = %#v, want %#v", got, wantInvocations)
	}
}

func TestTmuxBackendCurrentIdentityReportsLookupFailure(t *testing.T) {
	tmuxtest.Install(t, tmuxtest.WithCommand(tmuxtest.Command{
		Args:     []string{"display-message", "-t", "%9", "-p", "#{session_name}"},
		Stderr:   "no pane\n",
		ExitCode: 1,
	}))

	_, err := (TmuxBackend{}).CurrentIdentity(context.Background(), "%9")
	if err == nil {
		t.Fatal("CurrentIdentity() error = nil, want lookup failure")
	}
	var identityErr IdentityError
	if !errors.As(err, &identityErr) {
		t.Fatalf("error = %T %v, want IdentityError", err, err)
	}
	if identityErr.Backend != BackendKindTmux || identityErr.Failure != IdentityFailureLookupFailed || identityErr.Field != "session_name" {
		t.Fatalf("identity error = %#v, want tmux lookup_failed session_name", identityErr)
	}
}
