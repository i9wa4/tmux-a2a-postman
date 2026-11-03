package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestAvailableMailIdentityChecksAllHistoricalBoxesAndOperations(t *testing.T) {
	const occupied = "m-20261103T173802.482076000Z-ABC123"
	const free = "m-20261103T173802.482076000Z-DEF456"
	for _, box := range []string{"draft", "inbox", "read", "sent", "failed", "executions", "decisions", "transactions"} {
		t.Run(box, func(t *testing.T) {
			m := mailFixture(t)
			request := sendMail(t, m) // Initialize the verified workspace and both roles.
			root := filepath.Join(m.Store.StateRoot, "repo")
			dir := filepath.Join(root, "agents", "peer", box)
			name := occupied + ".md"
			if box == "executions" {
				name = occupied + ".json"
			}
			if box == "transactions" {
				dir = filepath.Join(root, box)
				name = "send-" + occupied + ".json"
			}
			content := []byte("occupied")
			if box == "decisions" {
				dir = filepath.Join(root, "agents", "peer", "executions", "decisions", "by-message")
				name = occupied + ".json"
				var err error
				content, err = json.Marshal(struct {
					RequestID string `json:"request_id"`
				}{request.ID})
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), content, 0o600); err != nil {
				t.Fatal(err)
			}
			attempts := 0
			generator := func() (mailIdentity, error) {
				attempts++
				if attempts == 1 {
					return mailIdentity{ID: occupied}, nil
				}
				return mailIdentity{ID: free}, nil
			}
			err := m.Store.WithVerified(context.Background(), mailRuntime("worker"), func(ws *os.Root, _ Snapshot) error {
				got, err := availableMailIdentity(generator, ws)
				if err != nil {
					return err
				}
				if got.ID != free || attempts != 2 {
					t.Fatalf("collision was not retried: %q, attempts=%d", got.ID, attempts)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDecisionIdentityLookupDoesNotScanRetainedDecisions(t *testing.T) {
	m := mailFixture(t)
	request := sendMail(t, m)
	root := filepath.Join(m.Store.StateRoot, "repo", "agents", "peer", "executions", "decisions")
	if err := os.MkdirAll(filepath.Join(root, "by-message"), 0o700); err != nil {
		t.Fatal(err)
	}
	for i := range 2050 {
		name := filepath.Join(root, request.ID+"-"+strconv.Itoa(i)+".json")
		if err := os.WriteFile(name, []byte("malformed unrelated decision"), 0o000); err != nil {
			t.Fatal(err)
		}
	}
	const occupied = "m-20261103T173802.482076000Z-ABC123"
	const free = "m-20261103T173802.482076000Z-DEF456"
	if err := os.WriteFile(filepath.Join(root, "by-message", occupied+".json"), []byte("reserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	err := m.Store.WithVerified(context.Background(), mailRuntime("worker"), func(ws *os.Root, _ Snapshot) error {
		attempts := 0
		got, err := availableMailIdentity(func() (mailIdentity, error) {
			attempts++
			if attempts == 1 {
				return mailIdentity{ID: occupied}, nil
			}
			return mailIdentity{ID: free}, nil
		}, ws)
		if err != nil || got.ID != free || attempts != 2 {
			t.Fatalf("retained decisions blocked allocation or hid a collision: %q, attempts=%d, err=%v", got.ID, attempts, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("identity allocation held the workspace lock too long: %s", elapsed)
	}
}

func TestReservedOperationIsDurableAndImmutableBeforePublication(t *testing.T) {
	m := mailFixture(t)
	e := Executor{Mail: m}
	const id = "m-20261103T173802.482076000Z-ABC123"
	result := ExecutionResult{Version: 1, Command: "printf once", Requester: "worker", Status: "unavailable"}
	got, err := e.reserveOperation(context.Background(), mailRuntime("worker"), result, func() (mailIdentity, error) {
		return mailIdentity{ID: id}, nil
	})
	if err != nil || got != id {
		t.Fatal(got, err)
	}
	path := filepath.Join(m.Store.StateRoot, "repo", "agents", "worker", "executions", id+".json")
	var reserved ExecutionResult
	if err := readJSONFile(path, &reserved); err != nil || reserved.Status != "reserved" || reserved.OperationID != id || reserved.Command != result.Command {
		t.Fatal(reserved, err)
	}
	result.OperationID = id
	result.Status = "dispatched"
	result.Command = "changed command"
	if err := e.save(context.Background(), mailRuntime("worker"), result, true); err == nil {
		t.Fatal("mutated command transitioned reserved operation")
	}
	if err := readJSONFile(path, &reserved); err != nil || reserved.Status != "reserved" || reserved.Command != "printf once" {
		t.Fatal(reserved, err)
	}
	result.Command = "printf once"
	if err := e.save(context.Background(), mailRuntime("worker"), result, true); err != nil {
		t.Fatal(err)
	}
	if err := readJSONFile(path, &reserved); err != nil || reserved.Status != "dispatched" {
		t.Fatal(reserved, err)
	}
}

func TestReservedOperationSurvivesRestartWithoutReusingIdentity(t *testing.T) {
	m := mailFixture(t)
	const occupied = "m-20261103T173802.482076000Z-ABC123"
	const next = "m-20261103T173802.482076000Z-DEF456"
	result := ExecutionResult{Version: 1, Command: "printf once", Requester: "worker", Status: "unavailable"}
	first := Executor{Mail: m}
	if id, err := first.reserveOperation(context.Background(), mailRuntime("worker"), result, func() (mailIdentity, error) {
		return mailIdentity{ID: occupied}, nil
	}); err != nil || id != occupied {
		t.Fatal(id, err)
	}
	// A new executor and runtime stand in for a process restart after the
	// parent directory sync but before any approval request publication.
	restarted := Executor{Mail: Mail{Store: Store{StateRoot: m.Store.StateRoot}}}
	attempts := 0
	id, err := restarted.reserveOperation(context.Background(), mailRuntime("worker"), result, func() (mailIdentity, error) {
		attempts++
		if attempts == 1 {
			return mailIdentity{ID: occupied}, nil
		}
		return mailIdentity{ID: next}, nil
	})
	if err != nil || id != next || attempts != 2 {
		t.Fatal(id, err, attempts)
	}
	path := filepath.Join(m.Store.StateRoot, "repo", "agents", "worker", "executions", occupied+".json")
	var saved ExecutionResult
	if err := readJSONFile(path, &saved); err != nil || saved.Status != "reserved" || saved.OperationID != occupied {
		t.Fatal(saved, err)
	}
}

func readJSONFile(path string, dst any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, dst)
}
