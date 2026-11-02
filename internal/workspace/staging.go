package workspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
)

type DeliveryFailure struct {
	Cause     error
	MessageID string
}

// An authorized interface route can be unavailable before a peer is verified.
// Retain the complete candidate only in the verified sender's draft, without
// claiming confirmed non-delivery or granting any permission at the peer.
func (m Mail) StageUnavailableCross(ctx context.Context, source NamedRuntime, toSession, to string, ids []string, body string, cause error) (Message, error) {
	var msg Message
	err := retryNoticeAdmission(ctx, func() error {
		return m.Store.WithVerified(ctx, source, func(ws *os.Root, s Snapshot) error {
			var refs []ReplyReference
			if len(ids) > 0 {
				sources, err := claimedReplySources(ws, s.Caller.Role, ids)
				if err != nil {
					return err
				}
				first := sources[0]
				if first.From != to || first.FromSession != toSession || first.ToSession != source.Name() {
					return ErrUnknown
				}
				refs = replyReferences(sources)
			}
			nonce, err := randomName()
			if err != nil {
				return err
			}
			msg = Message{ID: "m-" + strings.TrimPrefix(nonce, ".new-"), From: s.Caller.Role, FromSession: source.Name(), To: to, ToSession: toSession, Replies: refs, NeedsReply: len(ids) == 0, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Body: body, GuidanceVersion: 1}
			if msg.NeedsReply {
				msg.InputRequestID = "ireq_" + strings.TrimPrefix(msg.ID, "m-")
			}
			if err := m.addMessageContext(ctx, s, &msg); err != nil {
				return err
			}
			if err := validateMessage(msg); err != nil {
				return err
			}
			if err := m.stage(ws, msg); err != nil {
				return err
			}
			dir, err := openOwnBox(ws, s.Caller.Role, "draft")
			if err != nil {
				return err
			}
			defer func() { _ = dir.Close() }()
			if err := writeJSON(dir, msg.ID+".unknown.json", failureRecord{1, msg.ID, cause.Error()}); err != nil {
				return err
			}
			return syncDirectory(dir)
		})
	})
	if err != nil {
		return msg, errors.Join(cause, err)
	}
	return msg, cause
}

func (e *DeliveryFailure) Error() string { return e.Cause.Error() }
func (e *DeliveryFailure) Unwrap() error { return e.Cause }

// An injected interruption is a crash boundary, never evidence of rejection.
type mailStepError struct{ error }

func (e *mailStepError) Unwrap() error { return e.error }

func (m Mail) stage(ws *os.Root, msg Message) (retErr error) {
	span := m.Diagnostics.Start("mail_staging")
	defer func() { span.Error(retErr) }()
	draft, err := mailBox(ws, msg.From, "draft")
	if err != nil {
		return err
	}
	defer func() { _ = draft.Close() }()
	if err := ensureMessage(draft, msg); err != nil {
		return err
	}
	return m.step("send-draft")
}

func clearDraft(ws *os.Root, msg Message) error {
	draft, err := openOwnBox(ws, msg.From, "draft")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = draft.Close() }()
	prior, err := readMessage(draft, msg.ID+".md")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !sameMessage(prior, msg) {
		return errors.New("draft conflicts with committed message")
	}
	if err := draft.Remove(msg.ID + ".md"); err != nil {
		return err
	}
	return syncDirectory(draft)
}

func saveFailed(ws *os.Root, msg Message, reason string) error {
	failed, err := mailBox(ws, msg.From, "failed")
	if err != nil {
		return err
	}
	defer func() { _ = failed.Close() }()
	if err := ensureMessage(failed, msg); err != nil {
		return err
	}
	name := msg.ID + ".failure.json"
	var prior failureRecord
	err = readJSON(failed, name, &prior)
	record := failureRecord{1, msg.ID, reason}
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := writeJSON(failed, name, record); err != nil {
			return err
		}
	case err != nil:
		return err
	case prior != record:
		return errors.New("failure evidence conflicts")
	}
	if err := syncDirectory(failed); err != nil {
		return err
	}
	return clearDraft(ws, msg)
}

type failureRecord struct {
	Version   int    `json:"version"`
	MessageID string `json:"message_id"`
	Reason    string `json:"reason"`
}

func (m Mail) failUnpublished(ws *os.Root, msg Message, cause error) error {
	if err := m.stage(ws, msg); err != nil {
		return errors.Join(cause, err)
	}
	if err := saveFailed(ws, msg, cause.Error()); err != nil {
		return errors.Join(cause, err)
	}
	return &DeliveryFailure{Cause: cause, MessageID: msg.ID}
}

func deliveryProvenAbsent(ws *os.Root, msg Message) bool {
	for _, location := range []struct{ role, box string }{{msg.To, "inbox"}, {msg.To, "read"}, {msg.From, "sent"}} {
		box, err := openOwnBox(ws, location.role, location.box)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false
		}
		_, err = readMessage(box, msg.ID+".md")
		_ = box.Close()
		if !errors.Is(err, os.ErrNotExist) {
			return false
		}
	}
	return true
}
