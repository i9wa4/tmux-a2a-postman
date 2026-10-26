package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func mailRuntime(role string) *storedRuntime {
	r := storageRuntime()
	worker := r.snapshot.Caller
	peer := Binding{Role: "peer", Owner: worker.Owner}
	peer.Owner.Pane = "%2"
	r.snapshot.Bindings = []Binding{worker, peer}
	if role == "peer" {
		r.snapshot.Caller = peer
	}
	return r
}

func mailFixture(t *testing.T) Mail {
	t.Helper()
	return Mail{Store: Store{StateRoot: filepath.Join(t.TempDir(), "state")}}
}

func mailPath(m Mail, role, box, id string) string {
	return filepath.Join(m.Store.StateRoot, "repo", "agents", role, box, id+".md")
}

func sendMail(t *testing.T, m Mail) Message {
	t.Helper()
	msg, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "# body\n\nUnicode 日本語\n", "", true)
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

func TestMailPublishesSeparateCopiesAndReturnsDurableClaim(t *testing.T) {
	m := mailFixture(t)
	msg := sendMail(t, m)
	inbox, sent := mailPath(m, "peer", "inbox", msg.ID), mailPath(m, "worker", "sent", msg.ID)
	left, err := os.ReadFile(inbox)
	if err != nil {
		t.Fatal(err)
	}
	right, err := os.ReadFile(sent)
	if err != nil || string(left) != string(right) || !strings.HasSuffix(string(left), msg.Body) {
		t.Fatal("bad publication", err)
	}
	li, err := os.Stat(inbox)
	if err != nil {
		t.Fatal(err)
	}
	ri, err := os.Stat(sent)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(li, ri) || li.Sys().(*syscall.Stat_t).Nlink != 1 {
		t.Fatal("publication used linked copies")
	}
	claim, err := m.Claim(context.Background(), mailRuntime("peer"), "")
	if err != nil || claim == nil || claim.Message != msg || !filepath.IsAbs(claim.Path) {
		t.Fatalf("claim: %+v %v", claim, err)
	}
	if _, err := os.Stat(claim.Path); err != nil {
		t.Fatal("returned missing path", err)
	}
	if _, err := os.Stat(strings.TrimSuffix(claim.Path, ".md") + ".pop.json"); err != nil {
		t.Fatal("receipt absent", err)
	}
	if _, err := os.Stat(inbox); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("inbox survived claim", err)
	}
	again, err := m.Claim(context.Background(), mailRuntime("peer"), "")
	if err != nil || again != nil {
		t.Fatal("duplicate claim", again, err)
	}
	if _, err := os.Stat(sent); err != nil {
		t.Fatal("claim removed sender copy", err)
	}
}

func TestMailSendRecoveryAtEachPublicationBoundary(t *testing.T) {
	failure := errors.New("simulated interrupted send")
	for _, point := range []string{"send-intent", "send-inbox", "send-sent"} {
		t.Run(point, func(t *testing.T) {
			m := mailFixture(t)
			m.afterStep = func(step string) error {
				if step == point {
					return failure
				}
				return nil
			}
			msg, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "same payload", "", false)
			if !errors.Is(err, failure) {
				t.Fatal(err)
			}
			m.afterStep = nil
			if err := m.Recover(context.Background(), mailRuntime("worker")); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{mailPath(m, "peer", "inbox", msg.ID), mailPath(m, "worker", "sent", msg.ID)} {
				if _, err := os.Stat(path); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Recover(context.Background(), mailRuntime("worker")); err != nil {
				t.Fatal(err)
			}
			next, err := m.Send(context.Background(), mailRuntime("worker"), "peer", "same payload", "", false)
			if err != nil || next.ID == msg.ID {
				t.Fatal("equal commands deduplicated", err)
			}
		})
	}
}

func TestMailClaimRecoveryNeverResurrectsOrReturnsOldClaim(t *testing.T) {
	failure := errors.New("simulated interrupted claim")
	for _, point := range []string{"claim-intent", "claim-archived", "claim-receipt"} {
		t.Run(point, func(t *testing.T) {
			m := mailFixture(t)
			msg := sendMail(t, m)
			m.afterStep = func(step string) error {
				if step == point {
					return failure
				}
				return nil
			}
			got, err := m.Claim(context.Background(), mailRuntime("peer"), msg.ID)
			if !errors.Is(err, failure) || got != nil {
				t.Fatal("failed claim returned success", got, err)
			}
			m.afterStep = nil
			if err := m.Recover(context.Background(), mailRuntime("peer")); err != nil {
				t.Fatal(err)
			}
			got, err = m.Claim(context.Background(), mailRuntime("peer"), msg.ID)
			if err != nil || got != nil {
				t.Fatal("recovered old claim returned as new", got, err)
			}
			path := mailPath(m, "peer", "read", msg.ID)
			if _, err := os.Stat(path); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(strings.TrimSuffix(path, ".md") + ".pop.json"); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(mailPath(m, "peer", "inbox", msg.ID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("resurrected", err)
			}
		})
	}
}

func TestMailSendRecoveryDoesNotResurrectClaimedDelivery(t *testing.T) {
	m := mailFixture(t)
	msg := sendMail(t, m)
	claim, err := m.Claim(context.Background(), mailRuntime("peer"), msg.ID)
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	// Simulate the still-present durable send intent after inbox/read recovery.
	err = m.Store.WithVerified(context.Background(), mailRuntime("worker"), func(ws *os.Root, _ Snapshot) error {
		journal, err := privateDir(ws, "transactions", false)
		if err != nil {
			return err
		}
		defer journal.Close()
		return writeJSON(journal, "send-"+msg.ID+".json", mailTransaction{Version: 1, Kind: "send", Message: msg, Body: msg.Body})
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Recover(context.Background(), mailRuntime("worker")); err != nil {
		t.Fatal(err)
	}
	got, err := m.Claim(context.Background(), mailRuntime("peer"), "")
	if err != nil || got != nil {
		t.Fatal("send recovery resurrected claim", got, err)
	}
}

func TestMailClaimsOnlyOwnSelectedMessageAndRejectsUnsafeFiles(t *testing.T) {
	m := mailFixture(t)
	first := sendMail(t, m)
	second := sendMail(t, m)
	got, err := m.Claim(context.Background(), mailRuntime("worker"), first.ID)
	if err != nil || got != nil {
		t.Fatal("drained another agent", err)
	}
	got, err = m.Claim(context.Background(), mailRuntime("peer"), second.ID)
	if err != nil || got == nil || got.Message.ID != second.ID {
		t.Fatal("wrong selective claim", got, err)
	}
	if _, err := os.Stat(mailPath(m, "peer", "inbox", first.ID)); err != nil {
		t.Fatal("unrelated mail drained", err)
	}
	path := mailPath(m, "peer", "inbox", first.ID)
	target := filepath.Join(t.TempDir(), "untouched")
	if err := os.WriteFile(target, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Claim(context.Background(), mailRuntime("peer"), first.ID); err == nil {
		t.Fatal("claimed symlink")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "unchanged" {
		t.Fatal("touched symlink target", err)
	}
}

func TestMailConcurrentClaimHasOneWinner(t *testing.T) {
	m := mailFixture(t)
	msg := sendMail(t, m)
	start := make(chan struct{})
	type outcome struct {
		claim *Claimed
		err   error
	}
	results := make(chan outcome, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claim, err := m.Claim(context.Background(), mailRuntime("peer"), msg.ID)
			results <- outcome{claim, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for result := range results {
		if result.err != nil && !errors.Is(result.err, ErrBusy) {
			t.Fatal(result.err)
		}
		if result.claim != nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatal("claim winners", winners)
	}
}

func TestMailValidatesRecipientAndRecordBeforeIntent(t *testing.T) {
	m := mailFixture(t)
	for _, request := range []struct{ to, body, reply string }{
		{"../escape", "body", ""},
		{"peer", "body", "../escape"},
		{"peer", strings.Repeat("x", 65536), ""},
	} {
		if _, err := m.Send(context.Background(), mailRuntime("worker"), request.to, request.body, request.reply, false); err == nil {
			t.Fatal("accepted unsafe request")
		}
	}
	entries, err := os.ReadDir(filepath.Join(m.Store.StateRoot, "repo", "transactions"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatal("invalid send persisted intent")
	}
}
