package inputarbiter

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
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

var (
	ErrBindingInvalid   = errors.New("invalid human pane binding")
	ErrBindingMissing   = errors.New("human pane binding has no candidate")
	ErrBindingAmbiguous = errors.New("human pane binding has multiple candidates")
	ErrBindingStale     = errors.New("human pane binding changed during claim")
)

// BindingSpec selects a desired pane. The observed server and pane fences are
// obtained from tmux, never from this configuration.
type BindingSpec struct {
	InstallationIDFile string
	SocketPath         string
	SessionName        string
	PaneTitle          string
}

type ObservedPane struct {
	ID          string
	SessionName string
	Title       string
}

type Observation struct {
	SocketPath string
	ServerPID  int
	SocketDev  uint64
	SocketIno  uint64
	Panes      []ObservedPane
}

type BindingObserver interface {
	Observe(context.Context, string) (Observation, error)
}

type Binder struct {
	Store    Store
	Observer BindingObserver
}

type Binding struct {
	Key   ServerKey
	Owner Owner
}

// WithholdConfiguredPane removes the opted-in pane from legacy discovery before
// its map can be published. This Stage0 path records ownership only; it never
// grants the legacy router permission to notify or submit to that pane.
func WithholdConfiguredPane(ctx context.Context, cfg *config.Config, nodes map[string]discovery.NodeInfo) error {
	return withholdConfiguredPane(ctx, cfg, nodes, TmuxBindingObserver{})
}

// ConfiguredNodeKey is used only to exclude a physical pane from legacy pane
// marker preclaims. An incomplete opt-in has no usable key.
func ConfiguredNodeKey(cfg *config.Config) string {
	if cfg == nil || cfg.HumanPaneBinding == nil || !cfg.HumanPaneBinding.Enabled {
		return ""
	}
	requested := cfg.HumanPaneBinding
	if requested.SessionName == "" || requested.PaneTitle == "" {
		return ""
	}
	return requested.SessionName + ":" + requested.PaneTitle
}

func withholdConfiguredPane(ctx context.Context, cfg *config.Config, nodes map[string]discovery.NodeInfo, observer BindingObserver) error {
	if cfg == nil || cfg.HumanPaneBinding == nil || !cfg.HumanPaneBinding.Enabled {
		return nil
	}
	requested := cfg.HumanPaneBinding
	if requested.SessionName == "" || requested.PaneTitle == "" {
		// A malformed opt-in cannot identify which legacy route is unsafe.
		clear(nodes)
		return ErrBindingInvalid
	}
	delete(nodes, requested.SessionName+":"+requested.PaneTitle)
	_, err := (Binder{
		Store:    Store{Root: filepath.Join(filepath.Dir(requested.InstallationIDFile), "inputarbiter")},
		Observer: observer,
	}).Bind(ctx, BindingSpec{
		InstallationIDFile: requested.InstallationIDFile,
		SocketPath:         requested.SocketPath,
		SessionName:        requested.SessionName,
		PaneTitle:          requested.PaneTitle,
	})
	return err
}

// Bind claims only a uniquely observed live pane. It does not publish a route,
// set a pane marker, or submit input. A changed incumbent requires an explicit
// fenced Rebind by a caller that can reconcile the old agent context.
func (b Binder) Bind(ctx context.Context, spec BindingSpec) (Binding, error) {
	installationID, socket, err := validateBindingSpec(spec)
	if err != nil {
		return Binding{}, err
	}
	observer := b.Observer
	if observer == nil {
		observer = TmuxBindingObserver{}
	}
	first, err := observer.Observe(ctx, socket)
	if err != nil {
		if errors.Is(err, ErrBindingStale) {
			return Binding{}, ErrBindingStale
		}
		return Binding{}, fmt.Errorf("%w: first observation: %v", ErrBindingInvalid, err)
	}
	owner, err := candidateOwner(first, spec, socket)
	if err != nil {
		return Binding{}, err
	}
	fresh, err := observer.Observe(ctx, socket)
	if err != nil {
		return Binding{}, fmt.Errorf("%w: pre-claim observation: %v", ErrBindingStale, err)
	}
	if !sameObservedCandidate(first, fresh, spec, socket, owner) {
		return Binding{}, ErrBindingStale
	}
	key := ServerKey{InstallationID: installationID, SocketPath: socket}
	// A new conversation ID remains local until Store.Claim commits. A retry
	// against an existing identical pane recovers its persisted conversation.
	conversationID, err := newBindingID()
	if err != nil {
		return Binding{}, err
	}
	owner.ConversationID = conversationID
	claimed, err := b.Store.Claim(key, owner)
	if errors.Is(err, ErrOwned) {
		state, readErr := b.Store.Read(key)
		if readErr != nil {
			return Binding{}, readErr
		}
		if state.Owner.Designation != owner.Designation || state.Owner.ServerIncarnation != owner.ServerIncarnation || state.Owner.PaneID != owner.PaneID || state.Owner.PaneIncarnation != owner.PaneIncarnation {
			return Binding{}, ErrOwned
		}
		owner.ConversationID = state.Owner.ConversationID
		claimed, err = b.Store.Claim(key, owner)
	}
	if err != nil {
		return Binding{}, err
	}
	last, err := observer.Observe(ctx, socket)
	if err != nil || !sameObservedCandidate(first, last, spec, socket, owner) {
		return Binding{}, ErrBindingStale
	}
	return Binding{Key: key, Owner: claimed}, nil
}

func validateBindingSpec(spec BindingSpec) (string, string, error) {
	if spec.InstallationIDFile == "" || spec.SessionName == "" || spec.PaneTitle == "" || spec.SocketPath == "" {
		return "", "", ErrBindingInvalid
	}
	for _, path := range []string{spec.InstallationIDFile, spec.SocketPath} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return "", "", ErrBindingInvalid
		}
	}
	installationPath, err := filepath.EvalSymlinks(spec.InstallationIDFile)
	if err != nil || installationPath != spec.InstallationIDFile {
		return "", "", ErrBindingInvalid
	}
	socket, err := filepath.EvalSymlinks(spec.SocketPath)
	if err != nil || socket != spec.SocketPath {
		return "", "", ErrBindingInvalid
	}
	info, err := os.Lstat(spec.InstallationIDFile)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return "", "", ErrBindingInvalid
	}
	file, err := os.Open(spec.InstallationIDFile)
	if err != nil {
		return "", "", ErrBindingInvalid
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", "", ErrBindingInvalid
	}
	stat, ok := opened.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return "", "", ErrBindingInvalid
	}
	contents, err := io.ReadAll(io.LimitReader(file, 257))
	if err != nil || len(contents) > 256 {
		return "", "", ErrBindingInvalid
	}
	id := strings.TrimSpace(string(contents))
	if id == "" || strings.ContainsAny(id, "\x00\r\n") {
		return "", "", ErrBindingInvalid
	}
	return id, socket, nil
}

func candidateOwner(obs Observation, spec BindingSpec, socket string) (Owner, error) {
	if obs.SocketPath != socket || obs.ServerPID <= 0 || obs.SocketIno == 0 {
		return Owner{}, ErrBindingInvalid
	}
	var match ObservedPane
	count := 0
	for _, pane := range obs.Panes {
		if pane.ID == "" || pane.SessionName == "" {
			return Owner{}, ErrBindingInvalid
		}
		if len(pane.ID) < 2 || pane.ID[0] != '%' {
			return Owner{}, ErrBindingInvalid
		}
		if _, err := strconv.ParseUint(pane.ID[1:], 10, 64); err != nil {
			return Owner{}, ErrBindingInvalid
		}
		if pane.SessionName == spec.SessionName && pane.Title == spec.PaneTitle {
			match = pane
			count++
		}
	}
	if count == 0 {
		return Owner{}, ErrBindingMissing
	}
	if count != 1 {
		return Owner{}, ErrBindingAmbiguous
	}
	serverIncarnation := fmt.Sprintf("%d:%d:%d", obs.ServerPID, obs.SocketDev, obs.SocketIno)
	designation, _ := json.Marshal([2]string{spec.SessionName, spec.PaneTitle})
	return Owner{
		ServerIncarnation: serverIncarnation,
		PaneID:            match.ID,
		PaneIncarnation:   serverIncarnation + ":" + match.ID,
		Designation:       string(designation),
	}, nil
}

func sameObservedCandidate(first, next Observation, spec BindingSpec, socket string, owner Owner) bool {
	proposed, err := candidateOwner(next, spec, socket)
	return err == nil && first.SocketPath == next.SocketPath &&
		proposed.ServerIncarnation == owner.ServerIncarnation &&
		proposed.PaneID == owner.PaneID && proposed.PaneIncarnation == owner.PaneIncarnation &&
		proposed.Designation == owner.Designation
}

func newBindingID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}

// TmuxBindingObserver reads the raw pane list from the explicitly selected
// socket. It rejects socket replacement during its observation.
type TmuxBindingObserver struct{}

func (TmuxBindingObserver) Observe(ctx context.Context, socket string) (Observation, error) {
	before, err := socketIdentity(socket)
	if err != nil {
		return Observation{}, err
	}
	identity, err := exec.CommandContext(ctx, "tmux", "-S", socket, "display-message", "-p", "#{socket_path}\t#{pid}").Output()
	if err != nil {
		return Observation{}, err
	}
	fields := strings.Split(strings.TrimSpace(string(identity)), "\t")
	if len(fields) != 2 {
		return Observation{}, ErrBindingInvalid
	}
	connectedSocket, err := filepath.EvalSymlinks(fields[0])
	if err != nil || connectedSocket != socket {
		return Observation{}, ErrBindingInvalid
	}
	pid, err := strconv.Atoi(fields[1])
	if err != nil || pid <= 0 {
		return Observation{}, ErrBindingInvalid
	}
	raw, err := exec.CommandContext(ctx, "tmux", "-S", socket, "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		return Observation{}, err
	}
	ids, err := parsePaneIDs(raw)
	if err != nil {
		return Observation{}, err
	}
	obs := Observation{SocketPath: socket, ServerPID: pid, SocketDev: before.dev, SocketIno: before.ino}
	for _, id := range ids {
		rawPane, err := exec.CommandContext(ctx, "tmux", "-S", socket, "display-message", "-p", "-t", id, "#{pane_id}\t#{session_name}\t#{pane_title}").Output()
		if err != nil {
			return Observation{}, ErrBindingStale
		}
		pane, err := parsePaneObservation(rawPane, id)
		if err != nil {
			return Observation{}, err
		}
		obs.Panes = append(obs.Panes, pane)
	}
	// A pane created while its peers were inspected must not be missed.
	rawAfter, err := exec.CommandContext(ctx, "tmux", "-S", socket, "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		return Observation{}, ErrBindingStale
	}
	afterIDs, err := parsePaneIDs(rawAfter)
	if err != nil {
		return Observation{}, err
	}
	slices.Sort(ids)
	slices.Sort(afterIDs)
	if !slices.Equal(ids, afterIDs) {
		return Observation{}, ErrBindingStale
	}
	after, err := socketIdentity(socket)
	if err != nil || before != after {
		return Observation{}, ErrBindingStale
	}
	return obs, nil
}

// Only pane IDs are accepted from the list command. A title cannot introduce
// a second record because title data is never present in this output.
func parsePaneIDs(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[len(raw)-1] != '\n' {
		return nil, ErrBindingInvalid
	}
	ids := make([]string, 0)
	seen := make(map[string]bool)
	for _, id := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if !validPaneID(id) || seen[id] {
			return nil, ErrBindingInvalid
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

func parsePaneObservation(raw []byte, expectedID string) (ObservedPane, error) {
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		return ObservedPane{}, ErrBindingInvalid
	}
	line := bytes.TrimSuffix(raw, []byte{'\n'})
	if bytes.ContainsAny(line, "\r\n") {
		return ObservedPane{}, ErrBindingInvalid
	}
	parts := bytes.Split(line, []byte{'\t'})
	if len(parts) != 3 || string(parts[0]) != expectedID || !validPaneID(expectedID) || len(parts[1]) == 0 {
		return ObservedPane{}, ErrBindingInvalid
	}
	return ObservedPane{ID: expectedID, SessionName: string(parts[1]), Title: string(parts[2])}, nil
}

func validPaneID(id string) bool {
	if len(id) < 2 || id[0] != '%' {
		return false
	}
	for _, digit := range id[1:] {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

type socketID struct{ dev, ino uint64 }

func socketIdentity(path string) (socketID, error) {
	info, err := os.Stat(path)
	if err != nil {
		return socketID{}, err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return socketID{}, ErrBindingInvalid
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Ino == 0 {
		return socketID{}, ErrBindingInvalid
	}
	return socketID{dev: uint64(stat.Dev), ino: stat.Ino}, nil
}
