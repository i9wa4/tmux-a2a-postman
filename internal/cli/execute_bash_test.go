package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

type executeBashFixture struct {
	baseDir              string
	contextID            string
	sessionName          string
	sessionDir           string
	now                  time.Time
	policies             []config.CommandApprovalPolicy
	commandApproverNode  string
	nodes                map[string]config.NodeConfig
	discoveredNodes      map[string]discovery.NodeInfo
	notificationTemplate string
	stdout               bytes.Buffer
	stderr               bytes.Buffer
	runCount             int
	commands             []string
	runStatus            int
	runErr               error
}

func newExecuteBashFixture(t *testing.T, policies ...config.CommandApprovalPolicy) *executeBashFixture {
	t.Helper()

	baseDir := t.TempDir()
	contextID := "ctx-484"
	sessionName := "test-session"
	fixture := &executeBashFixture{
		baseDir:     baseDir,
		contextID:   contextID,
		sessionName: sessionName,
		sessionDir:  filepath.Join(baseDir, contextID, sessionName),
		now:         time.Date(2026, time.June, 1, 10, 0, 0, 0, time.UTC),
		policies:    policies,
		// #626: every existing fixture test uses "orchestrator" as the
		// approval Reviewer label; defaulting it here as a valid
		// command_approver_node too keeps these tests exercising the real
		// advisory/warn-only/blocking evaluation path instead of the
		// unified fail-open rule (which only applies when no VALID
		// command_approver_node is configured). Tests exercising the fail-open rule
		// itself override commandApproverNode/nodes before calling context().
		commandApproverNode: "orchestrator",
		nodes:               map[string]config.NodeConfig{"orchestrator": {}},
		discoveredNodes: map[string]discovery.NodeInfo{
			"test-session:orchestrator": {
				SessionName: "test-session",
				SessionDir:  filepath.Join(baseDir, contextID, "test-session"),
			},
		},
	}
	originalDiscover := discoverNodesForCommandApprovalDeliveryFn
	discoverNodesForCommandApprovalDeliveryFn = func(baseDir, contextID, selfSession string) (map[string]discovery.NodeInfo, []discovery.CollisionReport, error) {
		return fixture.discoveredNodes, nil, nil
	}
	t.Cleanup(func() { discoverNodesForCommandApprovalDeliveryFn = originalDiscover })
	return fixture
}

func (f *executeBashFixture) context() commandContext {
	return f.contextAsPane("worker")
}

// contextAsPane returns a commandContext whose tmux pane title (the
// authenticated caller identity --record-decision relies on, #626
// B1-residual) is paneName instead of the default "worker" requester
// identity — used to simulate a --record-decision call actually coming
// from the command_approver_node's own pane, structurally distinct from the
// requester's.
func (f *executeBashFixture) contextAsPane(paneName string) commandContext {
	return commandContext{
		stdout: &f.stdout,
		stderr: &f.stderr,
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{
				BaseDir:              f.baseDir,
				CommandApproval:      f.policies,
				CommandApproverNode:  f.commandApproverNode,
				Nodes:                f.nodes,
				NotificationTemplate: f.notificationTemplate,
			}, nil
		},
		getTmuxPaneName:    func() string { return paneName },
		getTmuxSessionName: func() string { return f.sessionName },
		now:                func() time.Time { return f.now },
		runBash: func(command string, stdout, stderr io.Writer) (int, error) {
			f.runCount++
			f.commands = append(f.commands, command)
			_, _ = fmt.Fprint(stdout, "ran\n")
			return f.runStatus, f.runErr
		},
	}
}

func (f *executeBashFixture) args(extra ...string) []string {
	base := []string{
		"--context-id", f.contextID,
		"--session", f.sessionName,
		"--requester", "worker",
	}
	return append(base, extra...)
}

func TestRunExecuteBashAdvisoryRecordsRequestWithoutCommandTextAndRuns(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	commandText := "printf raw-command-sentinel-7f3c3d9b-never-mailbox"

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "low-risk",
		"--category", "diagnostic",
		"--reviewer", "orchestrator",
		"--mode", "advisory",
		"--reason", "collect harmless diagnostic",
		"--command", commandText,
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
	if got := fixture.commands[0]; got != commandText {
		t.Fatalf("command = %q, want %q", got, commandText)
	}

	events := replayCommandEvents(t, fixture.sessionDir)
	var requestPayload journal.CommandApprovalRequestPayload
	foundRequest := false
	for _, event := range events {
		if bytes.Contains(event.Payload, []byte(commandText)) || bytes.Contains(event.Payload, []byte("command_text")) {
			t.Fatalf("event %s stored full command text by default: %s", event.Type, event.Payload)
		}
		if event.Type == journal.CommandApprovalRequestedEventType {
			foundRequest = true
			if err := json.Unmarshal(event.Payload, &requestPayload); err != nil {
				t.Fatalf("Unmarshal(request): %v", err)
			}
		}
	}
	if !foundRequest {
		t.Fatal("missing command approval request event")
	}
	if requestPayload.Requester != "worker" || requestPayload.Reviewer != "orchestrator" {
		t.Fatalf("request requester/reviewer = %q/%q", requestPayload.Requester, requestPayload.Reviewer)
	}
	if requestPayload.Mode != "advisory" || requestPayload.Label != "low-risk" || requestPayload.Category != "diagnostic" {
		t.Fatalf("request policy metadata = %#v", requestPayload)
	}
	if requestPayload.CommandHash == "" || requestPayload.Reason == "" || requestPayload.ExpiresAt == "" {
		t.Fatalf("request missing digest, reason, or expiry: %#v", requestPayload)
	}
}

func TestRunExecuteBashStoreCommandTextOptIn(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	commandText := "printf audit-me"

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "diagnostic",
		"--mode", "advisory",
		"--reviewer", "orchestrator",
		"--store-command-text",
		"--command", commandText,
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v", err)
	}

	for _, event := range replayCommandEvents(t, fixture.sessionDir) {
		if bytes.Contains(event.Payload, []byte(commandText)) && bytes.Contains(event.Payload, []byte("command_text")) {
			return
		}
	}
	t.Fatal("no audit event stored command_text after explicit opt in")
}

func TestRunExecuteBashStoreCommandTextDoesNotLeakToApprovalMailbox(t *testing.T) {
	policy := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policy)
	approverInfo := discovery.NodeInfo{SessionName: fixture.sessionName, SessionDir: fixture.sessionDir}
	fixture.discoveredNodes = map[string]discovery.NodeInfo{
		"orchestrator":              approverInfo,
		"test-session:orchestrator": approverInfo,
	}
	commandText := "printf raw-command-sentinel-mailbox-boundary"
	var observed message.DeliveryNotificationObservation
	restoreNotificationObserver := message.SetDeliveryNotificationObserverForTest(func(observation message.DeliveryNotificationObservation) {
		observed = observation
	})
	t.Cleanup(restoreNotificationObserver)

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--store-command-text",
		"--reason", "review raw command storage boundary",
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want pending blocking approval")
	}
	if !strings.Contains(err.Error(), "approval is absent") {
		t.Fatalf("error = %v, want pending approval absence", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want zero execution", fixture.runCount)
	}

	for _, forbidden := range []string{commandText, "raw-command-sentinel-mailbox-boundary"} {
		if strings.Contains(observed.Message, forbidden) {
			t.Fatalf("approval notification leaked raw command sentinel %q in %q", forbidden, observed.Message)
		}
	}
	if observed.Target.ActorID != "orchestrator" || observed.Recipient != "orchestrator" || observed.Sender != "worker" {
		t.Fatalf("notification provenance = %#v, want logical worker -> orchestrator", observed)
	}

	events := replayCommandEvents(t, fixture.sessionDir)
	storedInRequesterAudit := false
	for _, event := range events {
		if event.Type == journal.CommandApprovalRequestedEventType && bytes.Contains(event.Payload, []byte(commandText)) && bytes.Contains(event.Payload, []byte("command_text")) {
			storedInRequesterAudit = true
		}
	}
	if !storedInRequesterAudit {
		t.Fatal("requester audit did not store command_text after explicit opt in")
	}
	deadLetters, err := filepath.Glob(filepath.Join(fixture.sessionDir, "dead-letter", "*.md"))
	if err != nil {
		t.Fatalf("Glob(dead-letter) error = %v", err)
	}
	if len(deadLetters) != 0 {
		t.Fatalf("dead letters = %v, want none for trusted approval request delivery", deadLetters)
	}
}

func TestRunExecuteBashCommandApprovalABCLifecycleRejectsWithoutExecution(t *testing.T) {
	policy := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policy)
	manager := journal.NewManager(fixture.contextID, os.Getpid())
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)
	fixture.commandApproverNode = "approver"
	fixture.notificationTemplate = "notice {from_node}->{node} {filename}"
	fixture.nodes = map[string]config.NodeConfig{
		"worker":       {},
		"orchestrator": {},
		"approver":     {},
		"other":        {},
	}
	fixture.discoveredNodes = map[string]discovery.NodeInfo{
		"test-session:worker":       {PaneID: "%1", SessionName: fixture.sessionName, SessionDir: fixture.sessionDir},
		"test-session:orchestrator": {PaneID: "%2", SessionName: fixture.sessionName, SessionDir: fixture.sessionDir},
		"test-session:approver":     {PaneID: "%3", SessionName: fixture.sessionName, SessionDir: fixture.sessionDir},
		"test-session:other":        {PaneID: "%4", SessionName: fixture.sessionName, SessionDir: fixture.sessionDir},
	}
	nodes := fixture.discoveredNodes
	adjacency := map[string][]string{
		"worker":       {"orchestrator"},
		"orchestrator": {"approver"},
		"approver":     {"orchestrator"},
	}
	commandText := "printf f006-rejection-lifecycle-sentinel"
	var observed message.DeliveryNotificationObservation
	restoreNotificationObserver := message.SetDeliveryNotificationObserverForTest(func(observation message.DeliveryNotificationObservation) {
		observed = observation
	})
	t.Cleanup(restoreNotificationObserver)

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--reviewer", "orchestrator",
		"--reason", "F006 lifecycle request",
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want pending blocking approval")
	}
	if !strings.Contains(err.Error(), "approval is absent") {
		t.Fatalf("runExecuteBashWithContext() error = %v, want approval absence", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount after request = %d, want zero execution", fixture.runCount)
	}

	threadID, thread := onlyApprovalThread(t, fixture.sessionDir, fixture.now)
	if thread.Status != projection.CommandApprovalStatusPending {
		t.Fatalf("initial status = %q, want pending", thread.Status)
	}
	if thread.Requester != "worker" || thread.Reviewer != "orchestrator" || thread.CommandApproverNode != "approver" {
		t.Fatalf("thread routing fields = %#v, want requester worker, reviewer orchestrator, approver approver", thread)
	}
	if thread.InputRequestID == "" || thread.CommandHash == "" {
		t.Fatalf("thread missing correlation metadata: %#v", thread)
	}
	assertApprovalReplySlot(t, fixture.sessionDir, fixture.sessionName, thread.InputRequestID, true)
	if observed.Target.ActorID != "approver" || observed.Recipient != "approver" || observed.Sender != "worker" {
		t.Fatalf("notification provenance = %#v, want logical worker -> approver", observed)
	}
	if !strings.Contains(observed.Message, "worker->approver") || !strings.Contains(observed.Message, filepath.Base(observed.NotificationPath)) {
		t.Fatalf("notification message = %q, want sender, recipient, and message filename", observed.Message)
	}
	if strings.Contains(observed.Message, commandText) {
		t.Fatalf("approval notification leaked raw command text: %q", observed.Message)
	}
	requestFiles := listDirNames(t, filepath.Join(fixture.sessionDir, "inbox", "approver"))
	if len(requestFiles) != 1 {
		t.Fatalf("approver inbox files = %v, want exactly one approval request; session files: %v; events: %v; observed: %#v", requestFiles, walkRelativeFiles(t, fixture.sessionDir), summarizeJournalEvents(t, fixture.sessionDir), observed)
	}
	requestContent := readFileString(t, filepath.Join(fixture.sessionDir, "inbox", "approver", requestFiles[0]))
	for _, required := range []string{threadID, thread.InputRequestID, thread.CommandHash, "fills_input_request_id"} {
		if !strings.Contains(requestContent, required) {
			t.Fatalf("approval request missing %q:\n%s", required, requestContent)
		}
	}
	for _, actor := range []string{"worker", "orchestrator", "other"} {
		if got := listDirNames(t, filepath.Join(fixture.sessionDir, "inbox", actor)); len(got) != 0 {
			t.Fatalf("%s inbox files = %v, want no approval request", actor, got)
		}
	}

	deliverLifecyclePost(t, fixture.sessionDir, "20260601-100001-rabc-from-worker-to-approver.md", "test-session", nodes, adjacency, func(string) bool { return true }, lifecycleEnvelope(fixture.contextID, "worker", "approver", "", "", "", "ordinary A to C mail"))
	assertApprovalLifecycle(t, fixture.sessionDir, fixture.now, threadID, projection.CommandApprovalStatusPending, 0, "")
	assertLifecycleDeadLetters(t, fixture.sessionDir, "dl-routing-denied", 1)
	if got := listDirNames(t, filepath.Join(fixture.sessionDir, "inbox", "approver")); len(got) != 1 {
		t.Fatalf("approver inbox files after ordinary A->C = %v, want only approval request", got)
	}

	invalidAttempts := []struct {
		name        string
		filename    string
		from        string
		to          string
		threadID    string
		fillID      string
		commandHash string
		body        string
		enabled     func(string) bool
		wantSuffix  string
	}{
		{name: "mismatched fill", filename: "20260601-100002-rabc-from-approver-to-worker.md", from: "approver", to: "worker", threadID: threadID, fillID: "ireq_wrong", commandHash: thread.CommandHash, body: "NOT APPROVED: wrong fill.", wantSuffix: "dl-routing-denied"},
		{name: "mismatched hash", filename: "20260601-100003-rabc-from-approver-to-worker.md", from: "approver", to: "worker", threadID: threadID, fillID: thread.InputRequestID, commandHash: "sha256:badc0ffee", body: "NOT APPROVED: wrong hash.", wantSuffix: "dl-routing-denied"},
		{name: "mismatched thread", filename: "20260601-100004-rabc-from-approver-to-worker.md", from: "approver", to: "worker", threadID: "command-approval-wrong-thread", fillID: thread.InputRequestID, commandHash: thread.CommandHash, body: "NOT APPROVED: wrong thread.", wantSuffix: "dl-routing-denied"},
		{name: "mismatched requester", filename: "20260601-100005-rabc-from-approver-to-other.md", from: "approver", to: "other", threadID: threadID, fillID: thread.InputRequestID, commandHash: thread.CommandHash, body: "NOT APPROVED: wrong requester.", wantSuffix: "dl-routing-denied"},
		{name: "mismatched reviewer", filename: "20260601-100006-rabc-from-orchestrator-to-worker.md", from: "orchestrator", to: "worker", threadID: threadID, fillID: thread.InputRequestID, commandHash: thread.CommandHash, body: "NOT APPROVED: wrong reviewer.", wantSuffix: "dl-routing-denied"},
		{name: "session disabled", filename: "20260601-100007-rabc-from-approver-to-worker.md", from: "approver", to: "worker", threadID: threadID, fillID: thread.InputRequestID, commandHash: thread.CommandHash, body: "NOT APPROVED: disabled.", enabled: func(string) bool { return false }, wantSuffix: "dl-session-disabled"},
	}
	expectedDeadLetters := map[string]int{"dl-routing-denied": 1}
	for _, attempt := range invalidAttempts {
		t.Run(attempt.name, func(t *testing.T) {
			enabled := attempt.enabled
			if enabled == nil {
				enabled = func(string) bool { return true }
			}
			deliverLifecyclePost(t, fixture.sessionDir, attempt.filename, "test-session", nodes, adjacency, enabled, lifecycleEnvelope(fixture.contextID, attempt.from, attempt.to, attempt.threadID, attempt.fillID, attempt.commandHash, attempt.body))
			assertApprovalLifecycle(t, fixture.sessionDir, fixture.now, threadID, projection.CommandApprovalStatusPending, 0, "")
			assertApprovalReplySlot(t, fixture.sessionDir, fixture.sessionName, thread.InputRequestID, true)
			expectedDeadLetters[attempt.wantSuffix]++
			assertLifecycleDeadLetters(t, fixture.sessionDir, attempt.wantSuffix, expectedDeadLetters[attempt.wantSuffix])
		})
	}

	rejectReason := "F006 explicit rejection."
	validRejectFilename := "20260601-100008-rabc-from-approver-to-worker.md"
	deliverLifecyclePost(t, fixture.sessionDir, validRejectFilename, "test-session", nodes, adjacency, func(string) bool { return true }, lifecycleEnvelope(fixture.contextID, "approver", "worker", threadID, thread.InputRequestID, thread.CommandHash, "NOT APPROVED: "+rejectReason))
	assertApprovalLifecycle(t, fixture.sessionDir, fixture.now, threadID, projection.CommandApprovalStatusRejected, 1, rejectReason)
	assertApprovalReplySlot(t, fixture.sessionDir, fixture.sessionName, thread.InputRequestID, false)
	assertLifecycleDeadLetters(t, fixture.sessionDir, "dl-routing-denied", 7)
	if fixture.runCount != 0 {
		t.Fatalf("runCount after rejection = %d, want zero execution", fixture.runCount)
	}

	deliverLifecyclePost(t, fixture.sessionDir, "20260601-100009-rabc-from-approver-to-worker.md", "test-session", nodes, adjacency, func(string) bool { return true }, lifecycleEnvelope(fixture.contextID, "approver", "worker", threadID, thread.InputRequestID, thread.CommandHash, "NOT APPROVED: replayed rejection."))
	assertApprovalLifecycle(t, fixture.sessionDir, fixture.now, threadID, projection.CommandApprovalStatusRejected, 1, rejectReason)
	assertApprovalReplySlot(t, fixture.sessionDir, fixture.sessionName, thread.InputRequestID, false)
	assertLifecycleDeadLetters(t, fixture.sessionDir, "dl-routing-denied", 8)
	if fixture.runCount != 0 {
		t.Fatalf("runCount after duplicate replay = %d, want zero execution", fixture.runCount)
	}
}

func TestRunExecuteBashWarnOnlyRequiresOverride(t *testing.T) {
	policy := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "deploy",
		Mode:      "warn-only",
	}
	fixture := newExecuteBashFixture(t, policy)

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "deploy",
		"--command", "printf deploy",
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want warn-only block")
	}
	if !strings.Contains(err.Error(), "warn-only mode requires --override-approval") {
		t.Fatalf("error = %v, want warn-only override guidance", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

func TestRunExecuteBashWarnOnlyOverrideRunsAndAudits(t *testing.T) {
	policy := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "deploy",
		Mode:      "warn-only",
	}
	fixture := newExecuteBashFixture(t, policy)

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "deploy",
		"--override-approval",
		"--command", "printf deploy",
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
	decision := findExecutionDecisionPayload(t, fixture.sessionDir)
	if decision.Decision != "warn_override" || !decision.Override {
		t.Fatalf("execution decision = %#v, want warn override", decision)
	}
}

func TestRunExecuteBashBlockingRefusesInvalidApprovals(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setup      func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string)
		command    string
		wantReason string
	}{
		{
			name: "absent",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
			},
			command:    "printf absent",
			wantReason: "approval is absent",
		},
		{
			name: "stale",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
				threadID := commandApprovalThreadID(policy, commandDigest(commandText))
				fixture.appendCommandApprovalDecisionOnly(t, threadID, "orchestrator", journal.ApprovalDecisionApproved)
			},
			command:    "printf stale",
			wantReason: "approval decision is stale",
		},
		{
			name: "rejected",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
				fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionRejected, "orchestrator", fixture.now.Add(15*time.Minute))
			},
			command:    "printf rejected",
			wantReason: "approval is rejected",
		},
		{
			name: "expired",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
				fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionApproved, "orchestrator", fixture.now.Add(-time.Second))
			},
			command:    "printf expired",
			wantReason: "approval is expired",
		},
		{
			name: "wrong reviewer remains pending",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
				fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionApproved, "critic", fixture.now.Add(15*time.Minute))
			},
			command:    "printf reviewer",
			wantReason: "approval is pending",
		},
		{
			name: "changed digest",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
				fixture.appendCommandApproval(t, policy, "printf original", journal.ApprovalDecisionApproved, "orchestrator", fixture.now.Add(15*time.Minute))
			},
			command:    "printf changed",
			wantReason: "different command digest",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policyConfig := config.CommandApprovalPolicy{
				Requester: "worker",
				Reviewer:  "orchestrator",
				Label:     "protected",
				Category:  "release",
				Mode:      "blocking",
			}
			fixture := newExecuteBashFixture(t, policyConfig)
			policy := resolvedCommandApprovalPolicy{
				Requester: "worker",
				Reviewer:  "orchestrator",
				Mode:      "blocking",
				Label:     "protected",
				Category:  "release",
				TTL:       defaultCommandApprovalTTL,
			}
			tc.setup(t, fixture, policy, tc.command)

			err := runExecuteBashWithContext(fixture.context(), fixture.args(
				"--label", "protected",
				"--category", "release",
				"--command", tc.command,
			))
			if err == nil {
				t.Fatal("runExecuteBashWithContext() error = nil, want blocking refusal")
			}
			if !strings.Contains(err.Error(), tc.wantReason) {
				t.Fatalf("error = %v, want reason containing %q", err, tc.wantReason)
			}
			if fixture.runCount != 0 {
				t.Fatalf("runCount = %d, want 0", fixture.runCount)
			}
		})
	}
}

// TestRunExecuteBashBlockingRejectsSelfDeclaredReviewer guards #626
// B1-residual: a requester calling --record-decision from their OWN tmux
// pane, self-declaring via --reviewer as the configured command_approver_node's
// name (trivially readable from postman.toml or get-status), must be
// refused at the decision-recording step itself — --reviewer must have no
// bearing on acceptance. Only a call whose AUTHENTICATED caller identity
// (tmux pane title) matches the trusted command_approver_node is ever honored; see
// TestRunExecuteBashBlockingAcceptsRealCommandApproverNodeDespiteUnassignedLabel
// for that positive case, exercised from a structurally different caller
// identity via contextAsPane.
func TestRunExecuteBashBlockingRejectsSelfDeclaredReviewer(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "worker", // requester-controlled label naming itself as reviewer
		Label:     "protected",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	fixture.commandApproverNode = "orchestrator" // the actual, admin-configured command_approver_node
	fixture.nodes = map[string]config.NodeConfig{"orchestrator": {}, "worker": {}}
	commandText := "printf self-approve"

	// First invocation, from the requester's own pane ("worker"): creates
	// the approval request and blocks.
	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--reviewer", "worker",
		"--mode", "blocking",
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("first invocation error = nil, want blocking refusal")
	}
	threadID := commandApprovalThreadID(resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "worker",
		Mode:      "blocking",
		Label:     "protected",
	}, commandDigest(commandText))

	// The requester, still calling from their own "worker" pane, attempts
	// to self-declare as the reviewer via --reviewer=orchestrator (the
	// exploit: this name is public, readable from config/get-status). This
	// must be refused at the decision-recording step itself, because the
	// AUTHENTICATED caller ("worker") does not match command_approver_node
	// ("orchestrator") — regardless of what --reviewer claims.
	err = runExecuteBashWithContext(fixture.context(), fixture.args(
		"--thread-id", threadID,
		"--reviewer", "orchestrator",
		"--record-decision", "approved",
	))
	if err == nil {
		t.Fatal("record-decision error = nil, want refusal (self-declared --reviewer must not authenticate the caller)")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Fatalf("error = %v, want a --record-decision refusal", err)
	}

	// The command must still refuse: no valid decision was ever recorded.
	err = runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--reviewer", "worker",
		"--mode", "blocking",
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("second invocation error = nil, want blocking refusal (self-approval must not succeed)")
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0 (self-approved command must never run)", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingRecordDecisionRefusedFromNonReviewerCaller is
// the CLI refusal test guardian asked for explicitly: --record-decision
// --reviewer <command_approver_node_name> issued from a caller whose own pane
// identity is NOT the command_approver_node must be refused, independent of the
// self-approval framing above.
func TestRunExecuteBashBlockingRecordDecisionRefusedFromNonReviewerCaller(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	fixture.commandApproverNode = "orchestrator"
	fixture.nodes = map[string]config.NodeConfig{"orchestrator": {}, "bystander": {}}
	commandText := "printf non-reviewer-caller"

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--mode", "blocking",
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("first invocation error = nil, want blocking refusal pending approval")
	}
	threadID := commandApprovalThreadID(resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "unassigned",
		Mode:      "blocking",
		Label:     "protected",
	}, commandDigest(commandText))

	// A third-party pane ("bystander"), neither the requester nor the real
	// command_approver_node, tries to record a decision naming the real
	// command_approver_node via --reviewer.
	err = runExecuteBashWithContext(fixture.contextAsPane("bystander"), fixture.args(
		"--thread-id", threadID,
		"--reviewer", "orchestrator",
		"--record-decision", "approved",
	))
	if err == nil {
		t.Fatal("record-decision error = nil, want refusal from a non-reviewer caller")
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Fatalf("error = %v, want a --record-decision refusal", err)
	}
}

// TestRunExecuteBashBlockingAcceptsRealCommandApproverNodeDespiteUnassignedLabel
// guards the honest-admin side of #626 B1: when policy.Reviewer is left at
// its "unassigned" default (no matching command_approval policy sets a
// Reviewer label) but a valid command_approver_node is configured, a decision from
// that real command_approver_node must be accepted — it must not get stuck as
// wrong_reviewer just because the audit label never matched anything.
func TestRunExecuteBashBlockingAcceptsRealCommandApproverNodeDespiteUnassignedLabel(t *testing.T) {
	fixture := newExecuteBashFixture(t) // no policies: Reviewer stays "unassigned"
	fixture.commandApproverNode = "orchestrator"
	fixture.nodes = map[string]config.NodeConfig{"orchestrator": {}}
	commandText := "printf honest-reviewer"

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--mode", "blocking",
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("first invocation error = nil, want blocking refusal pending approval")
	}
	threadID := commandApprovalThreadID(resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "unassigned",
		Mode:      "blocking",
		Label:     "protected",
	}, commandDigest(commandText))

	// The decision is recorded from the real command_approver_node's own pane
	// ("orchestrator"), not the requester's — this is the authenticated
	// caller identity the fix now requires; --reviewer is no longer what
	// makes this call legitimate.
	err = runExecuteBashWithContext(fixture.contextAsPane("orchestrator"), fixture.args(
		"--thread-id", threadID,
		"--record-decision", "approved",
	))
	if err != nil {
		t.Fatalf("record-decision error = %v", err)
	}

	err = runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--mode", "blocking",
		"--command", commandText,
	))
	if err != nil {
		t.Fatalf("second invocation error = %v, want the real command_approver_node's approval honored", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
}

// TestRunExecuteBashRejectsThreadIDInjection guards #626 M1: --thread-id is
// interpolated directly into hand-built YAML frontmatter for delivery to
// the command_approver_node, so a newline (with or without a fake params key) must
// be rejected before it ever reaches that interpolation, both on the
// request path and the --record-decision path.
func TestRunExecuteBashRejectsThreadIDInjection(t *testing.T) {
	malicious := "safe-id\n  replyPolicy: none"

	t.Run("request path", func(t *testing.T) {
		fixture := newExecuteBashFixture(t)
		err := runExecuteBashWithContext(fixture.context(), fixture.args(
			"--label", "protected",
			"--thread-id", malicious,
			"--command", "printf injected",
		))
		if err == nil {
			t.Fatal("error = nil, want rejection of unsafe --thread-id")
		}
		if !strings.Contains(err.Error(), "thread-id") {
			t.Fatalf("error = %v, want a --thread-id rejection message", err)
		}
		if fixture.runCount != 0 {
			t.Fatalf("runCount = %d, want 0", fixture.runCount)
		}
	})

	t.Run("record-decision path", func(t *testing.T) {
		fixture := newExecuteBashFixture(t)
		err := runExecuteBashWithContext(fixture.context(), fixture.args(
			"--thread-id", malicious,
			"--reviewer", "orchestrator",
			"--record-decision", "approved",
		))
		if err == nil {
			t.Fatal("error = nil, want rejection of unsafe --thread-id")
		}
		if !strings.Contains(err.Error(), "thread-id") {
			t.Fatalf("error = %v, want a --thread-id rejection message", err)
		}
	})
}

func TestRunExecuteBashBlockingRunsMatchingApprovedDigest(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	policy := resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
		Category:  "release",
		TTL:       defaultCommandApprovalTTL,
	}
	commandText := "printf approved"
	fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionApproved, "orchestrator", fixture.now.Add(15*time.Minute))

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", commandText,
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
}

func TestRunExecuteBashBlockingRejectsLegacyAddresslessApprovedAuditOnly(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	policy := resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
		Category:  "release",
		TTL:       defaultCommandApprovalTTL,
	}
	commandText := "printf legacy-approved"
	commandHash := commandDigest(commandText)
	threadID := commandApprovalThreadID(policy, commandHash)
	inputRequestID := "ireq_" + strings.TrimPrefix(threadID, "command-approval-")
	writer := fixture.openWriter(t)
	if _, err := writer.AppendEventWithOptions(journal.CommandApprovalRequestedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalRequestPayload{
		Requester:           "worker",
		Reviewer:            "orchestrator",
		CommandApproverNode: "orchestrator",
		Mode:                "blocking",
		Label:               "protected",
		Category:            "release",
		CommandHash:         commandHash,
		InputRequestID:      inputRequestID,
		Reason:              "legacy request",
		ExpiresAt:           fixture.now.Add(15 * time.Minute).UTC().Format(time.RFC3339Nano),
	}, journal.AppendOptions{ThreadID: threadID}, fixture.now); err != nil {
		t.Fatalf("AppendEventWithOptions(legacy request): %v", err)
	}
	if _, err := writer.AppendEventWithOptions(journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalDecisionPayload{
		Reviewer:       "orchestrator",
		Decision:       journal.ApprovalDecisionApproved,
		Reason:         "legacy approved",
		InputRequestID: inputRequestID,
		CommandHash:    commandHash,
	}, journal.AppendOptions{ThreadID: threadID}, fixture.now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEventWithOptions(legacy decision): %v", err)
	}

	state, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now.Add(2*time.Second))
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() = (%#v, %v, %v), want legacy approval state", state, ok, err)
	}
	thread := state.Threads[threadID]
	if thread.Status != projection.CommandApprovalStatusApproved || !thread.HistoricalOnly {
		t.Fatalf("legacy thread = %#v, want approved historical-only audit state", thread)
	}
	if err := journal.SyncCommandApprovalDecisionHistory(fixture.sessionDir); err != nil {
		t.Fatalf("SyncCommandApprovalDecisionHistory() error = %v", err)
	}
	history, err := journal.ListCommandApprovalDecisionHistory(fixture.sessionDir)
	if err != nil {
		t.Fatalf("ListCommandApprovalDecisionHistory() error = %v", err)
	}
	if len(history) != 1 || history[0].EffectiveStatus != "approved" || !history[0].HistoricalOnly {
		t.Fatalf("legacy history = %#v, want approved historical-only audit entry", history)
	}

	err = runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--category", "release",
		"--thread-id", threadID,
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want historical-only approval block")
	}
	if !strings.Contains(err.Error(), "historical audit-only") {
		t.Fatalf("error = %v, want historical-only diagnostic", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0; legacy address-less approval must not authorize live execution", fixture.runCount)
	}
	decision := findExecutionDecisionPayload(t, fixture.sessionDir)
	if decision.Decision != "blocked" || !strings.Contains(decision.Reason, "historical audit-only") {
		t.Fatalf("execution decision = %#v, want blocked historical-only reason", decision)
	}
}

// TestRunExecuteBashBlockingFailsOpenWhenCommandApproverNodeUnconfigured guards
// #626's decided requirement 1 (unified fail-open rule): with no
// command_approver_node configured at all, even blocking mode must run the command,
// recorded distinctly as auto_approved_no_reviewer rather than a real
// approval.
func TestRunExecuteBashBlockingFailsOpenWhenCommandApproverNodeUnconfigured(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	fixture.commandApproverNode = ""
	fixture.nodes = nil

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", "printf unconfigured",
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v, want nil (fail open)", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
	decision := findExecutionDecisionPayload(t, fixture.sessionDir)
	if decision.Decision != commandApprovalDecisionAutoApprovedNoReviewer {
		t.Fatalf("decision = %q, want %q", decision.Decision, commandApprovalDecisionAutoApprovedNoReviewer)
	}
}

// TestRunExecuteBashBlockingFailsClosedWhenCommandApproverNodeUnresolvable
// prevents a configured-but-unresolvable reviewer from silently executing a
// blocking command (#680).
func TestRunExecuteBashBlockingFailsClosedWhenCommandApproverNodeUnresolvable(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	fixture.commandApproverNode = "typo-reviewer"
	fixture.nodes = map[string]config.NodeConfig{"orchestrator": {}}
	// A colliding live pane can use the same bare name in the requester session.
	// Static configuration must still prevent it receiving an approval request.
	fixture.discoveredNodes = map[string]discovery.NodeInfo{
		"test-session:typo-reviewer": {
			PaneID:      "%9",
			SessionName: "test-session",
			SessionDir:  fixture.sessionDir,
		},
	}

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", "printf unresolvable",
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want unresolved approver block")
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
	decision := findExecutionDecisionPayload(t, fixture.sessionDir)
	if decision.Decision != "blocked" {
		t.Fatalf("decision = %q, want blocked", decision.Decision)
	}
	if !strings.Contains(decision.Reason, "not resolvable") {
		t.Fatalf("decision reason = %q, want unresolved approver diagnostic", decision.Reason)
	}
	for _, event := range replayCommandEvents(t, fixture.sessionDir) {
		if event.Type == journal.CommandApprovalRequestedEventType {
			t.Fatalf("unexpected trusted pending approval event: %#v", event)
		}
	}
	postEntries, err := os.ReadDir(filepath.Join(fixture.sessionDir, "post"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("ReadDir(post) error = %v", err)
	}
	if len(postEntries) != 0 {
		t.Fatalf("post/ has %d entries, want no invalid-approver delivery", len(postEntries))
	}
}

func TestRunExecuteBashBlockingFailsClosedWhenCommandApproverNodeNotDiscovered(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	fixture.discoveredNodes = map[string]discovery.NodeInfo{}

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", "printf missing-discovery",
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want delivery failure block")
	}
	if !strings.Contains(err.Error(), `command_approver_node "orchestrator" not found among discovered nodes`) {
		t.Fatalf("error = %v, want missing discovered approver reason", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
	decision := findExecutionDecisionPayload(t, fixture.sessionDir)
	if decision.Decision != "blocked" {
		t.Fatalf("decision = %q, want blocked", decision.Decision)
	}
}

func TestRunExecuteBashBlockingRejectsExplicitThreadIDWithMismatchedDigest(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	policy := resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
		Category:  "release",
		TTL:       defaultCommandApprovalTTL,
	}

	// Approve the original command.
	originalCommand := "printf original-command"
	approvedThreadID := fixture.appendCommandApproval(t, policy, originalCommand, journal.ApprovalDecisionApproved, "orchestrator", fixture.now.Add(15*time.Minute))

	// Attempt to execute a different command using the approved thread ID.
	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--category", "release",
		"--thread-id", approvedThreadID,
		"--command", "printf attack-command",
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want digest_mismatch block")
	}
	if !strings.Contains(err.Error(), "different command digest") {
		t.Fatalf("error = %v, want reason containing \"different command digest\"", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0; command must not execute on digest mismatch", fixture.runCount)
	}
}

func TestRunExecuteBashPropagatesExitStatus(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	fixture.runStatus = 7

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "diagnostic",
		"--mode", "advisory",
		"--reviewer", "orchestrator",
		"--command", "exit 7",
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want exit status")
	}
	var exitErr commandExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want commandExitError", err, err)
	}
	if exitErr.ExitCode() != 7 {
		t.Fatalf("ExitCode() = %d, want 7", exitErr.ExitCode())
	}
	completed := findExecutionCompletedPayload(t, fixture.sessionDir)
	if completed.ExitStatus != 7 {
		t.Fatalf("completed exit status = %d, want 7", completed.ExitStatus)
	}
}

func TestRunExecuteBashRecordDecisionAndInspectCommandApprovals(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	policy := resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
		TTL:       defaultCommandApprovalTTL,
	}
	commandText := "printf approve-me"
	threadID := fixture.appendCommandApprovalRequest(t, policy, commandText, time.Now().Add(time.Hour))

	err := runExecuteBashWithContext(fixture.contextAsPane("orchestrator"), fixture.args(
		"--thread-id", threadID,
		"--record-decision", "approved",
		"--reason", "digest reviewed",
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext(record decision) error = %v", err)
	}

	configPath := fixture.writeConfigFile(t)
	stdout, _, err := captureCommandOutput(t, func() error {
		return RunInspectCommandApprovals([]string{
			"--config", configPath,
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
		})
	})
	if err != nil {
		t.Fatalf("RunInspectCommandApprovals() error = %v", err)
	}
	var output inspectCommandApprovalsOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("Unmarshal(inspect output): %v\n%s", err, stdout)
	}
	thread, ok := output.Threads[threadID]
	if !ok {
		t.Fatalf("inspect output missing thread %q: %#v", threadID, output.Threads)
	}
	if thread.Status != projection.CommandApprovalStatusApproved {
		t.Fatalf("thread status = %q, want approved", thread.Status)
	}
	if thread.DecidedAt == "" {
		t.Fatalf("thread missing decided_at: %#v", thread)
	}

	history, err := journal.ListCommandApprovalDecisionHistory(fixture.sessionDir)
	if err != nil {
		t.Fatalf("ListCommandApprovalDecisionHistory() error = %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("decision history entries = %d, want 1", len(history))
	}
	entry := history[0]
	if entry.ThreadID != threadID || entry.Decision != journal.ApprovalDecisionApproved || entry.EffectiveStatus != "approved" {
		t.Fatalf("decision history = %#v, want approved entry for thread %q", entry, threadID)
	}
	if entry.Requester != "worker" || entry.DecisionReviewer != "orchestrator" || entry.CommandApproverNode != "orchestrator" {
		t.Fatalf("decision history identities = %#v", entry)
	}
	if entry.Label != "protected" || entry.CommandHash == "" || entry.DecisionReason != "digest reviewed" {
		t.Fatalf("decision history command metadata = %#v", entry)
	}
	if entry.CommandText != "" {
		t.Fatalf("decision history stored command text by default: %#v", entry)
	}
}

func TestRunExecuteBashRecordRejectedDecisionWritesDecisionHistory(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	policy := resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
		TTL:       defaultCommandApprovalTTL,
	}
	threadID := fixture.appendCommandApprovalRequest(t, policy, "printf reject-me", time.Now().Add(time.Hour))

	err := runExecuteBashWithContext(fixture.contextAsPane("orchestrator"), fixture.args(
		"--thread-id", threadID,
		"--record-decision", "rejected",
		"--reason", "too broad for allowlist",
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext(record rejected decision) error = %v", err)
	}

	history, err := journal.ListCommandApprovalDecisionHistory(fixture.sessionDir)
	if err != nil {
		t.Fatalf("ListCommandApprovalDecisionHistory() error = %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("decision history entries = %d, want 1", len(history))
	}
	if history[0].Decision != journal.ApprovalDecisionRejected || history[0].EffectiveStatus != "rejected" {
		t.Fatalf("decision history = %#v, want rejected entry", history[0])
	}
	if history[0].DecisionReason != "too broad for allowlist" {
		t.Fatalf("decision reason = %q, want allowlist review reason", history[0].DecisionReason)
	}
}

func TestRunExecuteBashFailsOpenDoesNotWriteDecisionHistory(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	fixture.commandApproverNode = ""
	fixture.nodes = nil

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--command", "printf fail-open",
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v, want nil (fail open)", err)
	}

	history, err := journal.ListCommandApprovalDecisionHistory(fixture.sessionDir)
	if err != nil {
		t.Fatalf("ListCommandApprovalDecisionHistory() error = %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("decision history entries = %d, want 0 for auto_approved_no_reviewer: %#v", len(history), history)
	}
}

func TestRunExecuteBashDecisionHistoryCommandTextOptIn(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	commandText := "printf store-me"

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--store-command-text",
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("initial blocking command error = nil, want pending approval")
	}
	threadID := commandApprovalThreadID(resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
	}, commandDigest(commandText))

	err = runExecuteBashWithContext(fixture.contextAsPane("orchestrator"), fixture.args(
		"--thread-id", threadID,
		"--record-decision", "approved",
		"--reason", "safe exact command",
	))
	if err != nil {
		t.Fatalf("record decision error = %v", err)
	}

	history, err := journal.ListCommandApprovalDecisionHistory(fixture.sessionDir)
	if err != nil {
		t.Fatalf("ListCommandApprovalDecisionHistory() error = %v", err)
	}
	if len(history) != 1 {
		t.Fatalf("decision history entries = %d, want 1", len(history))
	}
	if history[0].CommandText != commandText {
		t.Fatalf("command_text = %q, want opt-in command text %q", history[0].CommandText, commandText)
	}
}

func TestRunExecuteBashRecordDecisionWarnsWhenDecisionHistorySyncFailsAfterAppend(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	policy := resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
		TTL:       defaultCommandApprovalTTL,
	}
	threadID := fixture.appendCommandApprovalRequest(t, policy, "printf approve-with-history-sync-failure", time.Now().Add(time.Hour))
	if err := os.WriteFile(journal.CommandApprovalDecisionHistoryDir(fixture.sessionDir), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile(history path as file) error = %v", err)
	}

	err := runExecuteBashWithContext(fixture.contextAsPane("orchestrator"), fixture.args(
		"--thread-id", threadID,
		"--record-decision", "approved",
		"--reason", "authoritative decision survives derived sync failure",
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext(record decision) error = %v, want nil after authoritative append: stderr=%s", err, fixture.stderr.String())
	}
	if !strings.Contains(fixture.stderr.String(), "command approval decision history sync failed after recording decision") {
		t.Fatalf("stderr = %q, want decision history sync warning", fixture.stderr.String())
	}

	state, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil {
		t.Fatalf("ProjectCommandApprovalState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectCommandApprovalState() ok = false, want true")
	}
	if got := state.Threads[threadID].Status; got != projection.CommandApprovalStatusApproved {
		t.Fatalf("thread status = %q, want approved", got)
	}
}

func (f *executeBashFixture) appendCommandApproval(t *testing.T, policy resolvedCommandApprovalPolicy, commandText string, decision journal.ApprovalDecision, decisionReviewer string, expiresAt time.Time) string {
	t.Helper()

	threadID := f.appendCommandApprovalRequest(t, policy, commandText, expiresAt)
	f.appendCommandApprovalDecisionForRequest(t, threadID, decisionReviewer, decision)
	return threadID
}

func (f *executeBashFixture) appendCommandApprovalRequest(t *testing.T, policy resolvedCommandApprovalPolicy, commandText string, expiresAt time.Time) string {
	t.Helper()

	writer := f.openWriter(t)
	commandHash := commandDigest(commandText)
	threadID := commandApprovalThreadID(policy, commandHash)
	// #626 B1: CommandApproverNode mirrors the fixture's own commandApproverNode, exactly
	// as recordCommandApprovalRequest always populates it from the
	// config-resolved node in production — this is the field decisions are
	// actually validated against now, never the plain Reviewer label.
	commandApproverAddress := ""
	if f.commandApproverNode != "" {
		commandApproverAddress = nodeaddr.Full(f.commandApproverNode, f.sessionName)
	}
	_, err := writer.AppendEventWithOptions(
		journal.CommandApprovalRequestedEventType,
		journal.VisibilityOperatorVisible,
		journal.CommandApprovalRequestPayload{
			Requester:              policy.Requester,
			RequesterAddress:       nodeaddr.Full(policy.Requester, f.sessionName),
			Reviewer:               policy.Reviewer,
			CommandApproverNode:    f.commandApproverNode,
			CommandApproverAddress: commandApproverAddress,
			Mode:                   policy.Mode,
			Label:                  policy.Label,
			Category:               policy.Category,
			CommandHash:            commandHash,
			InputRequestID:         "ireq_" + strings.TrimPrefix(threadID, "command-approval-"),
			Reason:                 "review requested",
			ExpiresAt:              expiresAt.UTC().Format(time.RFC3339Nano),
		},
		journal.AppendOptions{ThreadID: threadID},
		f.now,
	)
	if err != nil {
		t.Fatalf("AppendEventWithOptions(request): %v", err)
	}
	return threadID
}

func (f *executeBashFixture) appendCommandApprovalDecisionForRequest(t *testing.T, threadID, reviewer string, decision journal.ApprovalDecision) {
	t.Helper()

	state, ok, err := projection.ProjectCommandApprovalState(f.sessionDir, f.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() = (%#v, %v, %v), want request state before decision", state, ok, err)
	}
	thread, ok := state.Threads[threadID]
	if !ok {
		t.Fatalf("missing thread %q before decision", threadID)
	}
	writer := f.openWriter(t)
	_, err = writer.AppendEventWithOptions(
		journal.CommandApprovalDecidedEventType,
		journal.VisibilityOperatorVisible,
		journal.CommandApprovalDecisionPayload{
			Reviewer:         reviewer,
			ReviewerAddress:  nodeaddr.Full(reviewer, f.sessionName),
			RequesterAddress: thread.RequesterAddress,
			Decision:         decision,
			Reason:           "reviewed",
			InputRequestID:   thread.InputRequestID,
			CommandHash:      thread.CommandHash,
		},
		journal.AppendOptions{ThreadID: threadID},
		f.now,
	)
	if err != nil {
		t.Fatalf("AppendEventWithOptions(decision): %v", err)
	}
}

func (f *executeBashFixture) appendCommandApprovalDecisionOnly(t *testing.T, threadID, reviewer string, decision journal.ApprovalDecision) {
	t.Helper()

	writer := f.openWriter(t)
	_, err := writer.AppendEventWithOptions(
		journal.CommandApprovalDecidedEventType,
		journal.VisibilityOperatorVisible,
		journal.CommandApprovalDecisionPayload{
			Reviewer: reviewer,
			Decision: decision,
			Reason:   "reviewed",
		},
		journal.AppendOptions{ThreadID: threadID},
		f.now,
	)
	if err != nil {
		t.Fatalf("AppendEventWithOptions(decision): %v", err)
	}
}

func (f *executeBashFixture) openWriter(t *testing.T) *journal.Writer {
	t.Helper()

	writer, err := journal.OpenCurrentWriter(f.sessionDir)
	if err == nil {
		return writer
	}
	writer, err = journal.OpenShadowWriter(f.sessionDir, f.contextID, f.sessionName, os.Getpid(), f.now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	return writer
}

func (f *executeBashFixture) writeConfigFile(t *testing.T) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "postman.toml")
	content := fmt.Sprintf("[postman]\nbase_dir = %q\nedges = [\"worker --- orchestrator\"]\n", f.baseDir)
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(config): %v", err)
	}
	return configPath
}

func replayCommandEvents(t *testing.T, sessionDir string) []journal.Event {
	t.Helper()

	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	var commandEvents []journal.Event
	for _, event := range events {
		if strings.HasPrefix(event.Type, "command_") {
			commandEvents = append(commandEvents, event)
		}
	}
	return commandEvents
}

func onlyApprovalThread(t *testing.T, sessionDir string, now time.Time) (string, projection.CommandApprovalThread) {
	t.Helper()

	state, ok, err := projection.ProjectCommandApprovalState(sessionDir, now)
	if err != nil {
		t.Fatalf("ProjectCommandApprovalState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectCommandApprovalState() ok = false, want true")
	}
	if len(state.Threads) != 1 {
		t.Fatalf("threads = %#v, want exactly one", state.Threads)
	}
	for threadID, thread := range state.Threads {
		return threadID, thread
	}
	t.Fatal("missing only approval thread")
	return "", projection.CommandApprovalThread{}
}

func assertApprovalLifecycle(t *testing.T, sessionDir string, now time.Time, threadID string, wantStatus projection.CommandApprovalStatus, wantHistory int, wantReason string) {
	t.Helper()

	state, ok, err := projection.ProjectCommandApprovalState(sessionDir, now)
	if err != nil {
		t.Fatalf("ProjectCommandApprovalState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectCommandApprovalState() ok = false, want true")
	}
	thread, ok := state.Threads[threadID]
	if !ok {
		t.Fatalf("missing thread %q in %#v", threadID, state.Threads)
	}
	if thread.Status != wantStatus {
		t.Fatalf("thread status = %q, want %q", thread.Status, wantStatus)
	}
	history, err := journal.ListCommandApprovalDecisionHistory(sessionDir)
	if err != nil {
		t.Fatalf("ListCommandApprovalDecisionHistory() error = %v", err)
	}
	if len(history) != wantHistory {
		t.Fatalf("decision history length = %d, want %d: %#v", len(history), wantHistory, history)
	}
	if wantHistory == 0 {
		return
	}
	entry := history[0]
	if entry.ThreadID != threadID || entry.Decision != journal.ApprovalDecisionRejected || entry.EffectiveStatus != "rejected" {
		t.Fatalf("history decision fields = %#v, want rejected %s", entry, threadID)
	}
	if entry.Requester != "worker" || entry.Reviewer != "orchestrator" || entry.CommandApproverNode != "approver" || entry.DecisionReviewer != "approver" {
		t.Fatalf("history provenance = %#v, want worker/orchestrator/approver", entry)
	}
	if entry.CommandHash != thread.CommandHash || entry.DecisionReason != wantReason {
		t.Fatalf("history correlation/reason = %#v, want hash %q reason %q", entry, thread.CommandHash, wantReason)
	}
}

func assertApprovalReplySlot(t *testing.T, sessionDir, sessionName, inputRequestID string, wantOpen bool) {
	t.Helper()

	state, ok, err := projection.ProjectMessageInputRequestStateAt(sessionDir, sessionName, time.Date(2026, time.June, 1, 10, 30, 0, 0, time.UTC), projection.DefaultInputRequestStaleAfterSeconds)
	if err != nil {
		t.Fatalf("ProjectMessageInputRequestStateAt() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectMessageInputRequestStateAt() ok = false, want true")
	}
	foundInbound := false
	for _, input := range state.InputRequired {
		if input.InputRequestID == inputRequestID {
			foundInbound = true
			break
		}
	}
	foundOutbound := false
	for _, input := range state.WaitingOnInput {
		if input.InputRequestID == inputRequestID {
			foundOutbound = true
			break
		}
	}
	if foundInbound != wantOpen || foundOutbound != wantOpen {
		t.Fatalf("input request %q open inbound/outbound = %v/%v, want %v/%v; inbound=%#v outbound=%#v", inputRequestID, foundInbound, foundOutbound, wantOpen, wantOpen, state.InputRequired, state.WaitingOnInput)
	}
	if got := state.InputRequiredCounts["approver"]; (got > 0) != wantOpen {
		t.Fatalf("InputRequiredCounts[approver] = %d, want open=%v; counts=%#v", got, wantOpen, state.InputRequiredCounts)
	}
	if got := state.WaitingOnInputCounts["worker"]; (got > 0) != wantOpen {
		t.Fatalf("WaitingOnInputCounts[worker] = %d, want open=%v; counts=%#v", got, wantOpen, state.WaitingOnInputCounts)
	}
}

func deliverLifecyclePost(t *testing.T, sessionDir, filename, sessionName string, nodes map[string]discovery.NodeInfo, adjacency map[string][]string, enabled func(string) bool, content string) {
	t.Helper()

	path := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", filename, err)
	}
	if err := message.DeliverMessage(path, "ctx-484", nodes, adjacency, &config.Config{}, enabled, nil, idle.NewIdleTracker(), ""); err != nil {
		t.Fatalf("DeliverMessage(%s) error = %v", filename, err)
	}
}

func lifecycleEnvelope(contextID, from, to, threadID, fillID, commandHash, body string) string {
	var b strings.Builder
	b.WriteString("---\nparams:\n")
	b.WriteString("  contextId: " + contextID + "\n")
	b.WriteString("  from: " + from + "\n")
	b.WriteString("  to: " + to + "\n")
	if threadID != "" {
		b.WriteString("  thread_id: " + threadID + "\n")
	}
	if fillID != "" {
		b.WriteString("  fills_input_request_id: " + fillID + "\n")
	}
	if commandHash != "" {
		b.WriteString("  command_hash: " + commandHash + "\n")
	}
	b.WriteString("  timestamp: 2026-06-01T10:00:00Z\n---\n\n")
	b.WriteString(body)
	b.WriteByte('\n')
	return b.String()
}

func listDirNames(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("ReadDir(%s) error = %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func readFileString(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(content)
}

func walkRelativeFiles(t *testing.T, root string) []string {
	t.Helper()

	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%s) error = %v", root, err)
	}
	return files
}

func summarizeJournalEvents(t *testing.T, sessionDir string) []string {
	t.Helper()

	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("Replay(%s) error = %v", sessionDir, err)
	}
	summaries := make([]string, 0, len(events))
	for _, event := range events {
		summaries = append(summaries, fmt.Sprintf("%d:%s:%s:%s:%s", event.Sequence, event.Type, event.Visibility, event.SessionKey, string(event.Payload)))
	}
	return summaries
}

func assertLifecycleDeadLetters(t *testing.T, sessionDir, suffix string, want int) {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(sessionDir, "dead-letter", "*"+suffix+".md"))
	if err != nil {
		t.Fatalf("Glob(dead-letter) error = %v", err)
	}
	if len(matches) != want {
		t.Fatalf("dead letters with suffix %s = %d (%v), want %d", suffix, len(matches), matches, want)
	}
}

func findExecutionDecisionPayload(t *testing.T, sessionDir string) journal.CommandExecutionDecisionPayload {
	t.Helper()

	for _, event := range replayCommandEvents(t, sessionDir) {
		if event.Type != journal.CommandExecutionDecidedEventType {
			continue
		}
		var payload journal.CommandExecutionDecisionPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("Unmarshal(execution decision): %v", err)
		}
		return payload
	}
	t.Fatal("missing command execution decision event")
	return journal.CommandExecutionDecisionPayload{}
}

func findExecutionCompletedPayload(t *testing.T, sessionDir string) journal.CommandExecutionCompletedPayload {
	t.Helper()

	for _, event := range replayCommandEvents(t, sessionDir) {
		if event.Type != journal.CommandExecutionCompletedEventType {
			continue
		}
		var payload journal.CommandExecutionCompletedPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("Unmarshal(execution completed): %v", err)
		}
		return payload
	}
	t.Fatal("missing command execution completed event")
	return journal.CommandExecutionCompletedPayload{}
}
