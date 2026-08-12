package multiplexer

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"
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

func TestRegisterOwnershipBackendKeepsConcurrentHerdrBackendsAddressable(t *testing.T) {
	const backendCount = 1000
	cleanups := make([]func(), backendCount)
	var wg sync.WaitGroup
	for i := 0; i < backendCount; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			cleanups[i] = RegisterOwnershipBackend(&testOwnershipBackend{
				kind:        BackendKindHerdr,
				sessionName: "session-" + strconv.Itoa(i),
				owner:       "ctx-" + strconv.Itoa(i) + ":1",
			})
		}()
	}
	wg.Wait()
	t.Cleanup(func() {
		for _, cleanup := range cleanups {
			if cleanup != nil {
				cleanup()
			}
		}
	})

	backend, err := OwnershipBackendForKind(BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	for i := 0; i < backendCount; i++ {
		sessionName := "session-" + strconv.Itoa(i)
		want := "ctx-" + strconv.Itoa(i) + ":1"
		got, err := backend.SessionOwnerMarker(context.Background(), sessionName)
		if err != nil {
			t.Fatalf("SessionOwnerMarker(%q) error = %v", sessionName, err)
		}
		if got != want {
			t.Fatalf("SessionOwnerMarker(%q) = %q, want %q", sessionName, got, want)
		}
	}
}

func TestHerdrOwnershipCompositeSkipsEmptySessionMarker(t *testing.T) {
	emptyCleanup := RegisterOwnershipBackend(&testOwnershipBackend{
		kind:        BackendKindHerdr,
		sessionName: "work",
		owner:       "",
	})
	t.Cleanup(emptyCleanup)
	healthyCleanup := RegisterOwnershipBackend(&testOwnershipBackend{
		kind:        BackendKindHerdr,
		sessionName: "work",
		owner:       "ctx-work:1",
	})
	t.Cleanup(healthyCleanup)

	backend, err := OwnershipBackendForKind(BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	got, err := backend.SessionOwnerMarker(context.Background(), "work")
	if err != nil {
		t.Fatalf("SessionOwnerMarker(work) error = %v", err)
	}
	if got != "ctx-work:1" {
		t.Fatalf("SessionOwnerMarker(work) = %q, want later healthy backend marker", got)
	}

	healthyCleanup()
	backend, err = OwnershipBackendForKind(BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr after healthy cleanup) error = %v", err)
	}
	got, err = backend.SessionOwnerMarker(context.Background(), "work")
	if err != nil {
		t.Fatalf("SessionOwnerMarker(work after healthy cleanup) error = %v", err)
	}
	if got != "" {
		t.Fatalf("SessionOwnerMarker(work after healthy cleanup) = %q, want empty marker", got)
	}
}

func TestHerdrOwnershipCompositeSetUsesNonEmptySessionBackend(t *testing.T) {
	empty := &testOwnershipBackend{
		kind:        BackendKindHerdr,
		sessionName: "work",
		owner:       "",
	}
	emptyCleanup := RegisterOwnershipBackend(empty)
	t.Cleanup(emptyCleanup)
	healthy := &testOwnershipBackend{
		kind:        BackendKindHerdr,
		sessionName: "work",
		owner:       "ctx-old:1",
	}
	healthyCleanup := RegisterOwnershipBackend(healthy)
	t.Cleanup(healthyCleanup)

	backend, err := OwnershipBackendForKind(BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if err := backend.SetSessionOwnerMarker(context.Background(), "ctx-new", "work", 1234); err != nil {
		t.Fatalf("SetSessionOwnerMarker(work) error = %v", err)
	}
	if empty.setCalls != 0 {
		t.Fatalf("empty backend set calls = %d, want 0", empty.setCalls)
	}
	if healthy.setCalls != 1 || healthy.owner != "ctx-new:1234" {
		t.Fatalf("healthy set calls=%d owner=%q, want one mutation to selected backend", healthy.setCalls, healthy.owner)
	}
}

func TestHerdrOwnershipCompositeClearUsesNonEmptySessionBackend(t *testing.T) {
	empty := &testOwnershipBackend{
		kind:        BackendKindHerdr,
		sessionName: "work",
		owner:       "",
	}
	emptyCleanup := RegisterOwnershipBackend(empty)
	t.Cleanup(emptyCleanup)
	healthy := &testOwnershipBackend{
		kind:        BackendKindHerdr,
		sessionName: "work",
		owner:       "ctx-old:1",
	}
	healthyCleanup := RegisterOwnershipBackend(healthy)
	t.Cleanup(healthyCleanup)

	backend, err := OwnershipBackendForKind(BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if err := backend.ClearSessionOwnerMarker(context.Background(), "work"); err != nil {
		t.Fatalf("ClearSessionOwnerMarker(work) error = %v", err)
	}
	if empty.clearCalls != 0 {
		t.Fatalf("empty backend clear calls = %d, want 0", empty.clearCalls)
	}
	if healthy.clearCalls != 1 || healthy.owner != "" {
		t.Fatalf("healthy clear calls=%d owner=%q, want one clear on selected backend", healthy.clearCalls, healthy.owner)
	}
}

func TestHerdrOwnershipCompositeClearFallsBackToEmptySurvivor(t *testing.T) {
	empty := &testOwnershipBackend{
		kind:        BackendKindHerdr,
		sessionName: "work",
		owner:       "",
	}
	emptyCleanup := RegisterOwnershipBackend(empty)
	t.Cleanup(emptyCleanup)
	healthy := &testOwnershipBackend{
		kind:        BackendKindHerdr,
		sessionName: "work",
		owner:       "ctx-old:1",
	}
	healthyCleanup := RegisterOwnershipBackend(healthy)
	t.Cleanup(healthyCleanup)
	healthyCleanup()

	backend, err := OwnershipBackendForKind(BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if err := backend.ClearSessionOwnerMarker(context.Background(), "work"); err != nil {
		t.Fatalf("ClearSessionOwnerMarker(work) with empty survivor error = %v", err)
	}
	if empty.clearCalls != 1 {
		t.Fatalf("empty survivor clear calls = %d, want 1", empty.clearCalls)
	}
}

func TestHerdrOwnershipCompositeSetDoesNotFallThroughWhenAuthoritativeNonEmptyFails(t *testing.T) {
	mutationErr := errors.New("authoritative set rejected")
	authoritative := &testOwnershipBackend{
		kind:        BackendKindHerdr,
		sessionName: "work",
		owner:       "ctx-authoritative:1",
		setErr:      mutationErr,
	}
	authoritativeCleanup := RegisterOwnershipBackend(authoritative)
	t.Cleanup(authoritativeCleanup)
	fallback := &testOwnershipBackend{
		kind:        BackendKindHerdr,
		sessionName: "work",
		owner:       "ctx-fallback:1",
	}
	fallbackCleanup := RegisterOwnershipBackend(fallback)
	t.Cleanup(fallbackCleanup)

	backend, err := OwnershipBackendForKind(BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if err := backend.SetSessionOwnerMarker(context.Background(), "ctx-new", "work", 1234); !errors.Is(err, mutationErr) {
		t.Fatalf("SetSessionOwnerMarker(work) error = %v, want authoritative mutation error", err)
	}
	if authoritative.setCalls != 1 || authoritative.owner != "ctx-authoritative:1" {
		t.Fatalf("authoritative set calls=%d owner=%q, want failed mutation only on read winner", authoritative.setCalls, authoritative.owner)
	}
	if fallback.setCalls != 0 || fallback.owner != "ctx-fallback:1" {
		t.Fatalf("fallback set calls=%d owner=%q, want no fallthrough mutation", fallback.setCalls, fallback.owner)
	}
}

func TestHerdrOwnershipCompositeClearDoesNotFallThroughWhenAuthoritativeNonEmptyFails(t *testing.T) {
	mutationErr := errors.New("authoritative clear rejected")
	authoritative := &testOwnershipBackend{
		kind:        BackendKindHerdr,
		sessionName: "work",
		owner:       "ctx-authoritative:1",
		clearErr:    mutationErr,
	}
	authoritativeCleanup := RegisterOwnershipBackend(authoritative)
	t.Cleanup(authoritativeCleanup)
	fallback := &testOwnershipBackend{
		kind:        BackendKindHerdr,
		sessionName: "work",
		owner:       "ctx-fallback:1",
	}
	fallbackCleanup := RegisterOwnershipBackend(fallback)
	t.Cleanup(fallbackCleanup)

	backend, err := OwnershipBackendForKind(BackendKindHerdr)
	if err != nil {
		t.Fatalf("OwnershipBackendForKind(herdr) error = %v", err)
	}
	if err := backend.ClearSessionOwnerMarker(context.Background(), "work"); !errors.Is(err, mutationErr) {
		t.Fatalf("ClearSessionOwnerMarker(work) error = %v, want authoritative mutation error", err)
	}
	if authoritative.clearCalls != 1 || authoritative.owner != "ctx-authoritative:1" {
		t.Fatalf("authoritative clear calls=%d owner=%q, want failed mutation only on read winner", authoritative.clearCalls, authoritative.owner)
	}
	if fallback.clearCalls != 0 || fallback.owner != "ctx-fallback:1" {
		t.Fatalf("fallback clear calls=%d owner=%q, want no fallthrough mutation", fallback.clearCalls, fallback.owner)
	}
}

type testOwnershipBackend struct {
	kind        BackendKind
	sessionName string
	owner       string
	setErr      error
	clearErr    error
	setCalls    int
	clearCalls  int
}

func (t *testOwnershipBackend) Kind() BackendKind {
	return t.kind
}

func (t *testOwnershipBackend) SessionOwnerMarker(_ context.Context, sessionName string) (string, error) {
	if sessionName != t.sessionName {
		return "", fmt.Errorf("session %q not owned", sessionName)
	}
	return t.owner, nil
}

func (t *testOwnershipBackend) SetSessionOwnerMarker(_ context.Context, contextID string, sessionName string, pid int) error {
	if sessionName != t.sessionName {
		return fmt.Errorf("session %q not owned", sessionName)
	}
	t.setCalls++
	if t.setErr != nil {
		return t.setErr
	}
	t.owner = contextID + ":" + strconv.Itoa(pid)
	return nil
}

func (t *testOwnershipBackend) ClearSessionOwnerMarker(_ context.Context, sessionName string) error {
	if sessionName != t.sessionName {
		return fmt.Errorf("session %q not owned", sessionName)
	}
	t.clearCalls++
	if t.clearErr != nil {
		return t.clearErr
	}
	t.owner = ""
	return nil
}

func (t *testOwnershipBackend) PaneOwnerMarker(context.Context, ResourceID) (string, error) {
	return "", fmt.Errorf("pane not owned")
}

func (t *testOwnershipBackend) SetPaneOwnerMarker(context.Context, ResourceID, string) error {
	return fmt.Errorf("pane not owned")
}

func (t *testOwnershipBackend) ClearPaneOwnerMarker(context.Context, ResourceID) error {
	return fmt.Errorf("pane not owned")
}
