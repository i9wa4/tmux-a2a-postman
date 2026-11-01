package workspace

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
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

func readSummaryBox(ws *os.Root, role, box string) ([]Message, error) {
	root, err := openOwnBox(ws, role, box)
	if errors.Is(err, os.ErrNotExist) {
		return []Message{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	entries, err := boundedEntries(root)
	if err != nil {
		return nil, err
	}
	var messages []Message
	for _, entry := range entries {
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
		messages = append(messages, msg)
	}
	return messages, nil
}

func nodeSummary(ws *os.Root, role string) NodeStatus {
	node := NodeStatus{Node: role}
	boxes := map[string][]Message{}
	for _, box := range []string{"inbox", "read", "sent", "draft", "failed"} {
		messages, err := readSummaryBox(ws, role, box)
		if err != nil {
			node.Reason = err.Error()
			continue
		}
		boxes[box] = messages
		switch box {
		case "inbox":
			node.UnreadCount = countPointer(len(messages))
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
					if seen[msg.ID] {
						node.Reason = "delivery exists in inbox and read"
						return node
					}
					seen[msg.ID] = true
				}
				inbound, outbound := 0, 0
				for _, req := range incoming {
					if req.NeedsReply {
						answered := false
						for _, answer := range sent {
							if exactAnswer(req, answer) {
								answered = true
								break
							}
						}
						if !answered {
							inbound++
						}
					}
				}
				for _, req := range sent {
					if req.NeedsReply {
						answered := false
						for _, answer := range incoming {
							if exactAnswer(req, answer) {
								answered = true
								break
							}
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
	read, err := openOwnBox(ws, role, "read")
	if err == nil {
		defer func() { _ = read.Close() }()
		entries, err := boundedEntries(read)
		if err == nil {
			var latest time.Time
			for _, entry := range entries {
				if !strings.HasSuffix(entry.Name(), ".pop.json") {
					continue
				}
				id := strings.TrimSuffix(entry.Name(), ".pop.json")
				var receipt ClaimReceipt
				if !messageID(id) || readJSON(read, entry.Name(), &receipt) != nil || receipt.Version != 1 || receipt.ID != id || receipt.Role != role {
					node.Reason = "invalid POP receipt"
					continue
				}
				if _, err := readMessage(read, id+".md"); err != nil {
					node.Reason = "receipt archive unavailable"
					continue
				}
				when, err := time.Parse(time.RFC3339Nano, receipt.ClaimedAt)
				if err != nil || when.After(time.Now().UTC()) {
					node.Reason = "invalid POP receipt time"
					continue
				}
				if when.After(latest) {
					latest = when
				}
			}
			if !latest.IsZero() {
				value := latest.UTC().Format(time.RFC3339Nano)
				node.LastPopAt = &value
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
				node := nodeSummary(ws, role)
				node.LastCaptureAt, node.LastScreenChangeAt = readPaneTimes(ws, s, role)
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
