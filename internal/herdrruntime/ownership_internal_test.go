package herdrruntime

import (
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/multiplexer"
)

func TestOwnershipMuxReplaceSnapshotBuildsClearBackendFromDeterministicLiveSurvivor(t *testing.T) {
	mux := newOwnershipMux("work", true)
	later := multiplexer.HerdrBackend{Config: multiplexer.HerdrReadConfig{Runtime: multiplexer.HerdrRuntimeIdentity{
		SessionName: "work",
		PaneID:      "workspace-1:pane-z",
	}}}
	survivor := multiplexer.HerdrBackend{Config: multiplexer.HerdrReadConfig{Runtime: multiplexer.HerdrRuntimeIdentity{
		SessionName: "work",
		PaneID:      "workspace-1:pane-a",
	}}}

	mux.replaceSnapshot(map[herdrOwnershipKey]multiplexer.HerdrBackend{
		herdrOwnershipKeyForBackend(later, "workspace-1:pane-z"):    later,
		herdrOwnershipKeyForBackend(survivor, "workspace-1:pane-a"): survivor,
	}, multiplexer.HerdrBackend{})

	clearBackend, err := mux.backendForSessionClear("work")
	if err != nil {
		t.Fatalf("backendForSessionClear() error = %v", err)
	}
	if got := clearBackend.Config.Runtime.PaneID; got != "workspace-1:pane-a" {
		t.Fatalf("clear backend pane = %q, want live survivor", got)
	}
	sessionBackend, err := mux.backendForSession("work")
	if err != nil {
		t.Fatalf("backendForSession() error = %v", err)
	}
	if got := sessionBackend.Config.Runtime.PaneID; got != "workspace-1:pane-a" {
		t.Fatalf("session backend pane = %q, want same live survivor", got)
	}
}
