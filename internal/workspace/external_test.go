package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type externalTestRuntime struct{ *storedRuntime }

func (externalTestRuntime) Name() string { return "team" }
func externalFixture(t *testing.T) (Mail, externalTestRuntime, ExternalBinding) {
	t.Helper()
	m := mailFixture(t)
	rt := externalTestRuntime{storageRuntime()}
	b := ExternalBinding{filepath.Join(t.TempDir(), "external"), "~/repo", "team", "worker"}
	if _, err := m.SyncExternal(context.Background(), rt, b); err != nil {
		t.Fatal(err)
	}
	return m, rt, b
}

func publishExternal(t *testing.T, b ExternalBinding, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(b.Root, "inbox", name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func claimExternal(t *testing.T, m Mail, rt ExternalRuntime) *Claimed {
	t.Helper()
	c, err := m.Claim(context.Background(), rt, "")
	if err != nil || c == nil {
		t.Fatalf("claim: %v %#v", err, c)
	}
	return c
}

func TestExternalDurableRoundTripRestartAndPublicationGap(t *testing.T) {
	for _, state := range []string{"done", "blocked"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			m, rt, b := externalFixture(t)
			publishExternal(t, b, "request-1.md", "literal request\nEOF\n")
			first, err := m.SyncExternal(ctx, rt, b)
			if err != nil || len(first.Accepted) != 1 {
				t.Fatalf("intake: %#v %v", first, err)
			}
			claim := claimExternal(t, m, rt)
			if claim.Message.External == nil || claim.Message.Body != "literal request\nEOF\n" {
				t.Fatal("lost original text/provenance")
			}
			if _, err := os.Stat(filepath.Join(b.Root, "read", "request-1.md")); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(b.Root, "inbox", "request-1.md")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("not archived")
			}
			m.afterStep = func(step string) error {
				if step == "external-report-committed" {
					return errors.New("crash before outbox")
				}
				return nil
			}
			report, err := m.ReportExternal(ctx, rt, b, claim.Message.ID, state, "", "Exact terminal evidence")
			if err == nil || report.ID == "" {
				t.Fatal("fault not exercised")
			}
			m2 := Mail{Store: m.Store}
			recovered, err := m2.SyncExternal(ctx, rt, b)
			if err != nil || len(recovered.Published) != 1 {
				t.Fatalf("recovery: %#v %v", recovered, err)
			}
			raw, err := os.ReadFile(filepath.Join(b.Root, "outbox", report.ID+".md"))
			if err != nil || !strings.Contains(string(raw), "Exact terminal evidence") {
				t.Fatal(err, string(raw))
			}
			same, err := m2.ReportExternal(ctx, rt, b, claim.Message.ID, state, "", "Exact terminal evidence")
			if err != nil || same.ID != report.ID {
				t.Fatal("rerun not idempotent", err)
			}
			if _, err := m2.ReportExternal(ctx, rt, b, claim.Message.ID, state, "", "changed result"); err == nil {
				t.Fatal("terminal overwrite accepted")
			}
			publishExternal(t, b, "request-1.md", "literal request\nEOF\n")
			if _, err := m2.SyncExternal(ctx, rt, b); err != nil {
				t.Fatal(err)
			}
			if next, err := m2.Claim(ctx, rt, ""); err != nil || next != nil {
				t.Fatal("duplicate action", err)
			}
			publishExternal(t, b, "Request-1.md", "literal request\nEOF\n")
			if _, err := m2.SyncExternal(ctx, rt, b); err == nil {
				t.Fatal("case-alias filename was accepted as a second identity")
			}
			if err := os.Remove(filepath.Join(b.Root, "inbox", "Request-1.md")); err != nil {
				t.Fatal(err)
			}
			publishExternal(t, b, "request-1.md", "changed request")
			if _, err := m2.SyncExternal(ctx, rt, b); err == nil {
				t.Fatal("changed-payload retry accepted")
			}
		})
	}
}

func TestExternalCrashBoundariesNeverDuplicateClaim(t *testing.T) {
	for _, step := range []string{"send-draft", "send-intent", "send-inbox", "send-sent", "external-accepted", "external-archived"} {
		t.Run(step, func(t *testing.T) {
			ctx := context.Background()
			m, rt, b := externalFixture(t)
			publishExternal(t, b, "request.md", "request")
			fired := false
			m.afterStep = func(at string) error {
				if at == step && !fired {
					fired = true
					return errors.New("owned crash")
				}
				return nil
			}
			if _, err := m.SyncExternal(ctx, rt, b); err == nil || !fired {
				t.Fatal("fault not exercised")
			}
			m.afterStep = nil
			if _, err := m.SyncExternal(ctx, rt, b); err != nil {
				t.Fatal(err)
			}
			_ = claimExternal(t, m, rt)
			if next, err := m.Claim(ctx, rt, ""); err != nil || next != nil {
				t.Fatal("duplicate claim", err)
			}
			if _, err := m.SyncExternal(ctx, rt, b); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExternalExactDecisionsAndSupersededScopes(t *testing.T) {
	ctx := context.Background()
	m, rt, b := externalFixture(t)
	publishExternal(t, b, "request.md", "request")
	if _, err := m.SyncExternal(ctx, rt, b); err != nil {
		t.Fatal(err)
	}
	request := claimExternal(t, m, rt)
	pending, err := m.ReportExternal(ctx, rt, b, request.Message.ID, "pending", externalHash("action revision one"), "May I perform action revision one?")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.ReportExternal(ctx, rt, b, request.Message.ID, "pending", externalHash("action revision two"), "May I perform action revision two?")
	if err != nil {
		t.Fatal(err)
	}
	oldName := decisionFilename(pending.ID, pending.External.Scope, "approve")
	publishExternal(t, b, oldName, "yes")
	if _, err := m.SyncExternal(ctx, rt, b); err == nil {
		t.Fatal("superseded decision accepted")
	}
	if err := os.Remove(filepath.Join(b.Root, "inbox", oldName)); err != nil {
		t.Fatal(err)
	}
	catchup, err := m.SyncExternal(ctx, rt, b)
	if err != nil || len(catchup.Pending) != 1 || catchup.Pending[0].DecisionID != second.ID {
		t.Fatalf("bad catch-up: %#v %v", catchup, err)
	}
	name := catchup.Pending[0].ApproveFilename
	publishExternal(t, b, name, "approve only revision two")
	if _, err := m.SyncExternal(ctx, rt, b); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReportExternal(ctx, rt, b, request.Message.ID, "done", "", "premature result before reading answer"); err == nil {
		t.Fatal("terminal result bypassed unread exchange mail")
	}
	answer := claimExternal(t, m, rt)
	if answer.Message.External.DecisionID != second.ID || answer.Message.External.Choice != "approve" {
		t.Fatal("wrong decision correlation")
	}
	answered, err := m.SyncExternal(ctx, rt, b)
	if err != nil || len(answered.Pending) != 1 || answered.Pending[0].AnswerMessageID != answer.Message.ID || answered.Pending[0].ApproveFilename != "" {
		t.Fatalf("answered challenge offered another choice: %#v %v", answered, err)
	}
	opposite := catchup.Pending[0].DeclineFilename
	publishExternal(t, b, opposite, "no")
	if _, err := m.SyncExternal(ctx, rt, b); err == nil {
		t.Fatal("conflicting choice accepted")
	}
	if err := os.Remove(filepath.Join(b.Root, "inbox", opposite)); err != nil {
		t.Fatal(err)
	}
	result, err := m.ReportExternal(ctx, rt, b, answer.Message.ID, "done", "", "exact approved action completed")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := m.ReportExternal(ctx, rt, b, answer.Message.ID, "done", "", "exact approved action completed"); err != nil || again.ID != result.ID {
		t.Fatal("answered result retry changed identity", err)
	}
	final, err := m.SyncExternal(ctx, rt, b)
	if err != nil || len(final.Pending) != 0 {
		t.Fatal("terminal decision still pending", err)
	}
}

func TestExternalReportCrashBeforeMailCommit(t *testing.T) {
	for _, step := range []string{"send-draft", "send-intent", "send-inbox", "send-sent"} {
		t.Run(step, func(t *testing.T) {
			ctx := context.Background()
			m, rt, b := externalFixture(t)
			publishExternal(t, b, "request.md", "request")
			if _, err := m.SyncExternal(ctx, rt, b); err != nil {
				t.Fatal(err)
			}
			claim := claimExternal(t, m, rt)
			fired := false
			m.afterStep = func(at string) error {
				if at == step && !fired {
					fired = true
					return errors.New("report crash")
				}
				return nil
			}
			first, err := m.ReportExternal(ctx, rt, b, claim.Message.ID, "done", "", "terminal exact result")
			if err == nil || !fired {
				t.Fatal("report fault not exercised")
			}
			m.afterStep = nil
			again, err := m.ReportExternal(ctx, rt, b, claim.Message.ID, "done", "", "terminal exact result")
			if err != nil || again.ID != first.ID {
				t.Fatal("report retry failed", err)
			}
			entries, err := os.ReadDir(filepath.Join(b.Root, "outbox"))
			if err != nil || len(entries) != 1 {
				t.Fatal("duplicate/missing output", err)
			}
		})
	}
}

func TestExternalRevisedDecisionClosesAllClaimedExchangeMail(t *testing.T) {
	ctx := context.Background()
	m, rt, b := externalFixture(t)
	publishExternal(t, b, "request.md", "request")
	if _, err := m.SyncExternal(ctx, rt, b); err != nil {
		t.Fatal(err)
	}
	request := claimExternal(t, m, rt)
	var previousAnswer *Claimed
	for _, revision := range []string{"first action", "revised action"} {
		pending, err := m.ReportExternal(ctx, rt, b, request.Message.ID, "pending", externalHash(revision), revision)
		if err != nil {
			t.Fatal(err)
		}
		if previousAnswer != nil {
			if _, err := m.ReportExternal(ctx, rt, b, previousAnswer.Message.ID, "done", "", "stale decision result"); err == nil {
				t.Fatal("stale answer authorized revised action")
			}
		}
		publishExternal(t, b, decisionFilename(pending.ID, pending.External.Scope, "approve"), "approve only "+revision)
		if _, err := m.SyncExternal(ctx, rt, b); err != nil {
			t.Fatal(err)
		}
		previousAnswer = claimExternal(t, m, rt)
	}
	if _, err := m.ReportExternal(ctx, rt, b, previousAnswer.Message.ID, "done", "", "revised action done; earlier answer superseded"); err != nil {
		t.Fatal(err)
	}
	_, _, summary := m.Summary(ctx, rt, b.Node)
	if summary.InboundCount == nil || *summary.InboundCount != 0 {
		t.Fatalf("terminal exchange left claimed mail unanswered: %#v", summary)
	}
}

func TestExternalRestartBindingIsolationAndMissingReceipt(t *testing.T) {
	ctx := context.Background()
	m, rt, b := externalFixture(t)
	publishExternal(t, b, "same.md", "first binding")
	if _, err := m.SyncExternal(ctx, rt, b); err != nil {
		t.Fatal(err)
	}
	first := claimExternal(t, m, rt)
	other := b
	other.Root = filepath.Join(filepath.Dir(b.Root), "separate")
	if _, err := m.SyncExternal(ctx, rt, other); err != nil {
		t.Fatal(err)
	}
	publishExternal(t, other, "same.md", "other binding")
	if _, err := m.SyncExternal(ctx, rt, other); err != nil {
		t.Fatal(err)
	}
	second := claimExternal(t, m, rt)
	if first.Message.ID == second.Message.ID {
		t.Fatal("binding identity collision")
	}
	if _, err := m.ReportExternal(ctx, rt, other, first.Message.ID, "done", "", "foreign report"); err == nil {
		t.Fatal("foreign exchange report accepted")
	}
	rt.snapshot.Caller.Owner.Incarnation = "replacement-native-incarnation"
	rt.snapshot.Bindings = []Binding{rt.snapshot.Caller}
	rt.state = Dead
	if _, err := m.SyncExternal(ctx, rt, b); err != nil {
		t.Fatal("same persistent binding did not survive native restart", err)
	}
	if next, err := m.Claim(ctx, rt, ""); err != nil || next != nil {
		t.Fatal("restart replayed consumed mail", err)
	}
	if err := os.Remove(strings.TrimSuffix(first.Path, ".md") + ".pop.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SyncExternal(ctx, rt, b); err == nil {
		t.Fatal("missing claim receipt hidden")
	}
}

func TestExternalLocalLockAndInternalStateContainment(t *testing.T) {
	m, rt, b := externalFixture(t)
	root, lock, err := openExternalRoot(b)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.SyncExternal(context.Background(), rt, b)
	_ = lock.Close()
	_ = root.Close()
	if !errors.Is(err, ErrBusy) {
		t.Fatal("concurrent external mutation did not stop at admission", err)
	}
	b.Root = filepath.Join(m.Store.StateRoot, "external")
	if _, err := m.SyncExternal(context.Background(), rt, b); err == nil {
		t.Fatal("overlapping state root accepted")
	}
}

func TestExternalRejectsUnsafeFilesAndForeignBindings(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "directory", "public", "large", "invalid-utf8", "bad-name", "fake-decision"} {
		t.Run(kind, func(t *testing.T) {
			m, rt, b := externalFixture(t)
			p := filepath.Join(b.Root, "inbox", "request.md")
			switch kind {
			case "symlink":
				if err := os.Symlink(filepath.Join(b.Root, "binding.json"), p); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(filepath.Join(b.Root, "binding.json"), p); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(p, 0o700); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.WriteFile(p, []byte("request"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(p, 0o644); err != nil {
					t.Fatal(err)
				}
			case "large":
				publishExternal(t, b, "request.md", strings.Repeat("a", 32*1024+1))
			case "invalid-utf8":
				publishExternal(t, b, "request.md", string([]byte{255}))
			case "bad-name":
				publishExternal(t, b, "bad name.md", "request")
			case "fake-decision":
				publishExternal(t, b, "answer--fake.md", "proceed")
			}
			if _, err := m.SyncExternal(context.Background(), rt, b); err == nil {
				t.Fatal("unsafe input accepted")
			}
			if c, err := m.Claim(context.Background(), rt, ""); err != nil || c != nil {
				t.Fatal("unsafe input delivered", err)
			}
		})
	}
	m, rt, b := externalFixture(t)
	other := b
	other.Node = "peer"
	if _, err := m.SyncExternal(context.Background(), rt, other); err == nil {
		t.Fatal("foreign node accepted")
	}
	other = b
	other.Workspace = "~/other"
	if _, err := m.SyncExternal(context.Background(), rt, other); err == nil {
		t.Fatal("foreign workspace accepted")
	}
	other = b
	other.Session = "other"
	if _, err := m.SyncExternal(context.Background(), rt, other); err == nil {
		t.Fatal("foreign session accepted")
	}
	other = b
	other.Root = filepath.Join(filepath.Dir(b.Root), "alias")
	if err := os.Symlink(b.Root, other.Root); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SyncExternal(context.Background(), rt, other); err == nil {
		t.Fatal("aliased root accepted")
	}
}
