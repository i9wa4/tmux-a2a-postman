package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
)

func TestDiagnosticDestinationPrivateExclusiveAndSymlinkSafe(t *testing.T) {
	m := mailFixture(t)
	rt := mailRuntime("worker")
	id := "inv-" + strings.Repeat("a", 32)
	f, err := m.Store.OpenDiagnostic(context.Background(), rt, "2026-11-01", id)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	path := filepath.Join(m.Store.StateRoot, "repo", "agents", "worker", "logs", "2026-11-01", id+".jsonl")
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal(info, err)
	}
	for _, dir := range []string{filepath.Dir(path), filepath.Dir(filepath.Dir(path))} {
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatal(info, err)
		}
	}
	if _, err := m.Store.OpenDiagnostic(context.Background(), rt, "2026-11-01", id); !errors.Is(err, os.ErrExist) {
		t.Fatal("collision overwrote existing file", err)
	}
	// A pre-created symlink must not be followed even with an otherwise valid ID.
	id2 := "inv-" + strings.Repeat("b", 32)
	outside := filepath.Join(t.TempDir(), "sentinel")
	if err := os.WriteFile(outside, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(filepath.Dir(path), id2+".jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Store.OpenDiagnostic(context.Background(), rt, "2026-11-01", id2); err == nil {
		t.Fatal("followed symlink")
	}
	b, _ := os.ReadFile(outside)
	if string(b) != "unchanged" {
		t.Fatal("modified symlink target")
	}
	for _, date := range []string{"../escape", "2026-02-30", "2026-1-1"} {
		if _, err := m.Store.OpenDiagnostic(context.Background(), rt, date, id2); err == nil {
			t.Fatal(date)
		}
	}
}

func TestUnverifiedDiagnosticDestinationDoesNotInitializeState(t *testing.T) {
	m := mailFixture(t)
	rt := mailRuntime("worker")
	rt.snapshot.Caller.Role = "not-a-bound-node"
	if _, err := m.Store.OpenDiagnostic(context.Background(), rt, "2026-11-01", "inv-"+strings.Repeat("a", 32)); err == nil {
		t.Fatal("accepted unknown actor")
	}
	if _, err := os.Stat(m.Store.StateRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unverified diagnostics wrote state", err)
	}
}

func TestDiagnosticWritesCannotExtendMutationCriticalSection(t *testing.T) {
	m := mailFixture(t)
	r := diagnostic.New("send-heredoc", "test", "test")
	m.Diagnostics = r
	// This writer checks the actual mailbox lock by a second independent Store
	// invocation. It could not acquire admission if called under the first lock.
	sink := &lockCheckingSink{t: t, store: m.Store}
	r.Attach(sink, diagnostic.Identity{})
	if _, err := m.Send(diagnostic.With(context.Background(), r), mailRuntime("worker"), "peer", "privatebody", "", true); err != nil {
		t.Fatal(err)
	}
	r.End("ok", diagnostic.Facts{})
	if sink.calls == 0 {
		t.Fatal("no diagnostic writes checked")
	}
}

type lockCheckingSink struct {
	t     *testing.T
	store Store
	calls int
}

func (s *lockCheckingSink) Write(b []byte) (int, error) {
	s.calls++
	if err := s.store.WithVerified(context.Background(), mailRuntime("worker"), noMutation); err != nil {
		s.t.Fatalf("log write held mailbox lock: %v", err)
	}
	return len(b), nil
}
func (s *lockCheckingSink) Close() error { return nil }
