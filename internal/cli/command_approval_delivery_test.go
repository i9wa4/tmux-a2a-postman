package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

// TestDeliverCommandApprovalRequest_WritesMessageIntoReviewerPostDir guards
// #626 M2: command_approval_delivery.go had zero test coverage because the
// real discovery.DiscoverNodesWithCollisions shells out to tmux. This test
// exercises deliverCommandApprovalRequest through the discovery seam
// instead, and asserts the delivered message lands in the reviewer's post/
// directory (not left behind in draft/), with 0600 permissions and the
// expected frontmatter and thread id instructions.
func TestDeliverCommandApprovalRequest_WritesMessageIntoReviewerPostDir(t *testing.T) {
	baseDir := t.TempDir()
	reviewerSessionDir := filepath.Join(baseDir, "ctx-626", "reviewer-session")
	if err := config.CreateSessionDirs(reviewerSessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs(reviewer) failed: %v", err)
	}

	original := discoverNodesForCommandApprovalDeliveryFn
	discoverNodesForCommandApprovalDeliveryFn = func(baseDir, contextID, selfSession string) (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
		return map[string]discovery.NodeInfo{
			"orchestrator": {PaneID: "%2", SessionName: "reviewer-session", SessionDir: reviewerSessionDir},
		}, nil, nil
	}
	t.Cleanup(func() { discoverNodesForCommandApprovalDeliveryFn = original })

	policy := resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "unassigned",
		Mode:      "blocking",
		Label:     "protected",
		Category:  "release",
	}
	now := time.Date(2026, time.July, 8, 1, 0, 0, 0, time.UTC)
	threadID := "command-approval-aabbccdd11223344"

	deliverCommandApprovalRequest(&config.Config{}, baseDir, "ctx-626", "worker-session", policy, "orchestrator", threadID, "sha256:deadbeef", "verify release build", false, now)

	draftEntries, err := os.ReadDir(filepath.Join(reviewerSessionDir, "draft"))
	if err != nil {
		t.Fatalf("ReadDir(draft) error = %v", err)
	}
	if len(draftEntries) != 0 {
		t.Fatalf("draft/ has %d leftover entries, want 0 (message must be renamed into post/)", len(draftEntries))
	}

	postEntries, err := os.ReadDir(filepath.Join(reviewerSessionDir, "post"))
	if err != nil {
		t.Fatalf("ReadDir(post) error = %v", err)
	}
	if len(postEntries) != 1 {
		t.Fatalf("post/ has %d entries, want 1", len(postEntries))
	}

	postPath := filepath.Join(reviewerSessionDir, "post", postEntries[0].Name())
	info, err := os.Stat(postPath)
	if err != nil {
		t.Fatalf("Stat(post message) error = %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("post message perm = %v, want 0600", perm)
	}

	content, err := os.ReadFile(postPath)
	if err != nil {
		t.Fatalf("ReadFile(post message) error = %v", err)
	}
	body := string(content)
	for _, want := range []string{
		"from: worker",
		"to: orchestrator",
		"replyPolicy: required",
		"thread_id: " + threadID,
		"Command hash: sha256:deadbeef",
		"Requester-provided reason: verify release build",
		"APPROVED: <reason>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("delivered message missing %q; got:\n%s", want, body)
		}
	}
}

// TestDeliverCommandApprovalRequest_UnknownNodeIsBestEffort guards the
// no-op-not-crash behavior when the command_approver_node isn't currently
// discoverable: delivery must log and return without writing anything or
// panicking, since the approval request has already been journaled by the
// caller regardless of delivery outcome.
func TestDeliverCommandApprovalRequest_UnknownNodeIsBestEffort(t *testing.T) {
	baseDir := t.TempDir()

	original := discoverNodesForCommandApprovalDeliveryFn
	discoverNodesForCommandApprovalDeliveryFn = func(baseDir, contextID, selfSession string) (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
		return map[string]discovery.NodeInfo{}, nil, nil
	}
	t.Cleanup(func() { discoverNodesForCommandApprovalDeliveryFn = original })

	policy := resolvedCommandApprovalPolicy{Requester: "worker", Mode: "blocking", Label: "protected"}
	deliverCommandApprovalRequest(&config.Config{}, baseDir, "ctx-626", "worker-session", policy, "orchestrator", "command-approval-x", "sha256:x", "", false, time.Now())
	// No panic and no filesystem assertion needed: absence of a reviewer
	// session directory under baseDir is itself proof nothing was written.
	if _, err := os.Stat(filepath.Join(baseDir, "ctx-626")); !os.IsNotExist(err) {
		t.Fatalf("expected no session directories to be created, stat error = %v", err)
	}
}
