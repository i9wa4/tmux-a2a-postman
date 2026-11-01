package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestWorkspaceExecuteHeredocProcessAdvisoryAbsenceAndRepeat(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "unconfigured", true: "verified-absent"}[configured], func(t *testing.T) {
			f := newPublicApprovalFixture(t)
			f.writeConfig(configured)
			if configured {
				f.tmux("kill-pane", "-t", f.peer)
			}
			marker := filepath.Join(f.home, "advisory")
			source := "printf x >> " + marker
			for range 2 {
				ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				cmd := f.execution(ctx, source)
				var diagnostic bytes.Buffer
				cmd.Stderr = &diagnostic
				err := cmd.Run()
				cancel()
				result := decodeExecutionOutput(t, diagnostic.Bytes())
				if err != nil || !result.Success || result.Executed == nil || !*result.Executed || result.Approval != nil {
					t.Fatal(result, err, diagnostic.String())
				}
			}
			raw, err := os.ReadFile(marker)
			if err != nil || string(raw) != "xx" {
				t.Fatal(string(raw), err)
			}
		})
	}
}

func TestWorkspaceExecuteHeredocProcessWaitingFailureNeverDowngrades(t *testing.T) {
	for _, scenario := range []string{"reviewer-disappeared", "socket-probe-failed", "reviewer-retitled", "cancelled", "config-changed"} {
		t.Run(scenario, func(t *testing.T) {
			f := newPublicApprovalFixture(t)
			marker := filepath.Join(f.home, "must-not-run")
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := f.execution(ctx, "printf bad > "+marker)
			var diagnostic bytes.Buffer
			cmd.Stderr = &diagnostic
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			_ = f.waitRequest()
			switch scenario {
			case "reviewer-disappeared":
				f.tmux("kill-pane", "-t", f.peer)
			case "reviewer-retitled":
				f.tmux("select-pane", "-t", f.peer, "-T", "not-a-role")
			case "socket-probe-failed":
				if err := os.Rename(f.socket, f.socket+".hidden"); err != nil {
					t.Fatal(err)
				}
				defer func() { _ = os.Rename(f.socket+".hidden", f.socket) }()
			case "cancelled":
				if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
					t.Fatal(err)
				}
			case "config-changed":
				f.writeConfig(false)
			}
			err := cmd.Wait()
			result := decodeExecutionOutput(t, diagnostic.Bytes())
			if ctx.Err() != nil || err == nil || result.Executed == nil || *result.Executed || result.Success {
				t.Fatal(result, err, ctx.Err(), diagnostic.String())
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatal("waiting failure ran source", err)
			}
		})
	}
}

func TestWorkspaceExecuteHeredocProcessLateDecisionCannotAuthorizeFreshInvocation(t *testing.T) {
	f := newPublicApprovalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	marker := filepath.Join(f.home, "late-must-not-run")
	source := "printf x >> " + marker
	first := f.execution(ctx, source)
	var a bytes.Buffer
	first.Stderr = &a
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	old := f.waitRequest()
	if err := first.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(); err == nil {
		t.Fatal("interrupted request succeeded")
	}
	f.claimRequest(old)
	if _, err := f.decide(old, true); err != nil {
		t.Fatal(err)
	}
	second := f.execution(ctx, source)
	var b bytes.Buffer
	second.Stderr = &b
	if err := second.Start(); err != nil {
		t.Fatal(err)
	}
	fresh := f.waitRequestExcept(map[string]bool{old.ID: true})
	if fresh.ID == old.ID {
		t.Fatal("reused approval identity")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("late decision authorized new invocation")
	}
	f.claimRequest(fresh)
	if _, err := f.decide(fresh, false); err != nil {
		t.Fatal(err)
	}
	if err := second.Wait(); err == nil {
		t.Fatal("declined fresh request succeeded")
	}
	if _, err := os.Stat(filepath.Join(f.box("worker", "inbox"), func() string {
		entries, _ := os.ReadDir(f.box("peer", "sent"))
		for _, entry := range entries {
			msg := readProtocolArchive(t, filepath.Join(f.box("peer", "sent"), entry.Name()))
			if len(msg.Replies) == 1 && msg.Replies[0].MessageID == old.ID {
				return msg.ID + ".md"
			}
		}
		return "missing"
	}())); err != nil {
		t.Fatal("fresh waiter drained late reply", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("late/fresh rejected source ran")
	}
}

func TestWorkspaceExecuteHeredocProcessUnknownInitialIdentityNeverRuns(t *testing.T) {
	f := newPublicApprovalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	marker := filepath.Join(f.home, "unknown-must-not-run")
	f.tmux("select-pane", "-t", f.peer, "-T", "worker")
	cmd := f.execution(ctx, "printf bad > "+marker)
	var diagnostic bytes.Buffer
	cmd.Stderr = &diagnostic
	if err := cmd.Run(); err == nil {
		t.Fatal("ambiguous caller executed")
	}
	result := decodeExecutionOutput(t, diagnostic.Bytes())
	if result.Executed == nil || *result.Executed || result.Success {
		t.Fatal(result)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unknown initial identity ran source", err)
	}
}

// Keep the OS process boundary assertion for the executed child's exit status.
func assertExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		if want != 0 {
			t.Fatalf("want exit %d", want)
		}
		return
	}
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != want {
		t.Fatalf("exit=%v want %d", err, want)
	}
}
