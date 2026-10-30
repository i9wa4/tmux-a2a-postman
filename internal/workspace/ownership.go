package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
)

var ErrBusy = errors.New("workspace mutation is busy")

// Runtime supplies fresh native evidence; its methods must be bounded.
type Runtime interface {
	Snapshot(context.Context) (Snapshot, error)
	Probe(context.Context, Owner) (Liveness, error)
}

// Store owns durable workspaces below one application state directory.
// StateRoot is explicit so tests never use the caller's actual state.
type Store struct {
	StateRoot     string
	beforePublish func() error
	noticePublish func(*os.Root, string, appServerNoticeReservation) error
}

type workspaceRecord struct {
	Version int    `json:"version"`
	Root    string `json:"root"`
	Name    string `json:"name"`
}

type runtimeRecord struct {
	Version  int       `json:"version"`
	Owner    Owner     `json:"owner"`
	Bindings []Binding `json:"bindings"`
}

// DefaultStateRoot requires absolute HOME/XDG paths, without consulting cwd.
func DefaultStateRoot(home, xdg string) (string, error) {
	if !filepath.IsAbs(home) {
		return "", errors.New("HOME must be absolute")
	}
	if xdg == "" {
		xdg = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(xdg) {
		return "", errors.New("XDG_STATE_HOME must be absolute")
	}
	return filepath.Join(xdg, "tmux-a2a-postman"), nil
}

// WithVerified holds a nonblocking interprocess lock only through registration
// and fn's short file mutation. Never wait for a reply, notify, or execute Bash
// inside fn. The workspace handle is valid only during fn.
func (s Store) WithVerified(ctx context.Context, rt Runtime, fn func(*os.Root, Snapshot) error) error {
	if rt == nil || fn == nil {
		return ErrUnknown
	}
	first, err := rt.Snapshot(ctx)
	if err != nil {
		return err
	}
	name, err := validateSnapshot(first)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(s.StateRoot) {
		return errors.New("state root must be absolute")
	}
	if err := os.MkdirAll(s.StateRoot, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(s.StateRoot)
	if err != nil || !privateInfo(info, true) {
		return errors.New("state root is not a private real directory")
	}
	base, err := os.OpenRoot(s.StateRoot)
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(base.Close)
	opened, err := base.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return ErrUnknown
	}
	locks, err := privateDir(base, "locks", true)
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(locks.Close)
	lock, err := locks.OpenFile(name+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(lock.Close)
	lockInfo, err := lock.Stat()
	if err != nil || !privateInfo(lockInfo, false) {
		return errors.New("unsafe workspace lock")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrBusy
		}
		return err
	}
	defer func(fd int) { _ = syscall.Flock(fd, syscall.LOCK_UN) }(int(lock.Fd()))
	pathInfo, err := locks.Lstat(name + ".lock")
	if err != nil || !privateInfo(pathInfo, false) || !os.SameFile(lockInfo, pathInfo) {
		return ErrUnknown
	}
	fresh, err := rt.Snapshot(ctx)
	if err != nil {
		return err
	}
	freshName, err := validateSnapshot(fresh)
	if err != nil || freshName != name || fresh.Root != first.Root || !sameRuntime(first.Caller.Owner, fresh.Caller.Owner) {
		return fmt.Errorf("%w: runtime changed before workspace lock", ErrUnknown)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ws, err := privateDir(base, name, false)
	if errors.Is(err, os.ErrNotExist) {
		if err := s.createWorkspace(base, name, fresh); err != nil {
			return err
		}
		ws, err = privateDir(base, name, false)
	} else if err == nil {
		var identity workspaceRecord
		if readJSON(ws, "workspace.json", &identity) != nil || identity != (workspaceRecord{1, fresh.Root, name}) {
			_ = ws.Close()
			return errors.New("workspace original root mismatch or invalid identity record")
		}
		runtimeDir, openErr := privateDir(ws, "runtime", false)
		if openErr != nil {
			_ = ws.Close()
			return openErr
		}
		var previous runtimeRecord
		readErr := readJSON(runtimeDir, "binding.json", &previous)
		if readErr == nil && (previous.Version != 1 || !previous.Owner.valid() || ValidateBindings(previous.Bindings) != nil) {
			readErr = ErrUnknown
		}
		if readErr == nil {
			for _, b := range previous.Bindings {
				if !sameRuntime(previous.Owner, b.Owner) {
					readErr = ErrUnknown
				}
			}
		}
		if readErr == nil {
			_, _, readErr = ResolveOwner(&previous.Owner, fresh.Caller.Owner,
				func(owner Owner) (Liveness, error) { return rt.Probe(ctx, owner) })
		}
		if readErr == nil {
			readErr = replaceJSON(runtimeDir, "binding.json", runtimeRecord{1, fresh.Caller.Owner, fresh.Bindings})
		}
		_ = runtimeDir.Close()
		if readErr != nil {
			_ = ws.Close()
			return readErr
		}
	}
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(ws.Close)
	return fn(ws, fresh)
}

func validateSnapshot(s Snapshot) (string, error) {
	name, err := Name(s.Root)
	if err != nil || ValidateBindings(s.Bindings) != nil || !s.Caller.Owner.valid() {
		return "", ErrUnknown
	}
	found := false
	for _, b := range s.Bindings {
		if b == s.Caller {
			found = true
		}
	}
	if !found {
		return "", ErrUnknown
	}
	absent := map[string]bool{}
	for _, role := range s.AbsentRoles {
		if !binding.ValidateNodeName(role) || absent[role] {
			return "", ErrUnknown
		}
		for _, live := range s.Bindings {
			if live.Role == role {
				return "", ErrUnknown
			}
		}
		absent[role] = true
	}
	return name, nil
}

func privateInfo(info os.FileInfo, directory bool) bool {
	if info == nil || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0o700
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0o600 && stat.Nlink == 1
}

func privateDir(parent *os.Root, name string, create bool) (*os.Root, error) {
	if create {
		if err := parent.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, err
		}
	}
	before, err := parent.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !privateInfo(before, true) {
		return nil, errors.New("unsafe workspace directory: " + name)
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	after, err := root.Stat(".")
	if err != nil || !os.SameFile(before, after) {
		_ = root.Close()
		return nil, ErrUnknown
	}
	return root, nil
}

func readJSON(root *os.Root, name string, value any) error {
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(file.Close)
	info, err := file.Stat()
	if err != nil || !privateInfo(info, false) || info.Size() > 64*1024 {
		return errors.New("unsafe or oversized workspace record")
	}
	data, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return errors.New("cannot read workspace record")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("trailing workspace record data")
	}
	return nil
}

func randomName() (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", err
	}
	return ".new-" + hex.EncodeToString(nonce[:]), nil
}

func writeJSON(root *os.Root, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > 64*1024 {
		return errors.New("oversized workspace record")
	}
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func syncDirectory(root *os.Root) error {
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(file.Close)
	return file.Sync()
}

func replaceJSON(root *os.Root, name string, value any) error {
	temp, err := randomName()
	if err != nil {
		return err
	}
	defer func(removeFn func(string) error, name string) { _ = removeFn(name) }(root.Remove, temp)
	if err := writeJSON(root, temp, value); err != nil {
		return err
	}
	if err := root.Rename(temp, name); err != nil {
		return err
	}
	return syncDirectory(root)
}

func (s Store) createWorkspace(base *os.Root, name string, snapshot Snapshot) error {
	temp, err := randomName()
	if err != nil {
		return err
	}
	if err := base.Mkdir(temp, 0o700); err != nil {
		return err
	}
	defer func(removeFn func(string) error, name string) { _ = removeFn(name) }(base.RemoveAll, temp)
	staging, err := privateDir(base, temp, false)
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(staging.Close)
	if err := writeJSON(staging, "workspace.json", workspaceRecord{1, snapshot.Root, name}); err != nil {
		return err
	}
	runtimeDir, err := privateDir(staging, "runtime", true)
	if err != nil {
		return err
	}
	err = writeJSON(runtimeDir, "binding.json", runtimeRecord{1, snapshot.Caller.Owner, snapshot.Bindings})
	if err == nil {
		err = syncDirectory(runtimeDir)
	}
	_ = runtimeDir.Close()
	if err != nil {
		return err
	}
	if err := syncDirectory(staging); err != nil {
		return err
	}
	if s.beforePublish != nil {
		if err := s.beforePublish(); err != nil {
			return err
		}
	}
	if err := base.Rename(temp, name); err != nil {
		return err
	}
	return syncDirectory(base)
}
