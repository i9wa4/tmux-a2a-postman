package workspace

// The status cache is an observation namespace, never an agent registry.
import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	statusLimit           = 4 * 1024 * 1024
	StatusCaptureInterval = 30 * time.Second
	StatusRefreshBudget   = 2 * time.Second
)

// ServerContext is supplied explicitly by the tmux caller. PID zero means the
// caller does not know it; a reader never claims to verify current liveness.
type ServerContext struct {
	Socket string
	PID    int
}

func ParseServerContext(value string) (ServerContext, error) {
	last := strings.LastIndexByte(value, ',')
	if last < 0 {
		return ServerContext{}, errors.New("TMUX must contain an absolute socket, PID and session index")
	}
	before := strings.LastIndexByte(value[:last], ',')
	if before < 0 {
		return ServerContext{}, ErrUnknown
	}
	socket := value[:before]
	pid, err := strconv.Atoi(value[before+1 : last])
	if err != nil || pid < 0 || !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || strings.ContainsAny(socket, "\x00\r\n") {
		return ServerContext{}, errors.New("invalid explicit TMUX server context")
	}
	if _, err := strconv.ParseUint(value[last+1:], 10, 64); err != nil {
		return ServerContext{}, ErrUnknown
	}
	return ServerContext{socket, pid}, nil
}

type StatusServer struct {
	Socket          string `json:"socket"`
	CanonicalSocket string `json:"canonical_socket"`
	PID             int    `json:"pid"`
	Incarnation     string `json:"incarnation"`
}
type StatusCoverage struct {
	Enumerated  int    `json:"enumerated"`
	Captured    int    `json:"captured"`
	Reused      int    `json:"reused"`
	Unavailable int    `json:"unavailable"`
	Complete    bool   `json:"complete"`
	Reason      string `json:"reason,omitempty"`
}
type ServerStatus struct {
	Version   int             `json:"version"`
	Server    StatusServer    `json:"server"`
	StartedAt string          `json:"started_at"`
	AsOf      string          `json:"as_of"`
	Coverage  StatusCoverage  `json:"coverage"`
	Sessions  []StatusSession `json:"sessions"`
}
type StatusSession struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Windows []StatusWindow `json:"windows"`
}
type StatusWindow struct {
	ID     string       `json:"id"`
	Index  int          `json:"index"`
	Name   string       `json:"name"`
	Layout string       `json:"layout"`
	Panes  []StatusPane `json:"panes"`
}
type StatusPane struct {
	ID        string         `json:"id"`
	PID       string         `json:"pid"`
	Title     string         `json:"title"`
	Workspace string         `json:"workspace,omitempty"`
	Top       int            `json:"top"`
	Left      int            `json:"left"`
	Dead      bool           `json:"dead"`
	Linked    bool           `json:"linked"`
	Capture   *StatusCapture `json:"capture"`
	Status    *NodeStatus    `json:"status"`
	Scope     *StatusScope   `json:"scope"`
	Reason    string         `json:"reason,omitempty"`
}
type StatusCapture struct {
	At          string  `json:"at"`
	ChangedAt   *string `json:"changed_at"`
	Fingerprint string  `json:"fingerprint"`
	File        string  `json:"file"`
}

// Scope is saved configuration attribution, not permission to manage/send/read.
type StatusScope struct {
	Config      string              `json:"config"`
	Fingerprint string              `json:"fingerprint"`
	Edges       map[string][]string `json:"edges"`
	Interface   string              `json:"interface_node"`
	Sessions    []StatusRoute       `json:"sessions"`
}
type StatusRoute struct {
	Name      string `json:"name"`
	Parent    string `json:"parent"`
	Interface string `json:"interface_node"`
}

func statusKey(socket string) string {
	sum := sha256.Sum256([]byte(socket))
	return hex.EncodeToString(sum[:])
}

// %status cannot be produced by Name: literal percent is escaped as %25.
func (s Store) statusDir(server ServerContext, create bool) (*os.Root, error) {
	if !filepath.IsAbs(s.StateRoot) || !filepath.IsAbs(server.Socket) {
		return nil, ErrUnknown
	}
	if create {
		if err := os.MkdirAll(s.StateRoot, 0o700); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(s.StateRoot)
	if err != nil {
		return nil, err
	}
	if !stateRootInfo(info) {
		return nil, errors.New("unsafe status state root")
	}
	root, err := os.OpenRoot(s.StateRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return nil, ErrUnknown
	}
	ns, err := privateDir(root, "%status", create)
	if err != nil {
		return nil, err
	}
	defer func() { _ = ns.Close() }()
	return privateDir(ns, statusKey(server.Socket), create)
}

func readStatusFile(dir *os.Root, name string) ([]byte, error) {
	f, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil || !privateInfo(info, false) || info.Size() > statusLimit {
		return nil, errors.New("unsafe or oversized status snapshot")
	}
	raw, err := io.ReadAll(io.LimitReader(f, statusLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > statusLimit {
		return nil, errors.New("oversized status snapshot")
	}
	return raw, nil
}

func decodeStatus(raw []byte, server ServerContext) (ServerStatus, error) {
	var value ServerStatus
	if err := uniqueStatusJSON(raw); err != nil {
		return value, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return value, errors.New("trailing status data")
	}
	if value.Version != 1 || value.Server.Socket != server.Socket || value.Server.PID <= 0 || value.Server.Incarnation == "" || !filepath.IsAbs(value.Server.CanonicalSocket) || value.Sessions == nil {
		return value, errors.New("incompatible server status snapshot")
	}
	parts := strings.Split(value.Server.Incarnation, ":")
	if len(parts) != 5 || parts[0] != strconv.Itoa(value.Server.PID) {
		return value, errors.New("invalid saved server incarnation")
	}
	for _, part := range parts {
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return value, errors.New("invalid saved server incarnation")
		}
	}
	if server.PID != 0 && value.Server.PID != server.PID {
		return value, errors.New("saved status belongs to a different server PID; refresh required")
	}
	for _, at := range []string{value.StartedAt, value.AsOf} {
		if _, err := time.Parse(time.RFC3339Nano, at); err != nil {
			return value, errors.New("invalid status collection timestamp")
		}
	}
	sessions := map[string]bool{}
	total := 0
	for _, session := range value.Sessions {
		if !statusNativeID(session.ID, '$') || sessions[session.ID] || session.Windows == nil {
			return value, errors.New("invalid saved session")
		}
		sessions[session.ID] = true
		windows := map[string]bool{}
		panes := map[string]bool{}
		for _, window := range session.Windows {
			if !statusNativeID(window.ID, '@') || windows[window.ID] || window.Index < 0 || window.Panes == nil {
				return value, errors.New("invalid saved window")
			}
			windows[window.ID] = true
			for _, pane := range window.Panes {
				total++
				if !statusNativeID(pane.ID, '%') || panes[pane.ID] || !positivePID(pane.PID) || pane.Top < 0 || pane.Left < 0 {
					return value, errors.New("invalid saved pane")
				}
				panes[pane.ID] = true
				if pane.Capture != nil {
					c := pane.Capture
					at, e := time.Parse(time.RFC3339Nano, c.At)
					if e != nil || !strings.HasPrefix(c.Fingerprint, "sha256:") || len(c.Fingerprint) != 71 || filepath.Base(c.File) != c.File || !strings.HasSuffix(c.File, ".json") {
						return value, errors.New("invalid saved capture")
					}
					if c.ChangedAt != nil {
						changed, e := time.Parse(time.RFC3339Nano, *c.ChangedAt)
						if e != nil || changed.After(at) {
							return value, errors.New("invalid saved change timestamp")
						}
					}
				}
			}
		}
	}
	if total > 1024 {
		return value, errors.New("saved pane limit exceeded")
	}
	c := value.Coverage
	if c.Enumerated != total || c.Captured < 0 || c.Reused < 0 || c.Unavailable < 0 || c.Captured+c.Reused+c.Unavailable != total || c.Complete != (c.Unavailable == 0) {
		return value, errors.New("invalid saved coverage")
	}
	return value, nil
}

// ReadServerStatus performs no native RPC, discovery, mail scan, mkdir, lock,
// recovery, registration or persistent logging. Raw bytes are returned verbatim.
func (s Store) ReadServerStatus(server ServerContext) ([]byte, ServerStatus, error) {
	dir, err := s.statusDir(server, false)
	if err != nil {
		return nil, ServerStatus{}, err
	}
	defer func() { _ = dir.Close() }()
	raw, err := readStatusFile(dir, "status.json")
	if err != nil {
		return nil, ServerStatus{}, err
	}
	value, err := decodeStatus(raw, server)
	return raw, value, err
}

func publishStatus(dir *os.Root, server ServerContext, value ServerStatus, beforeRename, afterRename func() error) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > statusLimit {
		return errors.New("status snapshot exceeds publication limit")
	}
	if _, err := decodeStatus(raw, server); err != nil {
		return err
	}
	name, err := randomName()
	if err != nil {
		return err
	}
	defer func() { _ = dir.Remove(name) }()
	f, err := dir.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if beforeRename != nil {
		if err := beforeRename(); err != nil {
			return err
		}
	}
	if err := dir.Rename(name, "status.json"); err != nil {
		return err
	}
	if afterRename != nil {
		if err := afterRename(); err != nil {
			return fmt.Errorf("snapshot renamed; durability uncertain: %w", err)
		}
	}
	if err := syncDirectory(dir); err != nil {
		return fmt.Errorf("snapshot renamed; durability uncertain: %w", err)
	}
	return nil
}

func statusNativeID(id string, prefix byte) bool {
	if !nativeID(id, prefix) {
		return false
	}
	n, err := strconv.Atoi(id[1:])
	return err == nil && n >= 0 && strconv.Itoa(n) == id[1:]
}

// Reject duplicate keys and excessive nesting before interpreting a cache.
// Different consumers must not disagree about which server/identity was saved.
func uniqueStatusJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 32 {
			return errors.New("status JSON nesting limit")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("duplicate/invalid status JSON key")
				}
				seen[name] = true
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("invalid status JSON delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if err := visit(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing status JSON")
	}
	return nil
}
