package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
)

// NodeStatus is the common get-status row and post-operation self shape. Null
// counts mean unknown. Missing capture timestamps do not invalidate mail facts.
type NodeStatus struct {
	Node               string  `json:"node"`
	UnreadCount        *int    `json:"unread_count"`
	InboundCount       *int    `json:"inbound_count"`
	OutboundCount      *int    `json:"outbound_count"`
	DraftCount         *int    `json:"draft_count"`
	FailedCount        *int    `json:"failed_count"`
	LastPopAt          *string `json:"last_pop_at"`
	LastCaptureAt      *string `json:"last_capture_at"`
	LastScreenChangeAt *string `json:"last_screen_change_at"`
	Reason             string  `json:"reason,omitempty"`
}

func countPointer(n int) *int { return &n }

func readSummaryBox(ctx context.Context, ws *os.Root, role, box string) ([]Message, error) {
	return readBoxEntries(ctx, ws, role, box, boundedEntries, nil)
}

// Status may need to count a mailbox larger than the transport's bounded
// operation scan. Check its deadline between directory batches.
func statusEntries(ctx context.Context, root *os.Root) ([]os.DirEntry, error) {
	f, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var entries []os.DirEntry
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := f.ReadDir(128)
		entries = append(entries, batch...)
		if errors.Is(err, io.EOF) {
			return entries, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

func readBoxEntries(ctx context.Context, ws *os.Root, role, box string, entriesFor func(*os.Root) ([]os.DirEntry, error), onReceipt func(ClaimReceipt) error) ([]Message, error) {
	root, err := openOwnBox(ws, role, box)
	if errors.Is(err, os.ErrNotExist) {
		return []Message{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	entries, err := entriesFor(root)
	if err != nil {
		return nil, err
	}
	diagnostic.From(ctx).Add("summary_files_scanned", int64(len(entries)), 0)
	var messages []Message
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".unknown.json") && box == "draft" {
			id := strings.TrimSuffix(name, ".unknown.json")
			var evidence failureRecord
			if !messageID(id) || readJSON(root, name, &evidence) != nil || evidence.Version != 1 || evidence.MessageID != id || evidence.Reason == "" {
				return nil, errors.New("invalid uncertain delivery evidence")
			}
			if _, err := readMessage(root, id+".md"); err != nil {
				return nil, errors.New("uncertain delivery lacks its complete draft")
			}
			continue
		}
		if strings.HasSuffix(name, ".pop.json") && box == "read" {
			id := strings.TrimSuffix(name, ".pop.json")
			var receipt ClaimReceipt
			if !messageID(id) || readJSON(root, name, &receipt) != nil || receipt.Version != 1 || receipt.ID != id || receipt.Role != role {
				return nil, errors.New("invalid POP receipt")
			}
			if _, err := readMessage(root, id+".md"); err != nil {
				return nil, errors.New("POP receipt has no valid archive")
			}
			if onReceipt != nil {
				if err := onReceipt(receipt); err != nil {
					return nil, err
				}
			}
			continue
		}
		if strings.HasSuffix(name, ".failure.json") && box == "failed" {
			id := strings.TrimSuffix(name, ".failure.json")
			var failure failureRecord
			if !messageID(id) || readJSON(root, name, &failure) != nil || failure.Version != 1 || failure.MessageID != id || failure.Reason == "" {
				return nil, errors.New("invalid failure evidence")
			}
			if _, err := readMessage(root, id+".md"); err != nil {
				return nil, errors.New("failure evidence has no valid original mail")
			}
			continue
		}
		id := strings.TrimSuffix(name, ".md")
		if !messageID(id) || name != id+".md" {
			return nil, errors.New("unexpected mailbox entry")
		}
		msg, err := readMessage(root, name)
		if err != nil {
			return nil, err
		}
		outgoing := box == "sent" || box == "draft" || box == "failed"
		if msg.ID != id || (outgoing && msg.From != role) || (!outgoing && msg.To != role) {
			return nil, errors.New("mailbox provenance mismatch")
		}
		if box == "failed" {
			var failure failureRecord
			if readJSON(root, id+".failure.json", &failure) != nil || failure.Version != 1 || failure.MessageID != id || failure.Reason == "" {
				return nil, errors.New("failed mail lacks its failure evidence")
			}
		}
		diagnostic.From(ctx).Add("summary_body_bytes_decoded", int64(len(msg.Body)), 0)
		messages = append(messages, msg)
	}
	return messages, nil
}

// Match by referenced request ID first, then retain exactAnswer's sender,
// session and input-request checks for legacy and combined replies alike.
func indexAnswers(ctx context.Context, answers []Message) (map[string][]*Message, error) {
	byRequest := make(map[string][]*Message, len(answers))
	for i := range answers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		answer := &answers[i]
		if answer.ReplyTo != "" {
			byRequest[answer.ReplyTo] = append(byRequest[answer.ReplyTo], answer)
		}
		for _, ref := range answer.Replies {
			byRequest[ref.MessageID] = append(byRequest[ref.MessageID], answer)
		}
	}
	return byRequest, nil
}

func hasExactAnswer(ctx context.Context, request Message, byRequest map[string][]*Message) (bool, error) {
	for _, answer := range byRequest[request.ID] {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if exactAnswer(request, *answer) {
			return true, nil
		}
	}
	return false, nil
}

func nodeSummary(ctx context.Context, ws *os.Root, role string) NodeStatus {
	node := NodeStatus{Node: role}
	boxes := map[string][]Message{}
	statusScan := func(root *os.Root) ([]os.DirEntry, error) { return statusEntries(ctx, root) }
	var latestPop time.Time
	onReceipt := func(receipt ClaimReceipt) error {
		when, err := time.Parse(time.RFC3339Nano, receipt.ClaimedAt)
		if err != nil || when.After(time.Now().UTC()) {
			return errors.New("invalid POP receipt time")
		}
		if when.After(latestPop) {
			latestPop = when
		}
		return nil
	}
	for _, box := range []string{"inbox", "read", "sent", "draft", "failed"} {
		var receiptCheck func(ClaimReceipt) error
		if box == "read" {
			receiptCheck = onReceipt
		}
		messages, err := readBoxEntries(ctx, ws, role, box, statusScan, receiptCheck)
		if err != nil {
			node.Reason = err.Error()
			continue
		}
		boxes[box] = messages
		switch box {
		case "inbox":
			node.UnreadCount = countPointer(len(messages))
		case "read":
			if !latestPop.IsZero() {
				value := latestPop.UTC().Format(time.RFC3339Nano)
				node.LastPopAt = &value
			}
		case "draft":
			node.DraftCount = countPointer(len(messages))
		case "failed":
			node.FailedCount = countPointer(len(messages))
		}
	}
	if inbox, ok := boxes["inbox"]; ok {
		if read, ok := boxes["read"]; ok {
			if sent, ok := boxes["sent"]; ok {
				incoming := append(append([]Message{}, inbox...), read...)
				seen := map[string]bool{}
				for _, msg := range incoming {
					if err := ctx.Err(); err != nil {
						node.Reason = err.Error()
						return node
					}
					if seen[msg.ID] {
						node.Reason = "delivery exists in inbox and read"
						return node
					}
					seen[msg.ID] = true
				}
				sentAnswers, err := indexAnswers(ctx, sent)
				if err != nil {
					node.Reason = err.Error()
					return node
				}
				incomingAnswers, err := indexAnswers(ctx, incoming)
				if err != nil {
					node.Reason = err.Error()
					return node
				}
				inbound, outbound := 0, 0
				for _, req := range incoming {
					if err := ctx.Err(); err != nil {
						node.Reason = err.Error()
						return node
					}
					if req.NeedsReply {
						answered, err := hasExactAnswer(ctx, req, sentAnswers)
						if err != nil {
							node.Reason = err.Error()
							return node
						}
						if !answered {
							inbound++
						}
					}
				}
				for _, req := range sent {
					if err := ctx.Err(); err != nil {
						node.Reason = err.Error()
						return node
					}
					if req.NeedsReply {
						answered, err := hasExactAnswer(ctx, req, incomingAnswers)
						if err != nil {
							node.Reason = err.Error()
							return node
						}
						if !answered {
							outbound++
						}
					}
				}
				node.InboundCount, node.OutboundCount = countPointer(inbound), countPointer(outbound)
			}
		}
	}
	return node
}

// Summaries reads one verified workspace snapshot. It never creates storage,
// claims mail, recovers interrupted operations, captures panes or refreshes peers.
func (m Mail) Summaries(ctx context.Context, rt Runtime, roles []string) (string, string, []NodeStatus, error) {
	nodes := make([]NodeStatus, 0, len(roles))
	root := ""
	err := retryNoticeAdmission(ctx, func() error {
		return m.Store.withView(ctx, rt, func(ws *os.Root, s Snapshot) error {
			nodes = nodes[:0]
			root = s.Root
			for _, role := range roles {
				if !binding.ValidateNodeName(role) {
					return errors.New("invalid status node")
				}
				node := nodeSummary(ctx, ws, role)
				node.LastCaptureAt, node.LastScreenChangeAt = m.readCachedPaneTimes(s, role)
				nodes = append(nodes, node)
			}
			return nil
		})
	})
	if err != nil {
		nodes = nodes[:0]
		for _, role := range roles {
			nodes = append(nodes, NodeStatus{Node: role, Reason: err.Error()})
		}
	}
	return root, time.Now().UTC().Format(time.RFC3339Nano), nodes, err
}

func (m Mail) Summary(ctx context.Context, rt Runtime, role string) (string, string, NodeStatus) {
	root, at, nodes, _ := m.Summaries(ctx, rt, []string{role})
	return root, at, nodes[0]
}
