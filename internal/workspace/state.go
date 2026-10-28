package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

type Obligation struct {
	MessageID      string `json:"message_id"`
	InputRequestID string `json:"input_request_id"`
	Peer           string `json:"peer"`
	Claimed        bool   `json:"claimed"`
}

type SelfState struct {
	Known             bool          `json:"known"`
	Workspace         string        `json:"workspace,omitempty"`
	Node              string        `json:"node,omitempty"`
	Mailbox           string        `json:"mailbox,omitempty"`
	AsOf              string        `json:"as_of"`
	Unread            *int          `json:"unread"`
	Inbound           []Obligation  `json:"inbound"`
	Outbound          []Obligation  `json:"outbound"`
	Peers             []string      `json:"peers"`
	NextAction        string        `json:"next_action"`
	Activity          ActivityState `json:"activity"`
	UnavailableReason string        `json:"unavailable_reason,omitempty"`
}

// ActivityState summarizes saved pane evidence without treating a capture as
// proof of ongoing work. Mail obligations remain independent of this field.
type ActivityState struct {
	Status     string `json:"status"`
	Reason     string `json:"reason"`
	ObservedAt string `json:"observed_at,omitempty"`
	AgeSeconds *int64 `json:"age_seconds,omitempty"`
}

const activityFreshness = 5 * time.Minute

type SendResult struct {
	Message Message   `json:"message"`
	Self    SelfState `json:"self"`
}

type PopResult struct {
	Claim *Claimed  `json:"claim"`
	Self  SelfState `json:"self"`
}

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

func readOwnMail(ws *os.Root, role, box string) ([]Message, error) {
	root, err := openOwnBox(ws, role, box)
	if err != nil {
		return nil, err
	}
	defer func(closeFn func() error) { _ = closeFn() }(root.Close)
	entries, err := boundedEntries(root)
	if err != nil {
		return nil, err
	}
	messages := []Message{}
	for _, entry := range entries {
		name := entry.Name()
		if box == "read" && strings.HasSuffix(name, ".pop.json") {
			id := strings.TrimSuffix(name, ".pop.json")
			var receipt ClaimReceipt
			if !messageID(id) || readJSON(root, name, &receipt) != nil || receipt.Version != 1 ||
				receipt.ID != id || receipt.Role != role {
				return nil, errors.New("invalid saved POP receipt")
			}
			if _, err := time.Parse(time.RFC3339Nano, receipt.ClaimedAt); err != nil {
				return nil, err
			}
			if _, err := root.Lstat(id + ".md"); err != nil {
				return nil, err
			}
			continue
		}
		id := strings.TrimSuffix(name, ".md")
		if !messageID(id) || name != id+".md" {
			return nil, errors.New("unexpected own mailbox entry")
		}
		msg, err := readMessage(root, name)
		if err != nil {
			return nil, err
		}
		if msg.ID != id || (box == "sent" && msg.From != role) || (box != "sent" && msg.To != role) {
			return nil, errors.New("own mailbox provenance mismatch")
		}
		if box == "read" {
			var receipt ClaimReceipt
			if readJSON(root, id+".pop.json", &receipt) != nil {
				return nil, errors.New("claimed mail lacks durable receipt")
			}
		}
		messages = append(messages, msg)
	}
	return messages, nil
}

// readOwnActivity reads only this role's bounded saved captures. Invalid or
// contradictory evidence downgrades activity, never the file-derived mail
// facts. In particular, equal pane snapshots do not establish work progress.
func readOwnActivity(ws *os.Root, caller Binding, now time.Time) ActivityState {
	unknown := ActivityState{Status: "unknown", Reason: "no saved pane observation"}
	agents, err := privateDir(ws, "agents", false)
	if err != nil {
		unknown.Reason = "saved pane evidence unavailable"
		return unknown
	}
	defer func(closeFn func() error) { _ = closeFn() }(agents.Close)
	agent, err := privateDir(agents, caller.Role, false)
	if err != nil {
		unknown.Reason = "saved pane evidence unavailable"
		return unknown
	}
	defer func(closeFn func() error) { _ = closeFn() }(agent.Close)
	captures, err := privateDir(agent, "captures", false)
	if errors.Is(err, os.ErrNotExist) {
		return unknown
	}
	if err != nil {
		unknown.Reason = "saved pane evidence unavailable"
		return unknown
	}
	defer func(closeFn func() error) { _ = closeFn() }(captures.Close)
	entries, err := boundedEntries(captures)
	if err != nil {
		unknown.Reason = "saved pane evidence unavailable"
		return unknown
	}
	var latest CaptureResult
	var latestAt time.Time
	for _, entry := range entries {
		name := entry.Name()
		id := strings.TrimSuffix(name, ".json")
		if name != id+".json" || !messageID(id) {
			unknown.Reason = "saved pane evidence invalid"
			return unknown
		}
		var record CaptureResult
		if readJSON(captures, name, &record) != nil || record.Version != 1 || record.MessageID != id {
			unknown.Reason = "saved pane evidence invalid"
			return unknown
		}
		when, err := time.Parse(time.RFC3339Nano, record.CapturedAt)
		if err != nil {
			unknown.Reason = "saved pane evidence invalid"
			return unknown
		}
		if latestAt.IsZero() || when.After(latestAt) {
			latest, latestAt = record, when
		}
	}
	if latestAt.IsZero() {
		return unknown
	}
	unknown.ObservedAt = latest.CapturedAt
	if latestAt.After(now) {
		unknown.Reason = "saved pane observation conflicts with current time"
		return unknown
	}
	age := int64(now.Sub(latestAt).Seconds())
	unknown.AgeSeconds = &age
	if latest.Incarnation != caller.Owner.Incarnation {
		unknown.Reason = "saved pane observation conflicts with current incarnation"
		return unknown
	}
	var own *PaneObservation
	for i := range latest.Observations {
		if latest.Observations[i].Role == caller.Role {
			if own != nil {
				unknown.Reason = "saved pane evidence invalid"
				return unknown
			}
			own = &latest.Observations[i]
		}
	}
	if own == nil || own.Pane != caller.Owner.Pane || own.Incarnation != caller.Owner.Incarnation {
		unknown.Reason = "saved pane observation conflicts with current pane"
		return unknown
	}
	observedAt := latestAt
	if own.CapturedAt != "" {
		observed, err := time.Parse(time.RFC3339Nano, own.CapturedAt)
		if err != nil || observed.After(now) {
			unknown.Reason = "saved pane evidence invalid"
			return unknown
		}
		observedAt = observed
		unknown.ObservedAt = own.CapturedAt
		age = int64(now.Sub(observedAt).Seconds())
		unknown.AgeSeconds = &age
	}
	if latestAt.Before(now.Add(-activityFreshness)) || observedAt.Before(now.Add(-activityFreshness)) {
		unknown.Reason = "saved pane observation is stale"
		return unknown
	}
	if latest.Reason != "" || own.Reason != "" || own.Fingerprint == "" {
		unknown.Reason = "saved pane observation unavailable"
		return unknown
	}
	digest := sha256.Sum256([]byte(own.Text))
	if own.Fingerprint != "sha256:"+hex.EncodeToString(digest[:]) {
		unknown.Reason = "saved pane evidence invalid"
		return unknown
	}
	unknown.Reason = "saved pane observation cannot prove current work"
	return unknown
}

func exactAnswer(request, answer Message) bool {
	return request.NeedsReply && answer.ReplyTo == request.ID && answer.FillsInputRequestID == request.InputRequestID &&
		answer.From == request.To && answer.To == request.From &&
		answer.FromSession == request.ToSession && answer.ToSession == request.FromSession
}

func messagePeer(msg Message, inbound bool) string {
	if inbound {
		if msg.FromSession != "" {
			return msg.FromSession + ":" + msg.From
		}
		return msg.From
	}
	if msg.ToSession != "" {
		return msg.ToSession + ":" + msg.To
	}
	return msg.To
}

// State derives metadata only from this caller's files. allowed is the route
// layer's authorized local peer set, not permission inferred from other mail.
// No unread body is returned; no pane capture, notification or recovery occurs.
func (m Mail) State(ctx context.Context, rt Runtime, allowed []string) SelfState {
	state := SelfState{
		AsOf: time.Now().UTC().Format(time.RFC3339Nano), NextAction: "inspect state error",
		Activity: ActivityState{Status: "unknown", Reason: "mail state unavailable"},
	}
	err := m.Store.withView(ctx, rt, func(ws *os.Root, s Snapshot) error {
		state.Workspace, state.Node = s.Root, s.Caller.Role
		name, _ := Name(s.Root)
		state.Mailbox = filepath.Join(m.Store.StateRoot, name, "agents", s.Caller.Role)
		// A pending multi-file intent is not reported as a completed zero state.
		journal, err := privateDir(ws, "transactions", false)
		if err == nil {
			entries, listErr := boundedEntries(journal)
			_ = journal.Close()
			if listErr != nil {
				return listErr
			}
			if len(entries) > 0 {
				return errors.New("pending mail transaction requires mutation recovery")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		peers := []string{}
		seen := map[string]bool{}
		for _, peer := range allowed {
			found := false
			for _, b := range s.Bindings {
				if b.Role == peer && peer != s.Caller.Role {
					found = true
				}
			}
			if !found {
				return errors.New("authorized peer lacks a verified local binding")
			}
			if !seen[peer] {
				peers = append(peers, peer)
				seen[peer] = true
			}
		}
		sort.Strings(peers)
		inbox, err := readOwnMail(ws, s.Caller.Role, "inbox")
		if err != nil {
			return err
		}
		read, err := readOwnMail(ws, s.Caller.Role, "read")
		if err != nil {
			return err
		}
		sent, err := readOwnMail(ws, s.Caller.Role, "sent")
		if err != nil {
			return err
		}
		incoming := append(append([]Message{}, inbox...), read...)
		ids := map[string]bool{}
		for _, msg := range incoming {
			if ids[msg.ID] {
				return errors.New("delivery exists in both unread and claimed state")
			}
			ids[msg.ID] = true
		}
		inbound, outbound := []Obligation{}, []Obligation{}
		for i, req := range incoming {
			if !req.NeedsReply {
				continue
			}
			answered := false
			for _, answer := range sent {
				if exactAnswer(req, answer) {
					answered = true
				}
			}
			if !answered {
				inbound = append(inbound, Obligation{req.ID, req.InputRequestID, messagePeer(req, true), i >= len(inbox)})
			}
		}
		for _, req := range sent {
			if !req.NeedsReply {
				continue
			}
			answered := false
			for _, answer := range incoming {
				if exactAnswer(req, answer) {
					answered = true
				}
			}
			if !answered {
				outbound = append(outbound, Obligation{req.ID, req.InputRequestID, messagePeer(req, false), true})
			}
		}
		sort.Slice(inbound, func(i, j int) bool { return inbound[i].MessageID < inbound[j].MessageID })
		sort.Slice(outbound, func(i, j int) bool { return outbound[i].MessageID < outbound[j].MessageID })
		n := len(inbox)
		state.Known, state.Unread, state.Inbound, state.Outbound, state.Peers = true, &n, inbound, outbound, peers
		now := time.Now().UTC()
		state.AsOf = now.Format(time.RFC3339Nano)
		state.Activity = readOwnActivity(ws, s.Caller, now)
		state.NextAction = "none"
		switch {
		case n > 0:
			state.NextAction = "pop"
		case len(inbound) > 0:
			state.NextAction = "reply"
		case len(outbound) > 0:
			state.NextAction = "wait for reply"
		}
		return nil
	})
	if err != nil {
		state.Known = false
		state.Unread = nil
		state.Inbound = nil
		state.Outbound = nil
		state.Peers = nil
		state.UnavailableReason = err.Error()
		state.NextAction = "inspect state error"
	}
	return state
}

// Supplemental diagnostics never turn a committed operation into a failed send
// or resurrect a claim. CLI adapters must render Self even for an empty POP.
func (m Mail) SendWithState(ctx context.Context, rt Runtime, to, body, replyTo, fills string, needsReply bool, allowed []string) (SendResult, error) {
	msg, err := m.SendWithReply(ctx, rt, to, body, replyTo, fills, needsReply)
	return SendResult{msg, m.State(ctx, rt, allowed)}, err
}

func (m Mail) PopWithState(ctx context.Context, rt Runtime, selected string, allowed []string) (PopResult, error) {
	claim, err := m.Claim(ctx, rt, selected)
	return PopResult{claim, m.State(ctx, rt, allowed)}, err
}
