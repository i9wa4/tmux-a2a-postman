package herdrruntime

import (
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/multiplexer"
)

func TestOwnershipMuxDeletePaneBackendRebuildsClearBackendFromLiveSurvivor(t *testing.T) {
	mux := newOwnershipMux("work")
	clearSelected := multiplexer.HerdrBackend{Config: multiplexer.HerdrReadConfig{Runtime: multiplexer.HerdrRuntimeIdentity{
		SessionName: "work",
		PaneID:      "workspace-1:pane-clear",
	}}}
	survivor := multiplexer.HerdrBackend{Config: multiplexer.HerdrReadConfig{Runtime: multiplexer.HerdrRuntimeIdentity{
		SessionName: "work",
		PaneID:      "workspace-1:pane-survivor",
	}}}

	mux.setPaneBackend("workspace-1:pane-survivor", survivor)
	mux.setPaneBackend("workspace-1:pane-clear", clearSelected)
	mux.deletePaneBackend("workspace-1:pane-clear")

	clearBackend, err := mux.backendForSessionClear("work")
	if err != nil {
		t.Fatalf("backendForSessionClear() error = %v", err)
	}
	if got := clearBackend.Config.Runtime.PaneID; got != "workspace-1:pane-survivor" {
		t.Fatalf("clear backend pane = %q, want live survivor", got)
	}
	sessionBackend, err := mux.backendForSession("work")
	if err != nil {
		t.Fatalf("backendForSession() error = %v", err)
	}
	if got := sessionBackend.Config.Runtime.PaneID; got != "workspace-1:pane-survivor" {
		t.Fatalf("session backend pane = %q, want same live survivor", got)
	}
}
