package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
	"github.com/i9wa4/tmux-a2a-postman/internal/messageid"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
)

// Mail performs synchronous local-workspace file operations. Callers enforce
// configured routing before Send; notification and Bash run only after return.
type Mail struct {
	Diagnostics *diagnostic.Recorder
	Store       Store
	Context     MessageContextBuilder
	afterStep   func(string) error
}

type Message struct {
	External            *ExternalMessage `json:"external,omitempty"`
	Context             *ArchiveContext  `json:"context,omitempty"`
	Kind                string           `json:"kind,omitempty"`
	Replies             []ReplyReference `json:"replies,omitempty"`
	GuidanceVersion     int              `json:"guidance_version,omitempty"`
	InputRequestID      string           `json:"input_request_id,omitempty"`
	FillsInputRequestID string           `json:"fills_input_request_id,omitempty"`
	ID                  string           `json:"id"`
	From                string           `json:"from"`
	FromSession         string           `json:"from_session,omitempty"`
	To                  string           `json:"to"`
	ToSession           string           `json:"to_session,omitempty"`
	ReplyTo             string           `json:"reply_to,omitempty"`
	NeedsReply          bool             `json:"needs_reply"`
	CreatedAt           string           `json:"created_at"`
	Body                string           `json:"-"`
}

// ReplyReference closes one exact request in a single combined reply.
type ReplyReference struct {
	MessageID      string `json:"message_id"`
	InputRequestID string `json:"input_request_id,omitempty"`
}

const MaxCombinedReplies = 16

type mailTransaction struct {
	Version         int     `json:"version"`
	Kind            string  `json:"kind"`
	Message         Message `json:"message"`
	Body            string  `json:"body"`
	ClaimedAt       string  `json:"claimed_at,omitempty"`
	SourceRoot      string  `json:"source_root,omitempty"`
	DestinationRoot string  `json:"destination_root,omitempty"`
	FailureReason   string  `json:"failure_reason,omitempty"`
}

type ClaimReceipt struct {
	Version   int    `json:"version"`
	ID        string `json:"id"`
	Role      string `json:"role"`
	ClaimedAt string `json:"claimed_at"`
}

type Claimed struct {
	Message Message
	Path    string
	Receipt ClaimReceipt
}

func messageID(id string) bool {
	return messageid.Valid(id)
}

func validateMessage(msg Message) error {
	if err := validateExternalMessage(msg); err != nil {
		return err
	}
	if err := validateMessageContext(msg); err != nil {
		return err
	}
	if msg.Kind != "" && msg.Kind != "execution-request" && msg.Kind != "execution-decision" {
		return errors.New("invalid message kind")
	}
	if !utf8.ValidString(msg.Body) || (msg.GuidanceVersion != 0 && msg.GuidanceVersion != 1) {
		return errors.New("invalid message text or guidance version")
	}
	if len(msg.Replies) > MaxCombinedReplies || (len(msg.Replies) > 0 && (msg.ReplyTo != "" || msg.FillsInputRequestID != "")) {
		return errors.New("ambiguous reply references")
	}
	seenReplies := map[string]bool{}
	for _, ref := range msg.Replies {
		if !messageID(ref.MessageID) || seenReplies[ref.MessageID] || (ref.InputRequestID != "" && !requestID(ref.InputRequestID)) {
			return errors.New("invalid or duplicate reply reference")
		}
		seenReplies[ref.MessageID] = true
	}
	if !messageID(msg.ID) || !binding.ValidateNodeName(msg.From) || !binding.ValidateNodeName(msg.To) ||
		(msg.ReplyTo != "" && !messageID(msg.ReplyTo)) {
		return errors.New("invalid message identity")
	}
	if (msg.FromSession == "") != (msg.ToSession == "") ||
		(msg.FromSession != "" && (!validNativeSessionName(msg.FromSession) ||
			!validNativeSessionName(msg.ToSession) || msg.FromSession == msg.ToSession)) {
		return errors.New("invalid cross-session message identity")
	}
	if msg.NeedsReply != requestID(msg.InputRequestID) || (!msg.NeedsReply && msg.InputRequestID != "") ||
		(msg.FillsInputRequestID != "" && (!requestID(msg.FillsInputRequestID) || msg.ReplyTo == "")) {
		return errors.New("invalid exact reply correlation")
	}
	created, err := time.Parse(time.RFC3339Nano, msg.CreatedAt)
	if err != nil {
		return err
	}
	if stamp, timestamped := messageid.Timestamp(msg.ID); timestamped && !stamp.Equal(created) {
		return errors.New("message timestamp does not match creation metadata")
	}
	// The durable intent is deliberately bounded by the strict record reader.
	tx := mailTransaction{Version: 1, Kind: "send", Message: msg, Body: msg.Body}
	raw, err := json.Marshal(tx)
	if err != nil || len(raw)+1 > 60*1024 {
		return errors.New("message exceeds durable record limit")
	}
	wire, err := marshalMessage(msg)
	if err != nil || len(wire) > 64*1024 {
		return errors.New("message with generated recipient context exceeds archive limit")
	}
	return nil
}

func encodeMessage(msg Message) ([]byte, error) {
	if err := validateMessage(msg); err != nil {
		return nil, err
	}
	return marshalMessage(msg)
}

func readMessage(root *os.Root, name string) (Message, error) {
	var msg Message
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return msg, err
	}
	defer func(closeFn func() error) { _ = closeFn() }(f.Close)
	info, err := f.Stat()
	if err != nil || !privateInfo(info, false) || info.Size() > 64*1024 {
		return msg, errors.New("unsafe mail file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return msg, errors.New("cannot read bounded mail file")
	}
	return DecodeArchive(data)
}

// DecodeArchive validates one complete legacy or context-bearing Markdown
// archive and returns literal sender text separately from generated context.
func DecodeArchive(data []byte) (Message, error) {
	var msg Message
	if len(data) > 64*1024 {
		return msg, errors.New("mail archive exceeds bound")
	}
	prefix := []byte("<!-- postman ")
	split := bytes.Index(data, []byte(" -->\n\n"))
	if !bytes.HasPrefix(data, prefix) || split < len(prefix) {
		return msg, errors.New("invalid mail header")
	}
	decoder := json.NewDecoder(bytes.NewReader(data[len(prefix):split]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&msg); err != nil {
		return msg, err
	}
	msg.Body = string(data[split+len(" -->\n\n"):])
	if c := msg.Context; c != nil {
		if c.Version != 1 || c.Markdown != "" || c.Bytes <= 0 || c.Bytes > len(msg.Body) {
			return msg, errors.New("invalid generated recipient context boundary")
		}
		c.Markdown, msg.Body = msg.Body[:c.Bytes], msg.Body[c.Bytes:]
	} else if footer := messageGuidance(msg); footer != "" {
		if !strings.HasSuffix(msg.Body, footer) {
			return msg, errors.New("saved message lacks its generated reply guidance")
		}
		msg.Body = strings.TrimSuffix(msg.Body, footer)
	}
	canonical, err := encodeMessage(msg)
	if err != nil || !bytes.Equal(canonical, data) {
		return msg, errors.New("noncanonical mail file")
	}
	return msg, nil
}

func writeMessage(root *os.Root, name string, msg Message) error {
	data, err := encodeMessage(msg)
	if err != nil {
		return err
	}
	temp, err := randomName()
	if err != nil {
		return err
	}
	defer func(removeFn func(string) error, name string) { _ = removeFn(name) }(root.Remove, temp)
	f, err := root.OpenFile(temp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
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

func mailBox(ws *os.Root, role, box string) (*os.Root, error) {
	if !binding.ValidateNodeName(role) {
		return nil, errors.New("invalid agent role")
	}
	agents, err := privateDir(ws, "agents", true)
	if err != nil {
		return nil, err
	}
	defer func(closeFn func() error) { _ = closeFn() }(agents.Close)
	agent, err := privateDir(agents, role, true)
	if err != nil {
		return nil, err
	}
	defer func(closeFn func() error) { _ = closeFn() }(agent.Close)
	for _, dir := range []string{"draft", "inbox", "read", "sent", "failed", "pane"} {
		handle, err := privateDir(agent, dir, true)
		if err != nil {
			return nil, err
		}
		_ = handle.Close()
	}
	if err := syncDirectory(agent); err != nil {
		return nil, err
	}
	if err := syncDirectory(agents); err != nil {
		return nil, err
	}
	if err := syncDirectory(ws); err != nil {
		return nil, err
	}
	return privateDir(agent, box, false)
}

func (m Mail) step(name string) error {
	if m.afterStep != nil {
		if err := m.afterStep(name); err != nil {
			return &mailStepError{err}
		}
	}
	return nil
}

func sameMessage(a, b Message) bool { return reflect.DeepEqual(a, b) }

// Send gives every independent call a fresh operation identity, even for equal
// bodies. Interrupted publication is recovered from its exact immutable intent.
func (m Mail) Send(ctx context.Context, rt Runtime, to, body, replyTo string, needsReply bool) (Message, error) {
	return m.SendWithReply(ctx, rt, to, body, replyTo, "", needsReply)
}

// SendWithReply fills only a request that this caller has already claimed.
func (m Mail) SendWithReply(ctx context.Context, rt Runtime, to, body, replyTo, fills string, needsReply bool) (Message, error) {
	return m.sendWithKind(ctx, rt, to, body, replyTo, fills, needsReply, "")
}

func (m Mail) sendWithKind(ctx context.Context, rt Runtime, to, body, replyTo, fills string, needsReply bool, kind string) (Message, error) {
	var msg Message
	err := m.Store.WithVerified(ctx, rt, func(ws *os.Root, s Snapshot) error {
		bound := false
		for _, b := range s.Bindings {
			if b.Role == to {
				bound = true
			}
		}
		if err := m.recover(ws); err != nil {
			return err
		}
		if replyTo != "" {
			read, err := openOwnBox(ws, s.Caller.Role, "read")
			if err != nil {
				return err
			}
			defer func(closeFn func() error) { _ = closeFn() }(read.Close)
			original, err := readMessage(read, replyTo+".md")
			if err != nil || original.To != s.Caller.Role || original.From != to ||
				original.FromSession != "" || original.ToSession != "" {
				return errors.New("reply does not identify a claimed conversation")
			}
			var receipt ClaimReceipt
			if readJSON(read, replyTo+".pop.json", &receipt) != nil || receipt.Version != 1 || receipt.ID != replyTo || receipt.Role != s.Caller.Role {
				return errors.New("reply source lacks a matching POP receipt")
			}
			if fills != "" && (!original.NeedsReply || original.InputRequestID != fills) {
				return errors.New("reply fills a different request")
			}
		}
		identity, err := newMailIdentity()
		if err != nil {
			return err
		}
		msg = Message{
			Kind: kind,
			ID:   identity.ID, From: s.Caller.Role, To: to,
			ReplyTo: replyTo, NeedsReply: needsReply, CreatedAt: identity.CreatedAt, Body: body,
			GuidanceVersion: 1,
		}
		msg.FillsInputRequestID = fills
		if needsReply {
			msg.InputRequestID = inputRequestID(msg.ID)
		}
		if err := m.addMessageContext(ctx, s, &msg); err != nil {
			return err
		}
		if err := validateMessage(msg); err != nil {
			return err
		}
		tx := mailTransaction{Version: 1, Kind: "send", Message: msg, Body: body}
		if !bound {
			return m.failUnpublished(ws, msg, errors.New("recipient has no verified local binding"))
		}
		return m.begin(ws, tx)
	})
	return msg, err
}

// Recover completes only file intents. It cannot notify or execute a command.
func (m Mail) Recover(ctx context.Context, rt Runtime) error {
	return m.Store.WithVerified(ctx, rt, func(ws *os.Root, _ Snapshot) error { return m.recover(ws) })
}

func (m Mail) begin(ws *os.Root, tx mailTransaction) error {
	if tx.Kind == "send" {
		if err := m.stage(ws, tx.Message); err != nil {
			return err
		}
	}
	journal, err := privateDir(ws, "transactions", true)
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(journal.Close)
	if err := writeJSON(journal, tx.Kind+"-"+tx.Message.ID+".json", tx); err != nil {
		return err
	}
	if err := syncDirectory(journal); err != nil {
		return err
	}
	if err := syncDirectory(ws); err != nil {
		return err
	}
	if err := m.step(tx.Kind + "-intent"); err != nil {
		return err
	}
	return m.finish(ws, journal, tx)
}

func (m Mail) recover(ws *os.Root) error {
	journal, err := privateDir(ws, "transactions", true)
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(journal.Close)
	dir, err := journal.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := dir.ReadDir(1025)
	_ = dir.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return readErr
	}
	if len(entries) > 1024 {
		return errors.New("too many pending mail transactions")
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	// Send intents sort after claim intents. Both are safe in either order:
	// an already-archived delivery is never republished into inbox.
	for _, entry := range entries {
		var tx mailTransaction
		if err := readJSON(journal, entry.Name(), &tx); err != nil {
			return err
		}
		tx.Message.Body = tx.Body
		if tx.Version != 1 || entry.Name() != tx.Kind+"-"+tx.Message.ID+".json" ||
			validateMessage(tx.Message) != nil {
			return errors.New("invalid mail transaction")
		}
		if tx.Kind == "cross_send" {
			if !validCrossIntent(tx) {
				return errors.New("invalid cross-session transaction")
			}
			// A local operation cannot verify the intended peer. Preserve its
			// durable intent for exact-pair recovery without blocking local mail.
			continue
		}
		if tx.Kind != "send" && tx.Kind != "claim" {
			return errors.New("invalid mail transaction kind")
		}
		if tx.Kind == "send" && (tx.Message.FromSession != "" || tx.SourceRoot != "" || tx.DestinationRoot != "") {
			return errors.New("cross-session send requires a verified peer recovery path")
		}
		if tx.Kind == "claim" && (tx.SourceRoot != "" || tx.DestinationRoot != "") {
			return errors.New("claim has unexpected workspace roots")
		}
		if tx.Kind == "claim" {
			if _, err := time.Parse(time.RFC3339Nano, tx.ClaimedAt); err != nil {
				return err
			}
		}
		if err := m.finish(ws, journal, tx); err != nil {
			return err
		}
	}
	return nil
}

func (m Mail) finish(ws, journal *os.Root, tx mailTransaction) error {
	var err error
	switch {
	case tx.FailureReason != "":
		if tx.Kind != "send" {
			return errors.New("invalid terminal mail failure")
		}
		err = saveFailed(ws, tx.Message, tx.FailureReason)
	case tx.Kind == "send":
		err = m.publish(ws, tx.Message)
	default:
		err = m.archive(ws, tx)
	}
	if err != nil {
		var interrupted *mailStepError
		if tx.Kind == "send" && tx.FailureReason == "" && !errors.As(err, &interrupted) && deliveryProvenAbsent(ws, tx.Message) {
			tx.FailureReason = err.Error()
			if saveErr := replaceJSON(journal, tx.Kind+"-"+tx.Message.ID+".json", tx); saveErr == nil {
				if finishErr := m.finish(ws, journal, tx); finishErr == nil {
					return &DeliveryFailure{Cause: err, MessageID: tx.Message.ID}
				} else {
					return errors.Join(err, finishErr)
				}
			}
		}
		return err
	}
	if tx.Kind == "send" {
		if err := clearDraft(ws, tx.Message); err != nil {
			return err
		}
	}
	if err := journal.Remove(tx.Kind + "-" + tx.Message.ID + ".json"); err != nil {
		return err
	}
	return syncDirectory(journal)
}

func ensureMessage(box *os.Root, msg Message) error {
	existing, err := readMessage(box, msg.ID+".md")
	if err == nil {
		if !sameMessage(existing, msg) {
			return errors.New("message identity conflicts with existing content")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeMessage(box, msg.ID+".md", msg)
}

func (m Mail) publish(ws *os.Root, msg Message) (retErr error) {
	publishSpan := m.Diagnostics.Start("mail_publish")
	defer func() { publishSpan.Error(retErr) }()
	if msg.FromSession != "" || msg.ToSession != "" {
		return errors.New("local publication cannot deliver cross-session mail")
	}
	inbox, err := mailBox(ws, msg.To, "inbox")
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(inbox.Close)
	read, err := mailBox(ws, msg.To, "read")
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(read.Close)
	archived, readErr := readMessage(read, msg.ID+".md")
	switch {
	case readErr == nil:
		if !sameMessage(archived, msg) {
			return errors.New("archived message conflicts with send intent")
		}
		if _, err := inbox.Lstat(msg.ID + ".md"); !errors.Is(err, os.ErrNotExist) {
			return errors.New("delivery exists in both inbox and read")
		}
	case errors.Is(readErr, os.ErrNotExist):
		if err := ensureMessage(inbox, msg); err != nil {
			return err
		}
	default:
		return readErr
	}
	if err := m.step("send-inbox"); err != nil {
		return err
	}
	publishSpan.End("committed")
	m.Diagnostics.Note(diagnostic.Facts{MessageID: msg.ID, InputRequestID: msg.InputRequestID, Sender: msg.From, Recipient: msg.To, Mail: "committed"})
	archivedSpan := m.Diagnostics.Start("sender_archive")
	defer func() { archivedSpan.Error(retErr) }()
	sent, err := mailBox(ws, msg.From, "sent")
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(sent.Close)
	if err := ensureMessage(sent, msg); err != nil {
		return err
	}
	return m.step("send-sent")
}

// Claim optionally selects one exact eligible ID; it never consumes another
// role's inbox. A returned absolute path already exists and has a durable receipt.
func (m Mail) Claim(ctx context.Context, rt Runtime, selected string) (*Claimed, error) {
	if selected != "" && !messageID(selected) {
		return nil, errors.New("invalid selected message ID")
	}
	var result *Claimed
	err := m.Store.WithVerified(ctx, rt, func(ws *os.Root, s Snapshot) error {
		if err := m.recover(ws); err != nil {
			return err
		}
		inbox, err := mailBox(ws, s.Caller.Role, "inbox")
		if err != nil {
			return err
		}
		defer func(closeFn func() error) { _ = closeFn() }(inbox.Close)
		dir, err := inbox.Open(".")
		if err != nil {
			return err
		}
		entries, readErr := dir.ReadDir(1025)
		_ = dir.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if len(entries) > 1024 {
			return errors.New("inbox exceeds bounded claim scan")
		}
		names := []string{}
		for _, entry := range entries {
			id := strings.TrimSuffix(entry.Name(), ".md")
			if !messageID(id) || entry.Name() != id+".md" {
				return errors.New("unexpected inbox entry")
			}
			if selected == "" || selected == id {
				names = append(names, entry.Name())
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			return nil
		}
		msg, err := readMessage(inbox, names[0])
		if err != nil {
			return err
		}
		if msg.To != s.Caller.Role || names[0] != msg.ID+".md" {
			return errors.New("mail recipient mismatch")
		}
		tx := mailTransaction{Version: 1, Kind: "claim", Message: msg, Body: msg.Body, ClaimedAt: time.Now().UTC().Format(time.RFC3339Nano)}
		if err := m.begin(ws, tx); err != nil {
			return err
		}
		name, err := Name(s.Root)
		if err != nil {
			return err
		}
		result = &Claimed{
			Message: msg, Path: filepath.Join(m.Store.StateRoot, name, "agents", msg.To, "read", msg.ID+".md"),
			Receipt: ClaimReceipt{1, msg.ID, msg.To, tx.ClaimedAt},
		}
		return nil
	})
	return result, err
}

func (m Mail) archive(ws *os.Root, tx mailTransaction) (retErr error) {
	span := m.Diagnostics.Start("pop_claim")
	defer func() { span.Error(retErr) }()
	msg := tx.Message
	inbox, err := mailBox(ws, msg.To, "inbox")
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(inbox.Close)
	read, err := mailBox(ws, msg.To, "read")
	if err != nil {
		return err
	}
	defer func(closeFn func() error) { _ = closeFn() }(read.Close)
	previous, err := readMessage(read, msg.ID+".md")
	switch {
	case errors.Is(err, os.ErrNotExist):
		current, err := readMessage(inbox, msg.ID+".md")
		if err != nil || !sameMessage(current, msg) {
			return errors.New("claim source absent or conflicting")
		}
		from := filepath.Join("agents", msg.To, "inbox", msg.ID+".md")
		to := filepath.Join("agents", msg.To, "read", msg.ID+".md")
		if err := ws.Rename(from, to); err != nil {
			return err
		}
		if err := syncDirectory(inbox); err != nil {
			return err
		}
		if err := syncDirectory(read); err != nil {
			return err
		}
	case err != nil:
		return err
	case !sameMessage(previous, msg):
		return errors.New("claim archive conflicts")
	}
	if _, err := inbox.Lstat(msg.ID + ".md"); !errors.Is(err, os.ErrNotExist) {
		return errors.New("claim source still exists")
	}
	if err := m.step("claim-archived"); err != nil {
		return err
	}
	receipt := ClaimReceipt{1, msg.ID, msg.To, tx.ClaimedAt}
	name := msg.ID + ".pop.json"
	var prior ClaimReceipt
	err = readJSON(read, name, &prior)
	switch {
	case err == nil:
		if prior != receipt {
			return fmt.Errorf("claim receipt conflicts: %s", msg.ID)
		}
	case errors.Is(err, os.ErrNotExist):
		if err := writeJSON(read, name, receipt); err != nil {
			return err
		}
	default:
		return err
	}
	if err := syncDirectory(read); err != nil {
		return err
	}
	return m.step("claim-receipt")
}
