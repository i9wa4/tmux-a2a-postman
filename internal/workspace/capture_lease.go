package workspace

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"syscall"
)

// captureLease is inert registration state. ProcessIncarnation and
// PaneIncarnation must eventually come from an authenticated supervisor and
// OS-specific process-object proof. A tmux title, PID, or caller-supplied token
// cannot establish either value. No production capture path reads this record.
type captureLease struct {
	Role               string  `json:"role"`
	Binding            Binding `json:"binding"`
	PaneIncarnation    string  `json:"pane_incarnation"`
	ProcessIncarnation string  `json:"process_incarnation"`
	Generation         uint64  `json:"generation"`
}

type captureLeaseRecord struct {
	Version    int            `json:"version"`
	Generation uint64         `json:"generation"`
	Leases     []captureLease `json:"leases"`
}

func validCaptureIncarnation(value string) bool {
	if len(value) == 0 || len(value) > 256 || strings.TrimSpace(value) != value {
		return false
	}
	for _, ch := range value {
		if ch < 0x21 || ch > 0x7e {
			return false
		}
	}
	return true
}

func validateCaptureLeaseRecord(record captureLeaseRecord) error {
	if record.Version != 1 || record.Generation == 0 || len(record.Leases) == 0 {
		return ErrUnknown
	}
	roles, panes := map[string]bool{}, map[string]bool{}
	bindings := make([]Binding, 0, len(record.Leases))
	for _, lease := range record.Leases {
		if lease.Role != lease.Binding.Role ||
			!positivePID(lease.Binding.PanePID) ||
			!validCaptureIncarnation(lease.PaneIncarnation) ||
			!validCaptureIncarnation(lease.ProcessIncarnation) ||
			lease.Generation == 0 || lease.Generation > record.Generation ||
			roles[lease.Role] || panes[lease.Binding.Owner.Pane] {
			return ErrUnknown
		}
		roles[lease.Role], panes[lease.Binding.Owner.Pane] = true, true
		bindings = append(bindings, lease.Binding)
	}
	if len(bindings) > 0 && ValidateBindings(bindings) != nil {
		return ErrUnknown
	}
	return nil
}

// registerCaptureLease is only a ledger operation inside a caller's
// Store.WithVerified callback. It does not authenticate evidence or authorize
// capture. The expected generation prevents stale concurrent registration.
func registerCaptureLease(ws *os.Root, snapshot Snapshot, candidate captureLease, expected uint64) (uint64, error) {
	return registerCaptureLeaseWithSync(ws, snapshot, candidate, expected, syncDirectory)
}

func registerCaptureLeaseWithSync(ws *os.Root, snapshot Snapshot, candidate captureLease, expected uint64, sync func(*os.Root) error) (uint64, error) {
	return registerCaptureLeaseWithHooks(ws, snapshot, candidate, expected, sync, nil)
}

// afterPartial is a test-only first-create hook. Production passes nil.
func registerCaptureLeaseWithHooks(ws *os.Root, snapshot Snapshot, candidate captureLease, expected uint64, sync func(*os.Root) error, afterPartial func() error) (uint64, error) {
	if _, err := validateSnapshot(snapshot); err != nil ||
		candidate.Role != candidate.Binding.Role ||
		!positivePID(candidate.Binding.PanePID) ||
		!validCaptureIncarnation(candidate.PaneIncarnation) ||
		!validCaptureIncarnation(candidate.ProcessIncarnation) {
		return 0, ErrUnknown
	}
	found := false
	for _, binding := range snapshot.Bindings {
		if binding.Role == candidate.Role && binding == candidate.Binding {
			found = true
		}
	}
	if !found {
		return 0, ErrUnknown
	}
	runtimeDir, err := privateDir(ws, "runtime", false)
	if err != nil {
		return 0, err
	}
	defer func() { _ = runtimeDir.Close() }()

	record := captureLeaseRecord{}
	err = readJSON(runtimeDir, "capture-leases.json", &record)
	missing := errors.Is(err, os.ErrNotExist)
	if missing {
		record.Version = 1
	}
	if err != nil && !missing || !missing && validateCaptureLeaseRecord(record) != nil {
		return 0, ErrUnknown
	}
	if record.Generation != expected || record.Generation == math.MaxUint64 {
		return 0, ErrUnknown
	}
	record.Generation++
	candidate.Generation = record.Generation
	// A fresh registration rejects stale leases for other roles.
	kept := make([]captureLease, 0, len(record.Leases)+1)
	for _, lease := range record.Leases {
		if lease.Role == candidate.Role {
			continue
		}
		found := false
		for _, binding := range snapshot.Bindings {
			if binding == lease.Binding {
				found = true
				break
			}
		}
		if !found {
			return 0, ErrUnknown
		}
		kept = append(kept, lease)
	}
	kept = append(kept, candidate)
	record.Leases = kept
	if err := validateCaptureLeaseRecord(record); err != nil {
		return 0, err
	}
	if missing {
		err = createCaptureLeaseJSON(runtimeDir, record, sync, afterPartial)
	} else {
		err = replaceJSON(runtimeDir, "capture-leases.json", record)
	}
	if err != nil {
		return 0, err
	}
	return record.Generation, nil
}

// createCaptureLeaseJSON publishes a complete first ledger only after its
// temporary file has been synced. A failed write leaves no final ledger.
func createCaptureLeaseJSON(root *os.Root, record captureLeaseRecord, sync func(*os.Root) error, afterPartial func() error) error {
	temp, err := randomName()
	if err != nil {
		return err
	}
	return createCaptureLeaseJSONWithTemp(root, record, sync, afterPartial, temp)
}

func createCaptureLeaseJSONWithTemp(root *os.Root, record captureLeaseRecord, sync func(*os.Root) error, afterPartial func() error, temp string) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temp) }()
	mid := len(data) / 2
	_, writeErr := file.Write(data[:mid])
	if writeErr == nil && afterPartial != nil {
		writeErr = afterPartial()
	}
	if writeErr == nil {
		_, writeErr = file.Write(data[mid:])
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}

	// Another writer may have created the final name since the initial read.
	// Never silently replace its bytes, including a malformed or dangling entry.
	if _, err := root.Lstat("capture-leases.json"); err == nil {
		return ErrUnknown
	} else if !errors.Is(err, os.ErrNotExist) {
		return ErrUnknown
	}
	if err := root.Rename(temp, "capture-leases.json"); err != nil {
		return err
	}
	return sync(root)
}
