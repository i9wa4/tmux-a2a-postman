package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

func TestCaptureLeaseGenerationRejectsStaleAndRespawnedBinding(t *testing.T) {
	m := mailFixture(t)
	rt := mailRuntime("worker")
	rt.snapshot.Caller.PanePID = "12345"
	rt.snapshot.Bindings[0] = rt.snapshot.Caller
	ctx := context.Background()
	lease := captureLease{
		Role: "worker", Binding: rt.snapshot.Caller,
		PaneIncarnation: "pane-start-1", ProcessIncarnation: "owner-start-1",
	}
	register := func(candidate captureLease, expected uint64) (uint64, error) {
		var generation uint64
		err := m.Store.WithVerified(ctx, rt, func(ws *os.Root, fresh Snapshot) error {
			var err error
			generation, err = registerCaptureLease(ws, fresh, candidate, expected)
			return err
		})
		return generation, err
	}
	if generation, err := register(lease, 0); err != nil || generation != 1 {
		t.Fatalf("initial registration: generation=%d err=%v", generation, err)
	}
	if _, err := register(lease, 0); !errors.Is(err, ErrUnknown) {
		t.Fatalf("stale generation accepted: %v", err)
	}
	// tmux can retain the native pane ID while respawning its process.
	rt.snapshot.Caller.PanePID = "23456"
	rt.snapshot.Bindings[0] = rt.snapshot.Caller
	if _, err := register(lease, 1); !errors.Is(err, ErrUnknown) {
		t.Fatalf("old process binding accepted after same-pane respawn: %v", err)
	}
	fresh := lease
	fresh.Binding = rt.snapshot.Caller
	fresh.PaneIncarnation = "pane-start-2"
	fresh.ProcessIncarnation = "owner-start-2"
	if generation, err := register(fresh, 1); err != nil || generation != 2 {
		t.Fatalf("fresh registration: generation=%d err=%v", generation, err)
	}
	if _, err := register(lease, 2); !errors.Is(err, ErrUnknown) {
		t.Fatalf("old lease reused after new registration: %v", err)
	}
}

func TestCaptureLeaseRejectsAmbiguityAndCorruptLedger(t *testing.T) {
	m := mailFixture(t)
	rt := mailRuntime("worker")
	rt.snapshot.Caller.PanePID = "12345"
	rt.snapshot.Bindings[0] = rt.snapshot.Caller
	ctx := context.Background()
	lease := captureLease{
		Role: "worker", Binding: rt.snapshot.Caller,
		PaneIncarnation: "pane-start-1", ProcessIncarnation: "owner-start-1",
	}
	if err := m.Store.WithVerified(ctx, rt, func(ws *os.Root, fresh Snapshot) error {
		_, err := registerCaptureLease(ws, fresh, lease, 0)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	duplicate := rt.snapshot.Bindings[0]
	duplicate.Role = "peer"
	rt.snapshot.Bindings = append(rt.snapshot.Bindings, duplicate)
	if err := m.Store.WithVerified(ctx, rt, func(*os.Root, Snapshot) error {
		t.Fatal("ambiguous native pane reached lease mutation")
		return nil
	}); !errors.Is(err, ErrUnknown) {
		t.Fatalf("ambiguous role/pane accepted: %v", err)
	}
	rt.snapshot.Bindings = rt.snapshot.Bindings[:2]

	if err := m.Store.WithVerified(ctx, rt, func(ws *os.Root, fresh Snapshot) error {
		runtimeDir, err := privateDir(ws, "runtime", false)
		if err != nil {
			return err
		}
		defer runtimeDir.Close()
		if err := replaceJSON(runtimeDir, "capture-leases.json", captureLeaseRecord{
			Version: 1, Generation: 1,
			Leases: []captureLease{{
				Role: "worker", Binding: fresh.Caller, Generation: 2,
				PaneIncarnation: "pane-start-1", ProcessIncarnation: "owner-start-1",
			}},
		}); err != nil {
			return err
		}
		_, err = registerCaptureLease(ws, fresh, lease, 1)
		if !errors.Is(err, ErrUnknown) {
			t.Fatalf("corrupt ledger accepted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureLeaseRejectsMissingIncarnation(t *testing.T) {
	m := mailFixture(t)
	rt := mailRuntime("worker")
	rt.snapshot.Caller.PanePID = "12345"
	rt.snapshot.Bindings[0] = rt.snapshot.Caller
	err := m.Store.WithVerified(context.Background(), rt, func(ws *os.Root, fresh Snapshot) error {
		_, err := registerCaptureLease(ws, fresh, captureLease{
			Role: "worker", Binding: fresh.Caller, PaneIncarnation: "pane-start-1",
		}, 0)
		if !errors.Is(err, ErrUnknown) {
			t.Fatalf("missing owner incarnation accepted: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCaptureLeaseRejectsMalformedAndStalePeerRecords(t *testing.T) {
	for _, variant := range []string{"missing_version", "empty_record", "mixed_runtime", "wrong_session", "stale_peer"} {
		t.Run(variant, func(t *testing.T) {
			m := mailFixture(t)
			rt := mailRuntime("worker")
			rt.snapshot.Caller.PanePID = "12345"
			rt.snapshot.Bindings[0] = rt.snapshot.Caller
			err := m.Store.WithVerified(context.Background(), rt, func(ws *os.Root, fresh Snapshot) error {
				runtimeDir, err := privateDir(ws, "runtime", false)
				if err != nil {
					return err
				}
				defer runtimeDir.Close()
				candidate := captureLease{
					Role: "worker", Binding: fresh.Caller, Generation: 1,
					PaneIncarnation: "pane-start-1", ProcessIncarnation: "owner-start-1",
				}
				record := captureLeaseRecord{Version: 1, Generation: 1, Leases: []captureLease{candidate}}
				if variant != "missing_version" && variant != "empty_record" {
					peer := candidate
					peer.Role = "peer"
					peer.Binding.Role = "peer"
					peer.Binding.Owner.Pane = "%999"
					peer.Binding.PanePID = "23456"
					if variant == "mixed_runtime" {
						peer.Binding.Owner.Server = "other-server"
					}
					if variant == "wrong_session" {
						peer.Binding.Owner.Session = "$999"
					}
					record.Leases = append(record.Leases, peer)
				}
				if variant == "empty_record" {
					record = captureLeaseRecord{Version: 1, Generation: 0, Leases: []captureLease{}}
				}
				var stored any = record
				if variant == "missing_version" {
					stored = map[string]any{"generation": record.Generation, "leases": record.Leases}
				}
				if err := replaceJSON(runtimeDir, "capture-leases.json", stored); err != nil {
					return err
				}
				before, err := readCaptureLeaseBytes(runtimeDir)
				if err != nil {
					return err
				}
				expected := uint64(1)
				if variant == "empty_record" {
					expected = 0
				}
				_, err = registerCaptureLease(ws, fresh, candidate, expected)
				if !errors.Is(err, ErrUnknown) {
					t.Fatalf("%s record accepted: %v", variant, err)
				}
				after, err := readCaptureLeaseBytes(runtimeDir)
				if err != nil {
					return err
				}
				if string(before) != string(after) {
					t.Fatalf("%s rejection overwrote ledger", variant)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func readCaptureLeaseBytes(runtimeDir *os.Root) ([]byte, error) {
	file, err := runtimeDir.Open("capture-leases.json")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func TestCaptureLeaseFirstCreateSyncFaultReopenAndRetry(t *testing.T) {
	for _, variant := range []string{"sync_failure", "post_sync_crash"} {
		t.Run(variant, func(t *testing.T) {
			m := mailFixture(t)
			rt := mailRuntime("worker")
			rt.snapshot.Caller.PanePID = "12345"
			rt.snapshot.Bindings[0] = rt.snapshot.Caller
			lease := captureLease{
				Role: "worker", Binding: rt.snapshot.Caller,
				PaneIncarnation: "pane-start-1", ProcessIncarnation: "owner-start-1",
			}
			injected := errors.New("injected first-create sync fault")
			syncCalls := 0
			err := m.Store.WithVerified(context.Background(), rt, func(ws *os.Root, fresh Snapshot) error {
				_, err := registerCaptureLeaseWithSync(ws, fresh, lease, 0, func(root *os.Root) error {
					syncCalls++
					if variant == "post_sync_crash" {
						if err := syncDirectory(root); err != nil {
							return err
						}
					}
					return injected
				})
				return err
			})
			if !errors.Is(err, injected) || syncCalls != 1 {
				t.Fatalf("first create fault: calls=%d err=%v", syncCalls, err)
			}
			reopened := Mail{Store: m.Store}
			err = reopened.Store.WithVerified(context.Background(), rt, func(ws *os.Root, fresh Snapshot) error {
				runtimeDir, err := privateDir(ws, "runtime", false)
				if err != nil {
					return err
				}
				defer runtimeDir.Close()
				var record captureLeaseRecord
				if err := readJSON(runtimeDir, "capture-leases.json", &record); err != nil {
					return err
				}
				if record.Version != 1 || record.Generation != 1 || len(record.Leases) != 1 {
					t.Fatalf("reopened first create lost generation: %+v", record)
				}
				if _, err := registerCaptureLease(ws, fresh, lease, 0); !errors.Is(err, ErrUnknown) {
					t.Fatalf("retry reset generation after reported error: %v", err)
				}
				generation, err := registerCaptureLease(ws, fresh, lease, 1)
				if err != nil || generation != 2 {
					t.Fatalf("retry with durable generation: generation=%d err=%v", generation, err)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCreateCaptureLeaseJSONOccupiedTempPreserved(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	const temp = ".new-occupied"
	const sentinel = "existing temporary entry"
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, sentinel); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	record := captureLeaseRecord{Version: 1, Generation: 1, Leases: []captureLease{{Role: "worker"}}}
	err = createCaptureLeaseJSONWithTemp(root, record, func(*os.Root) error { return nil }, nil, temp)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("occupied temporary name accepted: %v", err)
	}
	file, err = root.Open(temp)
	if err != nil {
		t.Fatalf("occupied temporary entry was removed: %v", err)
	}
	data, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read occupied temporary entry: read=%v close=%v", readErr, closeErr)
	}
	if string(data) != sentinel {
		t.Fatalf("occupied temporary entry changed: %q", data)
	}
}
