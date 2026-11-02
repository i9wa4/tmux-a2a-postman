package workspace

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"
)

// Text files contain no transport envelope. The binding and ordinary mail
// archives retain correlation. All accesses remain anchored to private roots.
func externalText(root *os.Root, name string) ([]byte, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	before, err := f.Stat()
	if err != nil || !privateInfo(before, false) || before.Size() < 1 || before.Size() > 64*1024 {
		return nil, errors.New("external text must be a private regular file of 1..65536 bytes")
	}
	raw, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	pathInfo, pathErr := root.Lstat(name)
	if err != nil || pathErr != nil || !os.SameFile(before, pathInfo) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || len(raw) > 64*1024 || !utf8.Valid(raw) || bytes.IndexByte(raw, 0) >= 0 {
		return nil, errors.New("external text changed or is invalid")
	}
	return raw, nil
}

func ensureExternalText(root *os.Root, name string, raw []byte) error {
	prior, err := externalText(root, name)
	if err == nil {
		if !bytes.Equal(prior, raw) {
			return errors.New("external filename conflicts with existing text")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temp, err := randomName()
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temp) }()
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := root.Rename(temp, name); err != nil {
		return err
	}
	return syncDirectory(root)
}

func openExternalRoot(binding ExternalBinding) (*os.Root, *os.File, error) {
	if !filepath.IsAbs(binding.Root) || filepath.Clean(binding.Root) != binding.Root {
		return nil, nil, ErrUnknown
	}
	if err := os.Mkdir(binding.Root, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, nil, err
	}
	resolved, err := filepath.EvalSymlinks(binding.Root)
	if err != nil || resolved != binding.Root {
		return nil, nil, errors.New("external root must not contain symlinks")
	}
	before, err := os.Lstat(binding.Root)
	if err != nil || !privateInfo(before, true) {
		return nil, nil, errors.New("external root must be an owned private directory")
	}
	root, err := os.OpenRoot(binding.Root)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*os.Root, *os.File, error) { _ = root.Close(); return nil, nil, err }
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		return fail(ErrUnknown)
	}
	lock, err := root.OpenFile(".lock", os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if errors.Is(err, os.ErrExist) {
		lock, err = root.OpenFile(".lock", os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		return fail(err)
	}
	failedLock := func(err error) (*os.Root, *os.File, error) { _ = lock.Close(); return fail(err) }
	info, err := lock.Stat()
	if err != nil || !privateInfo(info, false) {
		return failedLock(ErrUnknown)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			err = ErrBusy
		}
		return failedLock(err)
	}
	current, err := root.Lstat(".lock")
	if err != nil || !os.SameFile(info, current) {
		return failedLock(ErrUnknown)
	}
	var saved ExternalBinding
	err = readJSON(root, "binding.json", &saved)
	if errors.Is(err, os.ErrNotExist) {
		err = writeJSON(root, "binding.json", binding)
	} else if err == nil && saved != binding {
		err = errors.New("external root belongs to a different binding; provision a separate root")
	}
	if err != nil {
		return failedLock(err)
	}
	for _, name := range []string{"inbox", "outbox", "read"} {
		dir, err := privateDir(root, name, true)
		if err != nil {
			return failedLock(err)
		}
		_ = dir.Close()
	}
	if err := syncDirectory(root); err != nil {
		return failedLock(err)
	}
	return root, lock, nil
}

func externalFilename(name string) bool {
	if !strings.HasSuffix(name, ".md") || len(name) > 128 || len(name) < 4 {
		return false
	}
	for _, c := range strings.TrimSuffix(name, ".md") {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}
