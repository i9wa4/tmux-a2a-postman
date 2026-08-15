package cli

import (
	"fmt"
	"log"
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
var (
	discoverNodesForCommandApprovalDeliveryFn = discovery.DiscoverNodesWithCollisions
	deliverCommandApprovalSystemMessageFn     = message.DeliverSystemMessageDirectResult
)

// deliverCommandApprovalRequest sends a reply-required postman message to
// the resolved command_approver_node when a command needs approval (#626).
// The approval request has already been journaled by the caller regardless;
// returning an error lets blocking mode fail closed when a configured
// approver cannot be reached through live discovery (#680).
//
// The reviewer's reply is matched back to this request by thread_id,
// fills_input_request_id, and command_hash. The input id is minted once by the
// caller before journaling and must be reused unchanged here so projection,
// mailbox state, and the reviewer's exact reply all describe the same request.
func deliverCommandApprovalRequest(cfg *config.Config, baseDir, contextID, requesterSessionName string, policy resolvedCommandApprovalPolicy, commandApproverNode, threadID, inputRequestID, commandHash, reason string, storeCommandText bool, now time.Time) error {
	nodes, _, err := discoverNodesForCommandApprovalDeliveryFn(baseDir, contextID, requesterSessionName)
	if err != nil {
		log.Printf("postman: WARNING: command approval delivery: discovering nodes: %v\n", err)
		return fmt.Errorf("command approval delivery failed: discovering nodes: %w", err)
	}
	resolvedApproverNode := discovery.ResolveNodeName(commandApproverNode, requesterSessionName, nodes)
	reviewerInfo, ok := nodes[resolvedApproverNode]
	if !ok {
		log.Printf("postman: WARNING: command approval delivery: command_approver_node %q not found among discovered nodes; falling back to inspect-command-approvals\n", commandApproverNode)
		return fmt.Errorf("command approval delivery failed: command_approver_node %q not found among discovered nodes", commandApproverNode)
	}
	if err := config.CreateSessionDirs(reviewerInfo.SessionDir); err != nil {
		log.Printf("postman: WARNING: command approval delivery: creating reviewer session directories: %v\n", err)
		return fmt.Errorf("command approval delivery failed: creating reviewer session directories: %w", err)
	}
	filename, err := message.GenerateFilename(now.Format("20060102-150405"), policy.Requester, commandApproverNode, reviewerInfo.SessionName)
	if err != nil {
		log.Printf("postman: WARNING: command approval delivery: generating filename: %v\n", err)
		return fmt.Errorf("command approval delivery failed: generating filename: %w", err)
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
	fmt.Fprintf(&body, "\nTo record your decision, reply with a body starting with `APPROVED: <reason>` or `NOT APPROVED: <reason>`, and keep these fields in your reply's frontmatter:\n\nthread_id: %s\nfills_input_request_id: %s\ncommand_hash: %s\n", threadID, inputRequestID, commandHash)

	content := fmt.Sprintf(
		"---\nparams:\n  contextId: %s\n  from: %s\n  to: %s\n  messageId: %s\n  replyPolicy: required\n  input_request_id: %s\n  thread_id: %s\n  command_hash: %s\n  timestamp: %s\n---\n\n%s\n",
		contextID, policy.Requester, commandApproverNode, filename, inputRequestID, threadID, commandHash, now.UTC().Format(time.RFC3339), body.String(),
	)

	// A validated command-approval request is control-plane traffic, not
	// ordinary requester mail. Deliver it through the trusted system channel so
	// the product's ordinary default-deny adjacency remains intact. The
	// envelope deliberately retains the logical requester; the system channel
	// is transport provenance, not a forged `from: daemon` claim.
	result, err := deliverCommandApprovalSystemMessageFn(
		filename,
		reviewerInfo,
		commandApproverNode,
		policy.Requester,
		contextID,
		content,
		cfg,
		nil,
		nodes,
		nil,
	)
	if err != nil {
		if result.Committed {
			retryResult, retryErr := deliverCommandApprovalSystemMessageFn(
				filename,
				reviewerInfo,
				commandApproverNode,
				policy.Requester,
				contextID,
				content,
				cfg,
				nil,
				nodes,
				nil,
			)
			if retryErr == nil && retryResult.Delivered {
				return nil
			}
			if retryErr != nil {
				return fmt.Errorf("command approval delivery failed: trusted delivery committed mailbox item but recovery retry failed: %w", retryErr)
			}
			return fmt.Errorf("command approval delivery failed: trusted delivery committed mailbox item but recovery retry left request undelivered")
		}
		return fmt.Errorf("command approval delivery failed: trusted delivery: %w", err)
	}
	if !result.Delivered {
		return fmt.Errorf("command approval delivery failed: trusted delivery left request undelivered")
	}
	return nil
}
