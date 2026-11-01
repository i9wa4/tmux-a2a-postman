package workspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
)

// messageGuidance is versioned because old archives must remain readable when
// the CLI's current instructions change. User Markdown is never rewritten.
func messageGuidance(msg Message) string {
	if msg.GuidanceVersion != 1 {
		return ""
	}
	start := "\n\n---\n<!-- postman reply guidance v1 -->\n"
	end := "\n<!-- end postman reply guidance v1 -->\n"
	if !msg.NeedsReply {
		return start + "No reply is required for this message." + end
	}
	if msg.Kind == "execution-request" {
		return start + "Review the exact command, reason, cwd and shell above. Choose exactly one action and give your reason:\n\n```sh\ntmux-a2a-postman approve-execution --to " + msg.ID + " --reason '<why this is approved>'\n# or\ntmux-a2a-postman decline-execution --to " + msg.ID + " --reason '<why this is declined>'\n```\nThe requesting CLI waits for this decision and executes the command itself. Do not execute the command in the approver pane." + end
	}
	return start + "A reply is required. Read this complete archived Markdown first, then reply to this message ID:\n\n```sh\ntmux-a2a-postman send-heredoc --to " + msg.ID + " <<'POSTMAN_BODY'\n<your reply>\nPOSTMAN_BODY\n```" + end
}

func claimedReplySources(ws *os.Root, role string, ids []string) ([]Message, error) {
	if len(ids) == 0 || len(ids) > MaxCombinedReplies {
		return nil, errors.New("reply requires 1 to 16 claimed message IDs")
	}
	read, err := openOwnBox(ws, role, "read")
	if err != nil {
		return nil, err
	}
	defer func() { _ = read.Close() }()
	seen := map[string]bool{}
	var sources []Message
	for _, id := range ids {
		if !messageID(id) || seen[id] {
			return nil, errors.New("invalid or duplicate reply message ID")
		}
		seen[id] = true
		msg, err := readMessage(read, id+".md")
		if err != nil || msg.To != role {
			return nil, errors.New("reply source is not this caller's claimed mail")
		}
		var receipt ClaimReceipt
		if readJSON(read, id+".pop.json", &receipt) != nil || receipt.Version != 1 || receipt.ID != id || receipt.Role != role {
			return nil, errors.New("reply source lacks a matching POP receipt")
		}
		if _, err := time.Parse(time.RFC3339Nano, receipt.ClaimedAt); err != nil {
			return nil, errors.New("invalid reply source receipt time")
		}
		if len(sources) > 0 {
			first := sources[0]
			if msg.From != first.From || msg.FromSession != first.FromSession || msg.ToSession != first.ToSession {
				return nil, errors.New("combined replies require the same sender and return route")
			}
		}
		sources = append(sources, msg)
	}
	return sources, nil
}

// ReplySources is read only. SendReply revalidates all inputs under its mutation
// lock; callers use these facts only to authorize the current route.
func (m Mail) ReplySources(ctx context.Context, rt Runtime, ids []string) ([]Message, error) {
	var sources []Message
	err := retryNoticeAdmission(ctx, func() error {
		return m.Store.withView(ctx, rt, func(ws *os.Root, s Snapshot) error {
			var err error
			sources, err = claimedReplySources(ws, s.Caller.Role, ids)
			return err
		})
	})
	return sources, err
}

func replyReferences(sources []Message) []ReplyReference {
	refs := make([]ReplyReference, 0, len(sources))
	for _, source := range sources {
		refs = append(refs, ReplyReference{source.ID, source.InputRequestID})
	}
	return refs
}

// SendReply emits exactly one reply, never a new request. All referenced mail
// must have a durable claim and one identical, currently authorized return route.
func (m Mail) SendReply(ctx context.Context, rt Runtime, ids []string, body string) (Message, error) {
	if _, err := m.ReplySources(ctx, rt, ids); err != nil {
		return Message{}, err
	}
	var msg Message
	err := m.Store.WithVerified(ctx, rt, func(ws *os.Root, s Snapshot) error {
		if err := m.recover(ws); err != nil {
			return err
		}
		sources, err := claimedReplySources(ws, s.Caller.Role, ids)
		if err != nil {
			return err
		}
		first := sources[0]
		if first.FromSession != "" || first.ToSession != "" {
			return errors.New("cross-session reply requires its verified peer")
		}
		bound := false
		for _, b := range s.Bindings {
			if b.Role == first.From {
				bound = true
			}
		}
		nonce, err := randomName()
		if err != nil {
			return err
		}
		msg = Message{ID: "m-" + strings.TrimPrefix(nonce, ".new-"), From: s.Caller.Role, To: first.From, Replies: replyReferences(sources), CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Body: body, GuidanceVersion: 1}
		if err := m.addMessageContext(ctx, s, &msg); err != nil {
			return err
		}
		if err := validateMessage(msg); err != nil {
			return err
		}
		if !bound {
			return m.failUnpublished(ws, msg, errors.New("reply recipient has no verified local binding"))
		}
		return m.begin(ws, mailTransaction{Version: 1, Kind: "send", Message: msg, Body: body})
	})
	return msg, err
}
