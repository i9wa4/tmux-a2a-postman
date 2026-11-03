package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func decodeExecutionRequest(msg Message) (ApprovalRequest, error) {
	var req ApprovalRequest
	decoder := json.NewDecoder(strings.NewReader(msg.Body))
	decoder.DisallowUnknownFields()
	if msg.Kind != "execution-request" || !msg.NeedsReply || decoder.Decode(&req) != nil {
		return req, errors.New("claimed message is not an execution request")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || req.Version != 2 || !messageID(req.OperationID) || strings.TrimSpace(req.Command) == "" || strings.TrimSpace(req.Reason) == "" || !filepath.IsAbs(req.CWD) || !filepath.IsAbs(req.Shell) {
		return req, errors.New("invalid execution request")
	}
	return req, nil
}

// DecideExecution has no user-supplied decision literal or command copy. It
// derives immutable correlation from one archive and reserves the choice before
// publication. A crash leaves a reviewable reservation, never a second decision.
func (m Mail) DecideExecution(ctx context.Context, rt Runtime, id, reason string, approve bool) (Message, error) {
	if strings.TrimSpace(reason) == "" || len(reason) > 4096 {
		return Message{}, errors.New("execution decision requires a reason of 1 to 4096 bytes")
	}
	sources, err := m.ReplySources(ctx, rt, []string{id})
	if err != nil {
		return Message{}, err
	}
	if _, err = decodeExecutionRequest(sources[0]); err != nil {
		return Message{}, err
	}
	var result Message
	err = m.Store.WithVerified(ctx, rt, func(ws *os.Root, s Snapshot) error {
		if err := m.recover(ws); err != nil {
			return err
		}
		sources, err := claimedReplySources(ws, s.Caller.Role, []string{id})
		if err != nil {
			return err
		}
		request, err := decodeExecutionRequest(sources[0])
		if err != nil {
			return err
		}
		original := sources[0]
		if original.FromSession != "" || original.ToSession != "" {
			return errors.New("execution approval is local to the requesting workspace")
		}
		bound := false
		for _, b := range s.Bindings {
			if b.Role == original.From {
				bound = true
			}
		}
		if !bound {
			return errors.New("execution requester has no verified binding")
		}
		box, err := executionBox(ws, s.Caller.Role)
		if err != nil {
			return err
		}
		defer func() { _ = box.Close() }()
		decisions, err := privateDir(box, "decisions", true)
		if err != nil {
			return err
		}
		defer func() { _ = decisions.Close() }()
		if _, err := decisions.Lstat(id + ".json"); err == nil {
			return errors.New("execution request already has a reserved decision")
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		decision := "rejected"
		if approve {
			decision = "approved"
		}
		payload := ApprovalDecision{Version: 2, Command: request.Command, OperationID: request.OperationID, Decision: decision, Reason: reason}
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		identity, err := newAvailableMailIdentity(ws)
		if err != nil {
			return err
		}
		result = Message{Kind: "execution-decision", ID: identity.ID, From: s.Caller.Role, To: original.From, Replies: replyReferences(sources), CreatedAt: identity.CreatedAt, Body: string(raw), GuidanceVersion: 1}
		if err := m.addMessageContext(ctx, s, &result); err != nil {
			return err
		}
		if err := validateMessage(result); err != nil {
			return err
		}
		if err := writeJSON(decisions, id+".json", struct {
			MessageID string           `json:"message_id"`
			Decision  ApprovalDecision `json:"decision"`
		}{result.ID, payload}); err != nil {
			return err
		}
		if err := syncDirectory(decisions); err != nil {
			return err
		}
		return m.begin(ws, mailTransaction{Version: 1, Kind: "send", Message: result, Body: result.Body})
	})
	return result, err
}

type executionWaitRecord struct {
	Version   int     `json:"version"`
	Request   Message `json:"request"`
	Requester Binding `json:"requester"`
}

func waiterDir(ws *os.Root, role string, create bool) (*os.Root, error) {
	var box *os.Root
	var err error
	if create {
		box, err = executionBox(ws, role)
	} else {
		agents, e := privateDir(ws, "agents", false)
		if e != nil {
			return nil, e
		}
		defer func() { _ = agents.Close() }()
		agent, e := privateDir(agents, role, false)
		if e != nil {
			return nil, e
		}
		defer func() { _ = agent.Close() }()
		box, err = privateDir(agent, "executions", false)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = box.Close() }()
	return privateDir(box, "waiters", create)
}

func (m Mail) executionWaiter(ctx context.Context, rt Runtime, request Message) (*os.File, error) {
	var held *os.File
	err := retryNoticeAdmission(ctx, func() error {
		return m.Store.WithVerified(ctx, rt, func(ws *os.Root, s Snapshot) error {
			if request.From != s.Caller.Role {
				return ErrUnknown
			}
			dir, err := waiterDir(ws, s.Caller.Role, true)
			if err != nil {
				return err
			}
			defer func() { _ = dir.Close() }()
			// Body is already in the immutable sent archive; avoid storing it twice.
			if err := writeJSON(dir, request.ID+".json", executionWaitRecord{1, request, s.Caller}); err != nil {
				return err
			}
			f, err := dir.OpenFile(request.ID+".json", os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				_ = f.Close()
				return err
			}
			if err := syncDirectory(dir); err != nil {
				_ = f.Close()
				return err
			}
			held = f
			return nil
		})
	})
	return held, err
}

// WaitingExecutionReply is read only. A matching record alone is insufficient:
// its private inode must still be locked by the active foreground waiter.
func (m Mail) WaitingExecutionReply(ctx context.Context, rt Runtime, reply Message) bool {
	if reply.Kind != "execution-decision" || len(reply.Replies) != 1 {
		return false
	}
	waiting := false
	_ = retryNoticeAdmission(ctx, func() error {
		return m.Store.withView(ctx, rt, func(ws *os.Root, s Snapshot) error {
			// The waiter can finish between publication and this check. A durable
			// exact receipt is stronger evidence that no extra TUI hint is needed.
			if read, err := openOwnBox(ws, reply.To, "read"); err == nil {
				defer func() { _ = read.Close() }()
				var receipt ClaimReceipt
				archived, err := readMessage(read, reply.ID+".md")
				if err == nil && sameMessage(archived, reply) && readJSON(read, reply.ID+".pop.json", &receipt) == nil && receipt.Version == 1 && receipt.ID == reply.ID && receipt.Role == reply.To {
					waiting = true
					return nil
				}
			}
			dir, err := waiterDir(ws, reply.To, false)
			if err != nil {
				return err
			}
			defer func() { _ = dir.Close() }()
			id := reply.Replies[0].MessageID
			var record executionWaitRecord
			if readJSON(dir, id+".json", &record) != nil || record.Version != 1 || !exactAnswer(record.Request, reply) {
				return ErrUnknown
			}
			found := false
			for _, b := range s.Bindings {
				if b == record.Requester {
					found = true
				}
			}
			if !found {
				return ErrUnknown
			}
			f, err := dir.OpenFile(id+".json", os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if err != nil {
				return err
			}
			defer func() { _ = f.Close() }()
			info, err := f.Stat()
			if err != nil || !privateInfo(info, false) {
				return ErrUnknown
			}
			path, err := dir.Lstat(id + ".json")
			if err != nil || !os.SameFile(info, path) {
				return ErrUnknown
			}
			err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			waiting = errors.Is(err, syscall.EWOULDBLOCK)
			return nil
		})
	})
	return waiting
}
