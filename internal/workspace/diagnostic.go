package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"regexp"
	"syscall"
	"time"
)

var diagnosticName = regexp.MustCompile(`^inv-[0-9a-f]{32}$`)

// OpenDiagnostic uses normal verified registration, never a guessed actor or
// fallback directory. Only operational CLIs invoke it. Log file creation and
// writes happen AFTER all mailbox locks have been released.
func (s Store) OpenDiagnostic(ctx context.Context, rt Runtime, date, id string) (io.WriteCloser, error) {
	if !diagnosticName.MatchString(id) {
		return nil, ErrUnknown
	}
	d, err := time.Parse("2006-01-02", date)
	if err != nil || d.Format("2006-01-02") != date {
		return nil, ErrUnknown
	}
	var owned *os.Root
	var role string
	open := func(ws *os.Root, snapshot Snapshot) error {
		var err error
		owned, err = ws.OpenRoot(".")
		role = snapshot.Caller.Role
		return err
	}
	err = s.withView(ctx, rt, open)
	if errors.Is(err, os.ErrNotExist) {
		err = s.WithVerified(ctx, rt, open)
	}
	if err != nil {
		if owned != nil {
			_ = owned.Close()
		}
		return nil, err
	}
	defer func() { _ = owned.Close() }()
	agents, err := privateDir(owned, "agents", true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = agents.Close() }()
	agent, err := privateDir(agents, role, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = agent.Close() }()
	logs, err := privateDir(agent, "logs", true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = logs.Close() }()
	day, err := privateDir(logs, date, true)
	if err != nil {
		return nil, err
	}
	defer func() { _ = day.Close() }()
	f, err := day.OpenFile(id+".jsonl", os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !privateInfo(info, false) {
		_ = f.Close()
		return nil, ErrUnknown
	}
	return f, nil
}

// DiagnosticRuntimeFingerprint avoids copying socket paths or host details.
func DiagnosticRuntimeFingerprint(owner Owner) string {
	b := sha256.Sum256([]byte(owner.Server + "\x00" + owner.Incarnation))
	return "sha256:" + hex.EncodeToString(b[:])
}
