package cli

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

// discoverNodesForCommandApprovalDeliveryFn is a seam over
// discovery.DiscoverNodesWithCollisions (#626 M2): the real implementation
// shells out to tmux, which is unavailable/nondeterministic in unit tests;
// tests override this var and restore it via t.Cleanup.
var discoverNodesForCommandApprovalDeliveryFn = discovery.DiscoverNodesWithCollisions

// deliverCommandApprovalRequest sends a reply-required postman message to
// the resolved reviewer_node when a command needs approval (#626). This is
// best-effort: a delivery failure is logged, not returned as an error — the
// approval request has already been journaled by the caller regardless, so
// a failed delivery only means the reviewer must be notified some other way
// (e.g. inspect-command-approvals), never that anything blocks or that the
// request goes unrecorded.
//
// The reviewer's reply is matched back to this request by thread_id (the
// same content-level correlation the daemon already uses for the
// orchestrator/critic approval flow), not by the input_request_id set here.
// input_request_id/reply_policy: required are still set so the request
// shows up as a normal open input request in get-status and inspect-message
// for the reviewer, reusing the existing fill-tracking UX; the reviewer's
// reply must preserve the given thread_id in its own frontmatter for the
// decision to be recorded automatically.
func deliverCommandApprovalRequest(cfg *config.Config, baseDir, contextID, requesterSessionName string, policy resolvedCommandApprovalPolicy, reviewerNode, threadID, commandHash, reason string, storeCommandText bool, now time.Time) {
	nodes, _, err := discoverNodesForCommandApprovalDeliveryFn(baseDir, contextID, requesterSessionName)
	if err != nil {
		log.Printf("postman: WARNING: command approval delivery: discovering nodes: %v\n", err)
		return
	}
	reviewerInfo, ok := nodes[reviewerNode]
	if !ok {
		log.Printf("postman: WARNING: command approval delivery: reviewer_node %q not found among discovered nodes; falling back to inspect-command-approvals\n", reviewerNode)
		return
	}
	if err := config.CreateSessionDirs(reviewerInfo.SessionDir); err != nil {
		log.Printf("postman: WARNING: command approval delivery: creating reviewer session directories: %v\n", err)
		return
	}
	inputRequestID, err := generateInputRequestID()
	if err != nil {
		log.Printf("postman: WARNING: command approval delivery: generating input request id: %v\n", err)
		return
	}
	filename, err := message.GenerateFilename(now.Format("20060102-150405"), policy.Requester, reviewerNode, reviewerInfo.SessionName)
	if err != nil {
		log.Printf("postman: WARNING: command approval delivery: generating filename: %v\n", err)
		return
	}

	var body strings.Builder
	fmt.Fprintf(&body, "Command approval requested by %s (mode: %s, label: %s, category: %s).\n\n", policy.Requester, policy.Mode, policy.Label, policy.Category)
	fmt.Fprintf(&body, "Command hash: %s\n", commandHash)
	if strings.TrimSpace(reason) != "" {
		fmt.Fprintf(&body, "Requester-provided reason: %s\n", reason)
	}
	if storeCommandText {
		fmt.Fprintf(&body, "\nThe full command text is stored in this session's durable audit journal (--store-command-text was set); it is not repeated in this message.\n")
	}
	fmt.Fprintf(&body, "\nTo record your decision, reply with a body starting with `APPROVED: <reason>` or `NOT APPROVED: <reason>`, and keep this thread id in your reply's frontmatter:\n\nthread_id: %s\n", threadID)

	content := fmt.Sprintf(
		"---\nparams:\n  contextId: %s\n  from: %s\n  to: %s\n  messageId: %s\n  replyPolicy: required\n  input_request_id: %s\n  thread_id: %s\n  timestamp: %s\n---\n\n%s\n",
		contextID, policy.Requester, reviewerNode, filename, inputRequestID, threadID, now.UTC().Format(time.RFC3339), body.String(),
	)

	// Write to draft/ then rename into post/ (matching send_message.go's
	// atomicity convention, #626 FIX-SOON) rather than writing post/
	// directly — this was the only post/ writer skipping it, and a partial
	// direct write could be picked up mid-write by the daemon's watcher.
	draftDir := filepath.Join(reviewerInfo.SessionDir, "draft")
	if err := os.MkdirAll(draftDir, 0o700); err != nil {
		log.Printf("postman: WARNING: command approval delivery: creating draft directory: %v\n", err)
		return
	}
	draftPath := filepath.Join(draftDir, filename)
	if err := os.WriteFile(draftPath, []byte(content), 0o600); err != nil {
		log.Printf("postman: WARNING: command approval delivery: writing draft: %v\n", err)
		return
	}
	postDir := filepath.Join(reviewerInfo.SessionDir, "post")
	if err := os.MkdirAll(postDir, 0o700); err != nil {
		log.Printf("postman: WARNING: command approval delivery: creating post directory: %v\n", err)
		return
	}
	if err := os.Rename(draftPath, filepath.Join(postDir, filename)); err != nil {
		log.Printf("postman: WARNING: command approval delivery: moving message into post: %v\n", err)
	}
}
