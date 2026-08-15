package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
)

// TestDeliverCommandApprovalRequest_WritesMessageIntoReviewerPostDir guards
// #626 M2: command_approval_delivery.go had zero test coverage because the
// real discovery.DiscoverNodesWithCollisions shells out to tmux. This test
// exercises deliverCommandApprovalRequest through the discovery seam
// instead, and asserts the delivered message lands in the reviewer's post/
// directory (not left behind in draft/), with 0600 permissions and the
// expected frontmatter and thread id instructions without requiring an
// ordinary requester-to-approver graph edge.
func TestDeliverCommandApprovalRequest_DeliversTrustedRequestToReviewerInbox(t *testing.T) {
	baseDir := t.TempDir()
	reviewerSessionDir := filepath.Join(baseDir, "ctx-626", "worker-session")
	if err := config.CreateSessionDirs(reviewerSessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs(reviewer) failed: %v", err)
	}

	original := discoverNodesForCommandApprovalDeliveryFn
	discoverNodesForCommandApprovalDeliveryFn = func(baseDir, contextID, selfSession string) (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
		return map[string]discovery.NodeInfo{
			"worker-session:orchestrator": {PaneID: "%2", SessionName: "worker-session", SessionDir: reviewerSessionDir},
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
	inputRequestID := "ireq_approval_delivery_123"
	manager := journal.NewManager("ctx-626", 31343)
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)
	var observed message.DeliveryNotificationObservation
	restoreNotificationObserver := message.SetDeliveryNotificationObserverForTest(func(observation message.DeliveryNotificationObservation) {
		observed = observation
	})
	t.Cleanup(restoreNotificationObserver)

	cfg := &config.Config{NotificationTemplate: "mail from {from_node} to {node}: {filename}"}
	if err := deliverCommandApprovalRequest(cfg, baseDir, "ctx-626", "worker-session", policy, "orchestrator", threadID, inputRequestID, "sha256:deadbeef", "verify release build", true, now); err != nil {
		t.Fatalf("deliverCommandApprovalRequest() error = %v", err)
	}

	inboxEntries, err := os.ReadDir(filepath.Join(reviewerSessionDir, "inbox", "orchestrator"))
	if err != nil {
		t.Fatalf("ReadDir(inbox) error = %v", err)
	}
	if len(inboxEntries) != 1 {
		t.Fatalf("inbox has %d entries, want 1", len(inboxEntries))
	}

	inboxPath := filepath.Join(reviewerSessionDir, "inbox", "orchestrator", inboxEntries[0].Name())
	info, err := os.Stat(inboxPath)
	if err != nil {
		t.Fatalf("Stat(inbox message) error = %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("post message perm = %v, want 0600", perm)
	}

	content, err := os.ReadFile(inboxPath)
	if err != nil {
		t.Fatalf("ReadFile(post message) error = %v", err)
	}
	body := string(content)
	for _, want := range []string{
		"from: worker",
		"to: orchestrator",
		"replyPolicy: required",
		"input_request_id: " + inputRequestID,
		"thread_id: " + threadID,
		"command_hash: sha256:deadbeef",
		"Command hash: sha256:deadbeef",
		"Requester-provided reason: verify release build",
		"The full command text is stored in this session's durable audit journal",
		"APPROVED: <reason>",
		"fills_input_request_id: " + inputRequestID,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("delivered message missing %q; got:\n%s", want, body)
		}
	}
	if strings.Contains(body, "raw-command-sentinel-never-deliver") {
		t.Fatal("delivered command approval exposed the raw command sentinel")
	}
	events, err := journal.Replay(reviewerSessionDir)
	if err != nil {
		t.Fatalf("journal.Replay() error = %v", err)
	}
	var payload map[string]string
	if err := json.Unmarshal(events[len(events)-1].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"from": "worker", "to": "orchestrator", "thread_id": threadID, "input_request_id": inputRequestID} {
		if payload[key] != want {
			t.Fatalf("payload[%s]=%q want %q", key, payload[key], want)
		}
	}
	if !strings.Contains(payload["content"], "command_hash: sha256:deadbeef") || !strings.Contains(payload["content"], "Command hash: sha256:deadbeef") || !strings.Contains(payload["content"], "Requester-provided reason: verify release build") {
		t.Fatalf("incoherent projection payload: %#v", payload)
	}
	if observed.Target.ActorID != "orchestrator" || observed.Target.SessionName != "worker-session" || observed.Recipient != "orchestrator" || observed.Sender != "worker" || observed.SourceSessionName != "worker-session" {
		t.Fatalf("notification provenance = %#v, want logical worker -> orchestrator in worker-session", observed)
	}
	if filepath.Base(observed.NotificationPath) != filepath.Base(inboxPath) {
		t.Fatalf("notification path = %q, want delivered inbox entry %q", observed.NotificationPath, inboxPath)
	}
	for _, want := range []string{"worker", "orchestrator", filepath.Base(inboxPath)} {
		if !strings.Contains(observed.Message, want) {
			t.Fatalf("real pane notification missing %q; got:\n%s", want, observed.Message)
		}
	}
	if strings.Contains(observed.Message, "worker-session:orchestrator") || strings.Contains(observed.Message, "sha256:deadbeef") || strings.Contains(observed.Message, "verify release build") {
		t.Fatalf("pane notification leaked transport identity or approval body: %q", observed.Message)
	}
}

func TestDeliverCommandApprovalRequest_RetriesCommittedUnprojectedDeliveryOnce(t *testing.T) {
	baseDir := t.TempDir()
	reviewerSessionDir := filepath.Join(baseDir, "ctx-626-retry", "worker-session")
	if err := config.CreateSessionDirs(reviewerSessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs(reviewer) failed: %v", err)
	}

	originalDiscover := discoverNodesForCommandApprovalDeliveryFn
	discoverNodesForCommandApprovalDeliveryFn = func(baseDir, contextID, selfSession string) (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
		return map[string]discovery.NodeInfo{
			"worker-session:orchestrator": {PaneID: "%2", SessionName: "worker-session", SessionDir: reviewerSessionDir},
		}, nil, nil
	}
	t.Cleanup(func() { discoverNodesForCommandApprovalDeliveryFn = originalDiscover })

	manager := journal.NewManager("ctx-626-retry", 31344)
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)

	var notifications int
	restoreNotificationObserver := message.SetDeliveryNotificationObserverForTest(func(message.DeliveryNotificationObservation) {
		notifications++
	})
	t.Cleanup(restoreNotificationObserver)

	originalDeliver := deliverCommandApprovalSystemMessageFn
	var calls int
	var firstFilename, secondFilename, firstContent, secondContent string
	failOpenCurrentWriter := true
	restoreOpenCurrentWriter := controlplane.SetOpenCurrentWriterForTest(func(sessionDir string) (*journal.Writer, error) {
		if failOpenCurrentWriter {
			failOpenCurrentWriter = false
			return nil, fmt.Errorf("injected OpenCurrentWriter failure after mailbox commit")
		}
		return journal.OpenCurrentWriter(sessionDir)
	})
	t.Cleanup(restoreOpenCurrentWriter)
	deliverCommandApprovalSystemMessageFn = func(filename string, nodeInfo discovery.NodeInfo, recipient, sender, contextID, content string, cfg *config.Config, adjacency map[string][]string, knownNodes map[string]discovery.NodeInfo, livenessMap map[string]bool) (controlplane.SystemMessageResult, error) {
		calls++
		if calls == 1 {
			firstFilename = filename
			firstContent = content
		} else if calls == 2 {
			secondFilename = filename
			secondContent = content
		}
		return originalDeliver(filename, nodeInfo, recipient, sender, contextID, content, cfg, adjacency, knownNodes, livenessMap)
	}
	t.Cleanup(func() { deliverCommandApprovalSystemMessageFn = originalDeliver })

	policy := resolvedCommandApprovalPolicy{Requester: "worker", Reviewer: "unassigned", Mode: "blocking", Label: "protected"}
	now := time.Date(2026, time.July, 8, 1, 2, 0, 0, time.UTC)
	if err := deliverCommandApprovalRequest(&config.Config{}, baseDir, "ctx-626-retry", "worker-session", policy, "orchestrator", "command-approval-retry", "ireq_retry", "sha256:deadbeef", "", false, now); err != nil {
		t.Fatalf("deliverCommandApprovalRequest() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("trusted delivery calls = %d, want first committed failure plus one retry", calls)
	}
	if firstFilename == "" || firstFilename != secondFilename || firstContent != secondContent {
		t.Fatalf("retry did not preserve immutable request identity/content: first=%q second=%q content_equal=%v", firstFilename, secondFilename, firstContent == secondContent)
	}
	if notifications != 1 {
		t.Fatalf("notifications = %d, want exactly one after recovery", notifications)
	}
	entries, err := os.ReadDir(filepath.Join(reviewerSessionDir, "inbox", "orchestrator"))
	if err != nil {
		t.Fatalf("ReadDir(inbox) error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbox entries = %d, want exactly one recovered mailbox item", len(entries))
	}
	events, err := journal.Replay(reviewerSessionDir)
	if err != nil {
		t.Fatalf("journal.Replay() error = %v", err)
	}
	var delivered int
	for _, event := range events {
		if event.Type == "mailbox_projection_delivered" {
			delivered++
		}
	}
	if delivered != 1 {
		t.Fatalf("mailbox projection delivered events = %d, want exactly one", delivered)
	}
}

// TestCommandApproverStatusAndDeliveryAgreeWithinRequesterSession verifies
// #695's configuration/status and runtime-delivery agreement: the same bare
// Mermaid-designated approver is healthy in status and resolves to the
// requester's session-qualified discovery key for delivery.
func TestCommandApproverStatusAndDeliveryAgreeWithinRequesterSession(t *testing.T) {
	baseDir := t.TempDir()
	reviewerSessionDir := filepath.Join(baseDir, "ctx-695", "worker-session")
	if err := config.CreateSessionDirs(reviewerSessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs(reviewer) failed: %v", err)
	}

	cfg := &config.Config{
		CommandApproverNode: "orchestrator",
		Nodes:               map[string]config.NodeConfig{"orchestrator": {}},
	}
	approver, valid := cfg.ResolveCommandApproverNode()
	if approver != "orchestrator" || !valid {
		t.Fatalf("ResolveCommandApproverNode() = (%q, %v), want (orchestrator, true)", approver, valid)
	}
	if status := buildCommandApprovalStatus(cfg); status != nil {
		t.Fatalf("buildCommandApprovalStatus() = %#v, want nil for a resolvable configured approver", status)
	}

	original := discoverNodesForCommandApprovalDeliveryFn
	discoverNodesForCommandApprovalDeliveryFn = func(baseDir, contextID, selfSession string) (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
		return map[string]discovery.NodeInfo{
			"worker-session:orchestrator": {
				PaneID:      "%2",
				SessionName: "worker-session",
				SessionDir:  reviewerSessionDir,
			},
		}, nil, nil
	}
	t.Cleanup(func() { discoverNodesForCommandApprovalDeliveryFn = original })

	policy := resolvedCommandApprovalPolicy{Requester: "worker", Mode: "blocking", Label: "protected"}
	if err := deliverCommandApprovalRequest(cfg, baseDir, "ctx-695", "worker-session", policy, approver, "command-approval-status-delivery", "ireq_status_delivery", "sha256:deadbeef", "", false, time.Now()); err != nil {
		t.Fatalf("deliverCommandApprovalRequest() error = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(reviewerSessionDir, "inbox", "orchestrator"))
	if err != nil {
		t.Fatalf("ReadDir(inbox) error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("inbox has %d entries, want 1", len(entries))
	}
}

// TestDeliverCommandApprovalRequest_UnknownNodeReturnsError guards #680:
// when the configured command_approver_node is not currently discoverable,
// delivery must report that fact so blocking mode can fail closed after the
// approval request is journaled.
func TestDeliverCommandApprovalRequest_UnknownNodeReturnsError(t *testing.T) {
	baseDir := t.TempDir()

	original := discoverNodesForCommandApprovalDeliveryFn
	discoverNodesForCommandApprovalDeliveryFn = func(baseDir, contextID, selfSession string) (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
		return map[string]discovery.NodeInfo{}, nil, nil
	}
	t.Cleanup(func() { discoverNodesForCommandApprovalDeliveryFn = original })

	policy := resolvedCommandApprovalPolicy{Requester: "worker", Mode: "blocking", Label: "protected"}
	err := deliverCommandApprovalRequest(&config.Config{}, baseDir, "ctx-626", "worker-session", policy, "orchestrator", "command-approval-x", "ireq_unknown", "sha256:x", "", false, time.Now())
	if err == nil {
		t.Fatal("deliverCommandApprovalRequest() error = nil, want missing approver error")
	}
	if !strings.Contains(err.Error(), `command_approver_node "orchestrator" not found among discovered nodes`) {
		t.Fatalf("deliverCommandApprovalRequest() error = %v", err)
	}
	// No panic and no filesystem assertion needed: absence of a reviewer
	// session directory under baseDir is itself proof nothing was written.
	if _, err := os.Stat(filepath.Join(baseDir, "ctx-626")); !os.IsNotExist(err) {
		t.Fatalf("expected no session directories to be created, stat error = %v", err)
	}
}

// TestDeliverCommandApprovalRequest_DoesNotRouteBareApproverAcrossSessions
// verifies that a bare command_approver_node remains scoped to the requester
// session. Cross-session delivery requires an explicitly qualified node name.
func TestDeliverCommandApprovalRequest_DoesNotRouteBareApproverAcrossSessions(t *testing.T) {
	baseDir := t.TempDir()

	original := discoverNodesForCommandApprovalDeliveryFn
	discoverNodesForCommandApprovalDeliveryFn = func(baseDir, contextID, selfSession string) (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
		return map[string]discovery.NodeInfo{
			"other-session:orchestrator": {
				PaneID:      "%2",
				SessionName: "other-session",
				SessionDir:  filepath.Join(baseDir, contextID, "other-session"),
			},
		}, nil, nil
	}
	t.Cleanup(func() { discoverNodesForCommandApprovalDeliveryFn = original })

	policy := resolvedCommandApprovalPolicy{Requester: "worker", Mode: "blocking", Label: "protected"}
	err := deliverCommandApprovalRequest(&config.Config{}, baseDir, "ctx-680", "worker-session", policy, "orchestrator", "command-approval-x", "ireq_cross_session", "sha256:x", "", false, time.Now())
	if err == nil {
		t.Fatal("deliverCommandApprovalRequest() error = nil, want missing same-session approver error")
	}
	if _, err := os.Stat(filepath.Join(baseDir, "ctx-680")); !os.IsNotExist(err) {
		t.Fatalf("expected no cross-session delivery, stat error = %v", err)
	}
}
