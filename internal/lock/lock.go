package lock

import (
	"fmt"
	"os"
	"syscall"
)

// SessionLock provides flock-based exclusive locking for a postman session.
type SessionLock struct {
	file *os.File
}

// NewSessionLock acquires an exclusive non-blocking lock on the given path.
// Returns an error if the lock is already held by another process.
func NewSessionLock(path string) (*SessionLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock already held: %w", err)
	}

	return &SessionLock{file: f}, nil
}

// Release releases the file lock and closes the lock file.
func (l *SessionLock) Release() error {
	if l.file == nil {
		return nil
	}
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		return err
	}
	return l.file.Close()
}
