package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ExternalBinding is provisioned by local configuration, never by message text.
// Its persistent identity deliberately excludes replaceable tmux pane IDs.
type ExternalBinding struct {
	Root      string `json:"root"`
	Workspace string `json:"workspace"`
	Session   string `json:"session"`
	Node      string `json:"node"`
}

type ExternalMessage struct {
	Binding    string `json:"binding"`
	Filename   string `json:"filename"`
	ExchangeID string `json:"exchange_id"`
	Direction  string `json:"direction"`
	State      string `json:"state,omitempty"`
	Scope      string `json:"scope,omitempty"`
	DecisionID string `json:"decision_id,omitempty"`
	Choice     string `json:"choice,omitempty"`
}

type ExternalRuntime interface {
	Runtime
	Name() string
}

func externalHash(s string) string { sum := sha256.Sum256([]byte(s)); return fmt.Sprintf("%x", sum) }

func (b ExternalBinding) key() string {
	return externalHash(b.Root + "\x00" + b.Workspace + "\x00" + b.Session + "\x00" + b.Node)
}
func externalRole(key string) string { return "external-" + key[:16] }
func hashString(s string) bool {
	raw, err := hex.DecodeString(s)
	return err == nil && len(raw) == 32 && strings.ToLower(s) == s
}

func validateExternalMessage(m Message) error {
	x := m.External
	if x == nil {
		return nil
	}
	if !hashString(x.Binding) || !externalFilename(x.Filename) || !messageID(x.ExchangeID) || m.FromSession != "" || m.ToSession != "" {
		return errors.New("invalid external message correlation")
	}
	role := externalRole(x.Binding)
	switch x.Direction {
	case "in":
		if m.From != role || m.To == role || !m.NeedsReply || x.State != "" {
			return ErrUnknown
		}
		if x.DecisionID != "" {
			if !messageID(x.DecisionID) || !hashString(x.Scope) || (x.Choice != "approve" && x.Choice != "decline") {
				return ErrUnknown
			}
		} else if x.Scope != "" || x.Choice != "" || x.ExchangeID != m.ID {
			return ErrUnknown
		}
	case "out":
		if m.To != role || m.From == role || m.NeedsReply || x.DecisionID != "" || x.Choice != "" {
			return ErrUnknown
		}
		if x.State == "pending" {
			if !hashString(x.Scope) {
				return ErrUnknown
			}
		} else if (x.State != "done" && x.State != "blocked") || x.Scope != "" {
			return ErrUnknown
		}
	default:
		return ErrUnknown
	}
	return nil
}

func noticeSource(s Snapshot, m Message) bool {
	if m.External != nil && m.External.Direction == "in" {
		return validateExternalMessage(m) == nil && s.Caller.Role == m.To
	}
	return s.Caller.Role == m.From
}

func (b ExternalBinding) verify(rt ExternalRuntime, s Snapshot) error {
	if b.Root == "" || b.Workspace != s.Root || b.Node != s.Caller.Role || b.Session != rt.Name() {
		return errors.New("external binding does not match the current interface runtime")
	}
	if _, err := Name(b.Workspace); err != nil {
		return err
	}
	for _, live := range s.Bindings {
		if live.Role == externalRole(b.key()) {
			return errors.New("external transport identity collides with a native role")
		}
	}
	return nil
}

type ExternalIntake struct {
	Filename     string        `json:"filename"`
	MessageID    string        `json:"message_id"`
	Notification *NoticeResult `json:"notification,omitempty"`
	message      Message
}
type ExternalPending struct {
	ExchangeID      string `json:"exchange_id"`
	DecisionID      string `json:"decision_id"`
	Scope           string `json:"scope"`
	ApproveFilename string `json:"approve_filename,omitempty"`
	DeclineFilename string `json:"decline_filename,omitempty"`
	AnswerMessageID string `json:"answer_message_id,omitempty"`
}
type ExternalSync struct {
	Accepted  []ExternalIntake  `json:"accepted"`
	Published []string          `json:"published"`
	Pending   []ExternalPending `json:"pending"`
	More      bool              `json:"more"`
}

func externalReports(ctx context.Context, ws *os.Root, b ExternalBinding) ([]Message, error) {
	all, err := readSummaryBox(ctx, ws, b.Node, "sent")
	if err != nil {
		return nil, err
	}
	var reports []Message
	for _, m := range all {
		if m.External != nil && m.External.Binding == b.key() && m.External.Direction == "out" {
			reports = append(reports, m)
		}
	}
	sort.Slice(reports, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, reports[i].CreatedAt)
		c, _ := time.Parse(time.RFC3339Nano, reports[j].CreatedAt)
		if a.Equal(c) {
			return reports[i].ID < reports[j].ID
		}
		return a.Before(c)
	})
	return reports, nil
}

func latestExternal(reports []Message, exchange string) *Message {
	var latest *Message
	for i := range reports {
		if reports[i].External.ExchangeID == exchange {
			latest = &reports[i]
		}
	}
	return latest
}

func decisionFilename(id, scope, choice string) string {
	return "answer--" + id + "--" + scope + "--" + choice + ".md"
}

func externalInput(b ExternalBinding, name, body string, reports []Message) (Message, error) {
	id := "m-" + externalHash(b.key() + "\x00" + name)[:32]
	x := &ExternalMessage{Binding: b.key(), Filename: name, ExchangeID: id, Direction: "in"}
	if strings.HasPrefix(name, "answer--") {
		parts := strings.Split(strings.TrimSuffix(name, ".md"), "--")
		if len(parts) != 4 || !messageID(parts[1]) || !hashString(parts[2]) || (parts[3] != "approve" && parts[3] != "decline") {
			return Message{}, errors.New("decision filename must identify one exact decision, scope and choice")
		}
		var decision *Message
		for i := range reports {
			if reports[i].ID == parts[1] {
				decision = &reports[i]
			}
		}
		if decision == nil || decision.External.State != "pending" || decision.External.Scope != parts[2] {
			return Message{}, errors.New("decision is unknown or scope changed")
		}
		latest := latestExternal(reports, decision.External.ExchangeID)
		if latest == nil || latest.ID != decision.ID {
			return Message{}, errors.New("decision is superseded or already terminal")
		}
		x.ExchangeID, x.DecisionID, x.Scope, x.Choice = decision.External.ExchangeID, decision.ID, parts[2], parts[3]
	}
	return Message{ID: id, From: externalRole(b.key()), To: b.Node, NeedsReply: true, InputRequestID: "ireq_" + id[2:], CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Body: body, GuidanceVersion: 1, External: x}, nil
}

// SyncExternal performs a bounded scan. A producer invokes it after atomic file
// publication and again on startup/reconnect. No resident watcher is implied.
func (m Mail) SyncExternal(ctx context.Context, rt ExternalRuntime, b ExternalBinding) (ExternalSync, error) {
	out := ExternalSync{Accepted: []ExternalIntake{}, Published: []string{}, Pending: []ExternalPending{}}
	initial, err := rt.Snapshot(ctx)
	if err != nil {
		return out, err
	}
	if err = b.verify(rt, initial); err != nil {
		return out, err
	}
	if err := m.separateExternalRoot(b); err != nil {
		return out, err
	}
	root, lock, err := openExternalRoot(b)
	if err != nil {
		return out, err
	}
	err = func() error {
		defer func() { _ = lock.Close(); _ = root.Close() }()
		inbox, err := privateDir(root, "inbox", false)
		if err != nil {
			return err
		}
		defer func() { _ = inbox.Close() }()
		read, err := privateDir(root, "read", false)
		if err != nil {
			return err
		}
		defer func() { _ = read.Close() }()
		outbox, err := privateDir(root, "outbox", false)
		if err != nil {
			return err
		}
		defer func() { _ = outbox.Close() }()
		return m.Store.WithVerified(ctx, rt, func(ws *os.Root, s Snapshot) error {
			if err := b.verify(rt, s); err != nil {
				return err
			}
			if err := m.recover(ws); err != nil {
				return err
			}
			reports, err := externalReports(ctx, ws, b)
			if err != nil {
				return err
			}
			received, err := readSummaryBox(ctx, ws, externalRole(b.key()), "sent")
			if err != nil {
				return err
			}
			answered := map[string]string{}
			for _, input := range received {
				if input.External != nil && input.External.Binding == b.key() && input.External.DecisionID != "" {
					answered[input.External.DecisionID] = input.ID
				}
			}
			for _, report := range reports {
				raw := []byte(externalReportText(report))
				if err := ensureExternalText(outbox, report.ID+".md", raw); err != nil {
					return err
				}
				out.Published = append(out.Published, report.ID)
				if latest := latestExternal(reports, report.External.ExchangeID); latest != nil && latest.ID == report.ID && report.External.State == "pending" {
					pending := ExternalPending{ExchangeID: report.External.ExchangeID, DecisionID: report.ID, Scope: report.External.Scope, AnswerMessageID: answered[report.ID]}
					if pending.AnswerMessageID == "" {
						pending.ApproveFilename = decisionFilename(report.ID, report.External.Scope, "approve")
						pending.DeclineFilename = decisionFilename(report.ID, report.External.Scope, "decline")
					}
					out.Pending = append(out.Pending, pending)
				}
			}
			// Read archives also recover the accepted-but-not-notified crash gap.
			seenAttention := map[string]bool{}
			for _, box := range []*os.Root{read, inbox} {
				entries, err := boundedEntries(box)
				if err != nil {
					return err
				}
				for _, entry := range entries {
					if err := ctx.Err(); err != nil {
						return err
					}
					name := entry.Name()
					if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".md") {
						continue
					}
					if !externalFilename(name) {
						return errors.New("invalid external filename")
					}
					if len(out.Accepted) >= 16 {
						out.More = true
						return nil
					}
					raw, err := externalText(box, name)
					if err != nil {
						return err
					}
					if len(raw) > 32*1024 {
						return errors.New("external input exceeds 32768 bytes")
					}
					msg, err := m.acceptExternal(ctx, ws, s, b, name, string(raw), reports, box == inbox)
					if err != nil {
						return err
					}
					if msg.External.DecisionID != "" {
						for i := range out.Pending {
							if out.Pending[i].DecisionID == msg.External.DecisionID {
								out.Pending[i].AnswerMessageID = msg.ID
								out.Pending[i].ApproveFilename = ""
								out.Pending[i].DeclineFilename = ""
							}
						}
					}
					if box == inbox {
						if err := m.step("external-accepted"); err != nil {
							return err
						}
						if err := ensureExternalText(read, name, raw); err != nil {
							return err
						}
						if err := m.step("external-archived"); err != nil {
							return err
						}
						current, err := externalText(inbox, name)
						if err != nil || string(current) != string(raw) {
							return errors.New("external published input changed before archival; preserve and reconcile the conflicting file")
						}
						if err := inbox.Remove(name); err != nil {
							return err
						}
						if err := syncDirectory(inbox); err != nil {
							return err
						}
					}
					// A claimed message never needs another input notification.
					claimed, err := openOwnBox(ws, b.Node, "read")
					if err == nil {
						_, checkErr := claimed.Lstat(msg.ID + ".md")
						_ = claimed.Close()
						if checkErr == nil {
							if _, err := claimedReplySources(ws, b.Node, []string{msg.ID}); err != nil {
								return err
							}
							continue
						}
						if !errors.Is(checkErr, os.ErrNotExist) {
							return checkErr
						}
					} else if !errors.Is(err, os.ErrNotExist) {
						return err
					}
					if !seenAttention[msg.ID] {
						out.Accepted = append(out.Accepted, ExternalIntake{Filename: name, MessageID: msg.ID, message: msg})
						seenAttention[msg.ID] = true
					}
				}
			}
			return nil
		})
	}()
	if err != nil {
		return out, err
	}
	// The same per-recipient input lock and no-replay receipts as agent mail.
	for i := range out.Accepted {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		notice := m.Store.NotifyCommittedAfterCommit(ctx, rt, rt, out.Accepted[i].message)
		out.Accepted[i].Notification = &notice
	}
	return out, nil
}

func (m Mail) acceptExternal(ctx context.Context, ws *os.Root, s Snapshot, b ExternalBinding, name, body string, reports []Message, published bool) (Message, error) {
	id := "m-" + externalHash(b.key() + "\x00" + name)[:32]
	sent, err := mailBox(ws, externalRole(b.key()), "sent")
	if err != nil {
		return Message{}, err
	}
	defer func() { _ = sent.Close() }()
	prior, err := readMessage(sent, id+".md")
	if err == nil {
		if prior.External == nil || prior.External.Binding != b.key() || prior.External.Filename != name || prior.Body != body {
			return Message{}, errors.New("external identity reused with changed payload")
		}
		return prior, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return Message{}, err
	}
	if !published {
		return Message{}, errors.New("external read archive has no durable accepted mail; restore mail state rather than replay it")
	}
	msg, err := externalInput(b, name, body, reports)
	if err != nil {
		return Message{}, err
	}
	if msg.External.DecisionID != "" {
		previous, err := readSummaryBox(ctx, ws, msg.From, "sent")
		if err != nil {
			return Message{}, err
		}
		for _, item := range previous {
			if item.External != nil && item.External.DecisionID == msg.External.DecisionID {
				return Message{}, errors.New("decision already answered; conflicting or repeated choice has no effect")
			}
		}
	}
	if err := m.addMessageContext(ctx, s, &msg); err != nil {
		return Message{}, err
	}
	if err := validateMessage(msg); err != nil {
		return Message{}, err
	}
	msg, err = resumeExternalDraft(ws, msg)
	if err != nil {
		return Message{}, err
	}
	return msg, m.begin(ws, mailTransaction{Version: 1, Kind: "send", Message: msg, Body: body})
}

// A deterministic transport ID must preserve the original timestamp/context if
// the process stopped after staging but before writing its mail intent.
func resumeExternalDraft(ws *os.Root, candidate Message) (Message, error) {
	draft, err := openOwnBox(ws, candidate.From, "draft")
	if errors.Is(err, os.ErrNotExist) {
		return candidate, nil
	}
	if err != nil {
		return Message{}, err
	}
	defer func() { _ = draft.Close() }()
	prior, err := readMessage(draft, candidate.ID+".md")
	if errors.Is(err, os.ErrNotExist) {
		return candidate, nil
	}
	if err != nil {
		return Message{}, err
	}
	candidate.CreatedAt, candidate.Context = prior.CreatedAt, prior.Context
	if !sameMessage(candidate, prior) {
		return Message{}, errors.New("external staged identity conflicts with the retried action")
	}
	return prior, nil
}

func externalReportText(msg Message) string {
	x := msg.External
	text := fmt.Sprintf("# %s\n\nExchange: %s\nReport: %s\nState: %s\n\n%s", x.State, x.ExchangeID, msg.ID, x.State, msg.Body)
	if x.State == "pending" {
		text += fmt.Sprintf("\n\nScope SHA256: %s\nPublish your reason as plain text in exactly one of these inbox files:\n- %s\n- %s\n", x.Scope, decisionFilename(msg.ID, x.Scope, "approve"), decisionFilename(msg.ID, x.Scope, "decline"))
	}
	return text
}

// ReportExternal requires a POP receipt and the active configured binding.
// It records an ordinary durable outgoing mail before publishing its projection.
func (m Mail) ReportExternal(ctx context.Context, rt ExternalRuntime, b ExternalBinding, to, state, scope, body string) (Message, error) {
	var result Message
	initial, err := rt.Snapshot(ctx)
	if err != nil {
		return result, err
	}
	if err = b.verify(rt, initial); err != nil {
		return result, err
	}
	if err := m.separateExternalRoot(b); err != nil {
		return result, err
	}
	root, lock, err := openExternalRoot(b)
	if err != nil {
		return result, err
	}
	defer func() { _ = lock.Close(); _ = root.Close() }()
	err = m.Store.WithVerified(ctx, rt, func(ws *os.Root, s Snapshot) error {
		if err := b.verify(rt, s); err != nil {
			return err
		}
		if err := m.recover(ws); err != nil {
			return err
		}
		sources, err := claimedReplySources(ws, b.Node, []string{to})
		if err != nil {
			return err
		}
		source := sources[0]
		if source.External == nil || source.External.Direction != "in" || source.External.Binding != b.key() {
			return errors.New("report source is not this external binding's claimed mail")
		}
		x := &ExternalMessage{Binding: b.key(), Direction: "out", ExchangeID: source.External.ExchangeID, State: state, Scope: scope, Filename: source.External.Filename}
		id := "m-" + externalHash(b.key() + "\x00" + to + "\x00" + state + "\x00" + scope + "\x00" + body)[:32]
		result = Message{ID: id, From: b.Node, To: source.From, External: x, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Body: body}
		if state != "pending" {
			result.Replies = replyReferences(sources)
			if source.ID != x.ExchangeID {
				originals, err := claimedReplySources(ws, b.Node, []string{x.ExchangeID})
				if err != nil {
					return err
				}
				result.Replies = append(result.Replies, replyReferences(originals)...)
			}
		}
		if err := validateMessage(result); err != nil {
			return err
		}
		reports, err := externalReports(ctx, ws, b)
		if err != nil {
			return err
		}
		for _, previous := range reports {
			if previous.ID == id {
				result = previous
				return nil
			}
		}
		if source.External.DecisionID != "" {
			latest := latestExternal(reports, x.ExchangeID)
			if latest == nil || latest.ID != source.External.DecisionID {
				return errors.New("answer refers to a superseded decision; no result may be applied to the newer action")
			}
		}
		if latest := latestExternal(reports, x.ExchangeID); latest != nil && (latest.External.State == "done" || latest.External.State == "blocked") {
			return errors.New("external exchange is already terminal")
		}
		result, err = resumeExternalDraft(ws, result)
		if err != nil {
			return err
		}
		return m.begin(ws, mailTransaction{Version: 1, Kind: "send", Message: result, Body: body})
	})
	if err != nil {
		return result, err
	}
	if err := m.step("external-report-committed"); err != nil {
		return result, err
	}
	outbox, err := privateDir(root, "outbox", false)
	if err != nil {
		return result, err
	}
	defer func() { _ = outbox.Close() }()
	return result, ensureExternalText(outbox, result.ID+".md", []byte(externalReportText(result)))
}

func (m Mail) separateExternalRoot(b ExternalBinding) error {
	for _, pair := range [][2]string{{m.Store.StateRoot, b.Root}, {b.Root, m.Store.StateRoot}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err != nil {
			return err
		}
		if rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("external mailbox and internal Postman state roots must be separate")
		}
	}
	return nil
}
