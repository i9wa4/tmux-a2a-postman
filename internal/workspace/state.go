package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

func requestID(id string) bool {
	return len(id) == 37 && strings.HasPrefix(id, "ireq_") && messageID("m-"+id[5:])
}

// withView never registers, takes over, creates a lock or recovers a journal.
// Its shared lock excludes cooperating mail writers for one saved-file snapshot.
func (s Store) withView(ctx context.Context, rt Runtime, fn func(*os.Root, Snapshot) error) error {
	if rt == nil || fn == nil || !filepath.IsAbs(s.StateRoot) {
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
	info, err := os.Lstat(s.StateRoot)
	if err != nil {
		return err
	}
	if !privateInfo(info, true) {
		return ErrUnknown
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
	locks, err := privateDir(base, "locks", false)
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(locks.Close)
	lock, err := locks.OpenFile(name+".lock", os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(lock.Close)
	li, err := lock.Stat()
	if err != nil || !privateInfo(li, false) {
		return ErrUnknown
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return ErrBusy
		}
		return err
	}
	defer func(fd int) { _ = syscall.Flock(fd, syscall.LOCK_UN) }(int(lock.Fd()))
	pathInfo, err := locks.Lstat(name + ".lock")
	if err != nil || !privateInfo(pathInfo, false) || !os.SameFile(li, pathInfo) {
		return ErrUnknown
	}
	fresh, err := rt.Snapshot(ctx)
	if err != nil {
		return err
	}
	freshName, err := validateSnapshot(fresh)
	if err != nil || name != freshName || first.Root != fresh.Root || first.Caller != fresh.Caller {
		return ErrUnknown
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ws, err := privateDir(base, name, false)
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(ws.Close)
	var identity workspaceRecord
	if readJSON(ws, "workspace.json", &identity) != nil || identity != (workspaceRecord{1, fresh.Root, name}) {
		return ErrUnknown
	}
	runtimeDir, err := privateDir(ws, "runtime", false)
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(runtimeDir.Close)
	var saved runtimeRecord
	if readJSON(runtimeDir, "binding.json", &saved) != nil || saved.Version != 1 || !saved.Owner.valid() ||
		ValidateBindings(saved.Bindings) != nil || !sameRuntime(saved.Owner, fresh.Caller.Owner) {
		return ErrUnknown
	}
	bound := false
	for _, b := range saved.Bindings {
		if !sameRuntime(saved.Owner, b.Owner) {
			return ErrUnknown
		}
		if b == fresh.Caller {
			bound = true
		}
	}
	if !bound {
		return ErrUnknown
	}
	return fn(ws, fresh)
}

func openOwnBox(ws *os.Root, role, box string) (*os.Root, error) {
	agents, err := privateDir(ws, "agents", false)
	if err != nil {
		return nil, err
	}
	defer func(closeFn func() error) { _ = closeFn() }(agents.Close)
	agent, err := privateDir(agents, role, false)
	if err != nil {
		return nil, err
	}
	defer func(closeFn func() error) { _ = closeFn() }(agent.Close)
	return privateDir(agent, box, false)
}

func boundedEntries(root *os.Root) ([]os.DirEntry, error) {
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func(closeFn func() error) { _ = closeFn() }(f.Close)
	entries, err := f.ReadDir(2049)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > 2048 {
		return nil, errors.New("mail snapshot exceeds bounded scan")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func exactAnswer(request, answer Message) bool {
	if !request.NeedsReply || answer.From != request.To || answer.To != request.From || answer.FromSession != request.ToSession || answer.ToSession != request.FromSession {
		return false
	}
	if answer.ReplyTo == request.ID && answer.FillsInputRequestID == request.InputRequestID {
		return true
	}
	for _, ref := range answer.Replies {
		if ref.MessageID == request.ID && ref.InputRequestID == request.InputRequestID {
			return true
		}
	}
	return false
}
