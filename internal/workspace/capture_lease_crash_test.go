package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// This test covers process-crash recovery, not power-loss durability.
func TestCaptureLeaseFirstCreateProcessCrash(t *testing.T) {
	if mode := os.Getenv("TMUX_A2A_CAPTURE_CRASH_MODE"); mode != "" && os.Getenv("TMUX_A2A_CAPTURE_CRASH_MARKER") != "" {
		captureLeaseCrashChild(t, mode)
		return
	}
	for _, mode := range []string{"mid_temp_write", "before_dir_sync", "after_dir_sync", "failed_dir_sync"} {
		t.Run(mode, func(t *testing.T) {
			m := mailFixture(t)
			rt := mailRuntime("worker")
			rt.snapshot.Caller.PanePID = "12345"
			rt.snapshot.Bindings[0] = rt.snapshot.Caller
			lease := captureLease{
				Role: "worker", Binding: rt.snapshot.Caller,
				PaneIncarnation: "pane-start-1", ProcessIncarnation: "owner-start-1",
			}
			if err := m.Store.WithVerified(context.Background(), rt,
				func(*os.Root, Snapshot) error { return nil }); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(t.TempDir(), "child-marker")
			if err := os.WriteFile(marker, []byte(m.Store.StateRoot), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestCaptureLeaseFirstCreateProcessCrash$")
			cmd.Env = append(os.Environ(), "TMUX_A2A_CAPTURE_CRASH_ROOT="+m.Store.StateRoot,
				"TMUX_A2A_CAPTURE_CRASH_MODE="+mode,
				"TMUX_A2A_CAPTURE_CRASH_MARKER="+marker)
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), "capture-crash-hook:"+mode) {
				t.Fatalf("child missed first-create hook: err=%v output=%s", err, output)
			}
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("child did not exit: %v", err)
			}
			if mode == "failed_dir_sync" {
				if exit.ExitCode() != 94 {
					t.Fatalf("failed sync not reported: %v output=%s", err, output)
				}
			} else {
				status, ok := exit.Sys().(syscall.WaitStatus)
				if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
					t.Fatalf("child was not killed: %v output=%s", err, output)
				}
			}
			restarted := Store{StateRoot: m.Store.StateRoot}
			fresh := mailRuntime("worker")
			fresh.snapshot.Caller.PanePID = "12345"
			fresh.snapshot.Bindings[0] = fresh.snapshot.Caller
			err = restarted.WithVerified(context.Background(), fresh, func(ws *os.Root, snapshot Snapshot) error {
				dir, err := privateDir(ws, "runtime", false)
				if err != nil {
					return err
				}
				defer func() { _ = dir.Close() }()
				var record captureLeaseRecord
				err = readJSON(dir, "capture-leases.json", &record)
				if mode == "mid_temp_write" && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("mid-temp SIGKILL published final ledger: err=%v record=%+v", err, record)
				}
				if errors.Is(err, os.ErrNotExist) {
					if mode == "after_dir_sync" {
						t.Fatal("synced first-create ledger missing after process death")
					}
					if mode == "mid_temp_write" {
						opened, err := dir.Open(".")
						if err != nil {
							return err
						}
						entries, readErr := opened.ReadDir(-1)
						closeErr := opened.Close()
						if readErr != nil {
							return readErr
						}
						if closeErr != nil {
							return closeErr
						}
						orphan := false
						for _, entry := range entries {
							if !strings.HasPrefix(entry.Name(), ".new-") {
								continue
							}
							f, openErr := dir.Open(entry.Name())
							if openErr != nil {
								return openErr
							}
							data, readErr := io.ReadAll(f)
							closeErr := f.Close()
							if readErr != nil {
								return readErr
							}
							if closeErr != nil {
								return closeErr
							}
							orphan = orphan || len(data) > 0 && !json.Valid(data)
						}
						if !orphan {
							t.Fatal("partial temporary ledger was not left by SIGKILL")
						}
					}
					generation, err := registerCaptureLease(ws, snapshot, lease, 0)
					if err != nil || generation != 1 {
						t.Fatalf("absent-ledger retry: generation=%d err=%v", generation, err)
					}
					var retried captureLeaseRecord
					if err := readJSON(dir, "capture-leases.json", &retried); err != nil ||
						retried.Generation != 1 || validateCaptureLeaseRecord(retried) != nil {
						t.Fatalf("retry did not publish complete ledger: %+v err=%v", retried, err)
					}
					return nil
				}
				if err != nil {
					return err
				}
				if record.Version != 1 || record.Generation != 1 || len(record.Leases) != 1 ||
					record.Leases[0].Role != lease.Role || record.Leases[0].Binding != lease.Binding ||
					record.Leases[0].PaneIncarnation != lease.PaneIncarnation ||
					record.Leases[0].ProcessIncarnation != lease.ProcessIncarnation ||
					validateCaptureLeaseRecord(record) != nil {
					t.Fatalf("invalid surviving ledger: %+v", record)
				}
				if _, err := registerCaptureLease(ws, snapshot, lease, 0); !errors.Is(err, ErrUnknown) {
					t.Fatalf("stale generation accepted after restart: %v", err)
				}
				generation, err := registerCaptureLease(ws, snapshot, lease, 1)
				if err != nil || generation != 2 {
					t.Fatalf("observed-generation retry: generation=%d err=%v", generation, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func captureLeaseCrashChild(t *testing.T, mode string) {
	root := os.Getenv("TMUX_A2A_CAPTURE_CRASH_ROOT")
	marker := os.Getenv("TMUX_A2A_CAPTURE_CRASH_MARKER")
	if root == "" || marker == "" {
		t.Fatal("missing isolated state root or parent marker")
	}
	boundRoot, err := os.ReadFile(marker)
	if err != nil || string(boundRoot) != root {
		t.Fatalf("invalid parent marker: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatalf("parent marker was not consumed: %v", err)
	}
	rt := mailRuntime("worker")
	rt.snapshot.Caller.PanePID = "12345"
	rt.snapshot.Bindings[0] = rt.snapshot.Caller
	lease := captureLease{
		Role: "worker", Binding: rt.snapshot.Caller,
		PaneIncarnation: "pane-start-1", ProcessIncarnation: "owner-start-1",
	}
	injected := errors.New("injected directory sync failure")
	err = (Store{StateRoot: root}).WithVerified(context.Background(), rt, func(ws *os.Root, snapshot Snapshot) error {
		if mode == "mid_temp_write" {
			_, err := registerCaptureLeaseWithHooks(ws, snapshot, lease, 0, syncDirectory, func() error {
				_, _ = fmt.Fprintln(os.Stdout, "capture-crash-hook:"+mode)
				if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
					return err
				}
				select {}
			})
			return err
		}
		_, err := registerCaptureLeaseWithSync(ws, snapshot, lease, 0, func(dir *os.Root) error {
			if mode == "after_dir_sync" {
				if err := syncDirectory(dir); err != nil {
					return err
				}
			} else if mode != "before_dir_sync" && mode != "failed_dir_sync" {
				return fmt.Errorf("unknown child mode %q", mode)
			}
			_, _ = fmt.Fprintln(os.Stdout, "capture-crash-hook:"+mode)
			if mode == "failed_dir_sync" {
				return injected
			}
			if err := syscall.Kill(os.Getpid(), syscall.SIGKILL); err != nil {
				return err
			}
			select {}
		})
		return err
	})
	if mode == "failed_dir_sync" && errors.Is(err, injected) {
		os.Exit(94)
	}
	t.Fatalf("child did not die or report sync failure: mode=%s err=%v", mode, err)
}
