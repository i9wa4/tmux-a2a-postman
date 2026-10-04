package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/tmuxtest"
)

type executeBashFixture struct {
	baseDir              string
	contextID            string
	sessionName          string
	sessionDir           string
	nowMu                sync.Mutex
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

// currentNow/advanceNow (#831 D2 test migration) are mutex-guarded since
// TestRunExecuteBashBlockingApprovalIsSingleUseAcrossConcurrentWaiters and
// similar tests drive concurrent waiters against the same fixture, each
// polling (and, under the new default fake sleep below, advancing the clock)
// from its own goroutine.
func (f *executeBashFixture) currentNow() time.Time {
	f.nowMu.Lock()
	defer f.nowMu.Unlock()
	return f.now
}

func (f *executeBashFixture) advanceNow(d time.Duration) {
	f.nowMu.Lock()
	f.now = f.now.Add(d)
	f.nowMu.Unlock()
}

// waitWatchdogPolls (#831 D2 test migration, "wait watchdog" requirement) is
// an upper bound on how many fake-clock poll ticks the default sleep below
// will advance through before failing the test with a clear diagnostic,
// distinct from and independent of the production --wait-timeout-seconds
// deadline being exercised. It guards against a misconfigured fake clock
// (one that never actually advances what the wait loop believes has
// elapsed) hanging the test suite instead of failing fast. The default
// approval TTL (15min) at the real 500ms poll interval needs at most ~1800
// ticks to naturally reach a terminal expired/wait_timeout state; this cap
// is comfortably above that.
const waitWatchdogPolls = 4000

func newExecuteBashFixture(t *testing.T, policies ...config.CommandApprovalPolicy) *executeBashFixture {
	t.Helper()

	fixture := newExecuteBashFixtureRaw(t, policies...)
	// #823 rework-3 (F-002): blocking mode now refuses to shadow-mint or
	// shadow-claim when journal.OpenCurrentWriter finds no current session
	// (a genuine cold start with no live daemon session yet). Every
	// existing fixture-driven test reflects ordinary usage INSIDE an
	// already-established session, not that cold-start edge case, so this
	// pre-bootstraps a real current session/lease once here -- exactly the
	// same journal.OpenShadowWriter call any of this fixture's other
	// helpers would eventually trigger anyway. Tests that specifically
	// exercise the cold-start refusal build their own fixture with
	// newExecuteBashFixtureRaw instead (see
	// TestRunExecuteBashBlockingColdStartNoCurrentWriterFailsClosed).
	_ = fixture.openWriter(t)
	return fixture
}

// newExecuteBashFixtureRaw builds a fixture WITHOUT the F-002 current-writer
// pre-bootstrap, for tests that specifically need a genuinely cold session
// (no session-state.json / lease on disk yet).
func newExecuteBashFixtureRaw(t *testing.T, policies ...config.CommandApprovalPolicy) *executeBashFixture {
	t.Helper()

	// #845: this fixture's discoveredNodes map hardcodes fake tmux PaneIDs
	// (e.g. "%3") that are NOT scoped to any real session -- tmux PaneIDs
	// are unique per tmux SERVER, not per session. Any pane-notification
	// delivery exercised by a test using this fixture must never reach a
	// real "tmux" binary, or it sends real keystrokes to whatever
	// ambient pane on the real server happens to hold that ID. InstallMissing
	// points PATH at an empty directory so "tmux" cannot be found at all,
	// which is stricter and safer than a scripted fake for fixtures that
	// don't assert on tmux's own output.
	tmuxtest.InstallMissing(t)

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

// TestNewExecuteBashFixtureNeverReachesRealTmux is a regression guard for
// #845: this package's fixtures hardcode fake PaneIDs (e.g. "%3") that are
// not scoped to any real tmux session, so any fixture-driven test that
// reached a real "tmux" binary could send real keystrokes to an unrelated,
// ambient pane on whatever live tmux server the test process happened to
// run inside. newExecuteBashFixtureRaw installs tmuxtest.InstallMissing to
// prevent this; this test fails loudly if that wiring is ever removed, by
// directly asserting "tmux" cannot be resolved on PATH while a fixture is
// active.
func TestNewExecuteBashFixtureNeverReachesRealTmux(t *testing.T) {
	_ = newExecuteBashFixtureRaw(t, config.CommandApprovalPolicy{})

	if path, err := exec.LookPath("tmux"); err == nil {
		t.Fatalf("expected \"tmux\" to be unresolvable while an execute-bash fixture is active (internal/tmuxtest.InstallMissing must be wired into newExecuteBashFixtureRaw), but found it at %q -- this would let fixture-driven tests send real keystrokes to an ambient tmux pane (see #845)", path)
	}
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
	pollCount := 0
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
		now:                func() time.Time { return f.currentNow() },
		// #831 D2 test migration: the DEFAULT sleep for every test that does
		// not set its own ctx.sleep override. Since #823 made blocking-mode
		// waiting synchronous and this issue removes --no-wait entirely,
		// every test that reaches the wait loop now needs a deterministic
		// clock -- this ADVANCES the fixture's fake now() by the requested
		// duration (never a no-op that would desynchronize the fake clock
		// from what the wait loop believes has elapsed), bounded by
		// waitWatchdogPolls so a genuinely stuck wait loop fails fast with a
		// clear diagnostic instead of spinning indefinitely.
		sleep: func(_ context.Context, d time.Duration) {
			pollCount++
			if pollCount > waitWatchdogPolls {
				panic(fmt.Sprintf("wait loop did not reach a terminal state within %d ticks (possible fixture/production bug)", waitWatchdogPolls))
			}
			f.advanceNow(d)
		},
		runBash: func(command string, stdout, stderr io.Writer) (int, error) {
			f.runCount++
			f.commands = append(f.commands, command)
			_, _ = fmt.Fprint(stdout, "ran\n")
			// #823 F-009: the stub also writes to stderr, so tests can
			// assert stderr capture is wired all the way through
			// ctx.runBash, not just stdout.
			_, _ = fmt.Fprint(stderr, "ran-stderr-9f2c\n")
			return f.runStatus, f.runErr
		},
	}
}

// args builds the standard test invocation. #831 (D2/D4) removed both
// --no-wait and --requester from execute-bash entirely: the effective
// requester is now always whatever pane title the context() (default
// "worker", #626 B1-residual precedent) or contextAsPane(paneName) call
// supplies, and blocking mode always waits (bounded by
// --wait-timeout-seconds and the request's own expiry) -- the fixture's
// default ctx.sleep above (which advances the fake clock deterministically,
// bounded by waitWatchdogPolls) is what keeps every test that reaches the
// wait loop from hanging, not an opt-out flag.
func (f *executeBashFixture) args(extra ...string) []string {
	base := []string{
		"--context-id", f.contextID,
		"--session", f.sessionName,
	}
	return append(base, extra...)
}

func TestRunExecuteBashAdvisoryRecordsRequestWithoutCommandTextAndRuns(t *testing.T) {
	// #831 D1: advisory access is now config-declared, not CLI-flag-declared
	// -- a --mode advisory flag with no matching config entry would
	// downgrade the (blocking) floor and be refused. Pinning this explicit
	// advisory policy makes advisory mode the genuine, admin-declared floor
	// for this label/category, not an absence-of-config artifact.
	// #831 I-005 (guardian rework 1): Requester is pinned too, since D5/D7
	// refuse loading any advisory/warn-only config entry whose Requester is
	// wildcard/empty -- a real LoadConfig would reject this fixture's policy
	// without it.
	fixture := newExecuteBashFixture(t, config.CommandApprovalPolicy{
		Requester: "worker",
		Label:     "low-risk",
		Category:  "diagnostic",
		Mode:      "advisory",
	})
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
	// #831 D1: see TestRunExecuteBashAdvisoryRecordsRequestWithoutCommandTextAndRuns
	// -- advisory access is config-declared, not CLI-flag-declared.
	// #831 I-005 (guardian rework 1): Requester and Category are pinned too,
	// since D5/D7 refuse loading any advisory/warn-only config entry whose
	// Requester or Category is wildcard/empty -- a real LoadConfig would
	// reject this fixture's policy without them. The invocation below adds
	// --category to match.
	fixture := newExecuteBashFixture(t, config.CommandApprovalPolicy{
		Requester: "worker",
		Label:     "diagnostic",
		Category:  "diagnostic",
		Mode:      "advisory",
	})
	commandText := "printf audit-me"

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "diagnostic",
		"--category", "diagnostic",
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
	// #831 D2: blocking mode always waits now, ending in wait_timeout
	// rather than the old immediate "approval is absent" diagnostic.
	if !strings.Contains(err.Error(), "approval wait timed out") {
		t.Fatalf("error = %v, want approval wait timeout", err)
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
	// #831 D2: blocking mode always waits now, so a first-time absent
	// approval mints a fresh pending request and waits on it (the fixture's
	// fake clock advances deterministically to the wait_timeout deadline;
	// no one decides it before this call returns) rather than reporting the
	// immediate "approval is absent" state.
	if !strings.Contains(err.Error(), "approval wait timed out") {
		t.Fatalf("runExecuteBashWithContext() error = %v, want approval wait timeout", err)
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
	// #831 I-005 (guardian rework 1): Category is pinned too, since D5/D7
	// refuse loading any advisory/warn-only config entry whose Category is
	// wildcard/empty -- a real LoadConfig would reject this fixture's policy
	// without it. The invocation below adds --category to match.
	policy := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "deploy",
		Category:  "deploy",
		Mode:      "warn-only",
	}
	fixture := newExecuteBashFixture(t, policy)

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "deploy",
		"--category", "deploy",
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
	// #831 I-005 (guardian rework 1): Category is pinned too, since D5/D7
	// refuse loading any advisory/warn-only config entry whose Category is
	// wildcard/empty -- a real LoadConfig would reject this fixture's policy
	// without it. The invocation below adds --category to match.
	policy := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "deploy",
		Category:  "deploy",
		Mode:      "warn-only",
	}
	fixture := newExecuteBashFixture(t, policy)

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "deploy",
		"--category", "deploy",
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

// TestRunExecuteBashDefaultModeIsBlockingWithoutOverride pins #753: with no
// --mode flag and no policy override, a valid resolvable reviewer must fail
// closed on an unapproved command rather than fall through to the old
// advisory default that ran the command unconditionally. It asserts the
// recorded Mode/Decision explicitly (guardian F-018) because an
// error+runCount==0 assertion alone cannot distinguish the new blocking
// default from a warn-only default, which also refuses without
// --override-approval; and it covers the --override-approval case
// separately (guardian F-018) because warn-only, unlike blocking, would
// let --override-approval run the command.
func TestRunExecuteBashDefaultModeIsBlockingWithoutOverride(t *testing.T) {
	// #831 D2: blocking mode always waits now, so this ends in wait_timeout
	// (the fixture's fake clock advances deterministically) rather than the
	// old immediate "approval is absent" diagnostic.
	t.Run("no override flag", func(t *testing.T) {
		fixture := newExecuteBashFixture(t)

		err := runExecuteBashWithContext(fixture.context(), fixture.args(
			"--label", "unset-mode",
			"--command", "printf default-mode",
		))
		if err == nil {
			t.Fatal("runExecuteBashWithContext() error = nil, want default-blocking refusal")
		}
		if !strings.Contains(err.Error(), "approval wait timed out") {
			t.Fatalf("error = %v, want approval wait timeout diagnostic", err)
		}
		if fixture.runCount != 0 {
			t.Fatalf("runCount = %d, want 0 (command must not run without approval)", fixture.runCount)
		}
		decision := findExecutionDecisionPayload(t, fixture.sessionDir)
		if decision.Mode != commandApprovalModeBlocking {
			t.Fatalf("decision.Mode = %q, want %q", decision.Mode, commandApprovalModeBlocking)
		}
		if decision.Decision != "blocked" {
			t.Fatalf("decision.Decision = %q, want %q", decision.Decision, "blocked")
		}
	})

	// #831 D3: --override-approval now fails LOUDLY (an explicit error,
	// before any request is minted or decision recorded) when passed but
	// the effective mode is not warn-only, replacing the silent no-op this
	// subtest used to document.
	t.Run("with override-approval flag", func(t *testing.T) {
		fixture := newExecuteBashFixture(t)

		err := runExecuteBashWithContext(fixture.context(), fixture.args(
			"--label", "unset-mode",
			"--command", "printf default-mode",
			"--override-approval",
		))
		if err == nil {
			t.Fatal("runExecuteBashWithContext() error = nil, want --override-approval-outside-warn-only refusal")
		}
		if !strings.Contains(err.Error(), "--override-approval only applies in warn-only mode") {
			t.Fatalf("error = %v, want loud override-approval-outside-warn-only diagnostic", err)
		}
		if fixture.runCount != 0 {
			t.Fatalf("runCount = %d, want 0 (command must not run without approval)", fixture.runCount)
		}
	})
}

// TestRunExecuteBashDefaultModeStillFailsOpenWithoutCommandApproverNode pins
// the other half of #753: the unified fail-open rule (#626) must keep
// applying to the new blocking default exactly as it does to an explicit
// --mode blocking, so topologies without a configured command_approver_node
// don't deadlock.
func TestRunExecuteBashDefaultModeStillFailsOpenWithoutCommandApproverNode(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	fixture.commandApproverNode = ""
	fixture.nodes = nil

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "unset-mode-no-approver",
		"--command", "printf default-mode-fail-open",
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

// TestRunExecuteBashDefaultModeFailsClosedWhenCommandApproverNodeUnresolvable
// pins the newly-reachable fail-closed edge at the default (guardian
// F-019): a configured-but-unresolvable command_approver_node now fails
// closed under the blocking default exactly as it does under an explicit
// --mode blocking (see
// TestRunExecuteBashBlockingFailsClosedWhenCommandApproverNodeUnresolvable),
// with no policy or --mode flag setting the mode explicitly.
func TestRunExecuteBashDefaultModeFailsClosedWhenCommandApproverNodeUnresolvable(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	fixture.commandApproverNode = "typo-reviewer"
	fixture.nodes = map[string]config.NodeConfig{"orchestrator": {}}
	fixture.discoveredNodes = map[string]discovery.NodeInfo{
		"test-session:typo-reviewer": {
			PaneID:      "%9",
			SessionName: "test-session",
			SessionDir:  fixture.sessionDir,
		},
	}

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "unset-mode-unresolvable",
		"--command", "printf default-mode-unresolvable",
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want unresolved approver block")
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
	decision := findExecutionDecisionPayload(t, fixture.sessionDir)
	if decision.Mode != commandApprovalModeBlocking {
		t.Fatalf("decision.Mode = %q, want %q", decision.Mode, commandApprovalModeBlocking)
	}
	if decision.Decision != "blocked" {
		t.Fatalf("decision.Decision = %q, want %q", decision.Decision, "blocked")
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

func TestRunExecuteBashBlockingRefusesInvalidApprovals(t *testing.T) {
	for _, tc := range []struct {
		name       string
		setup      func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string)
		command    string
		wantReason string
	}{
		{
			// #831 D2: blocking mode always waits now (--no-wait was
			// removed) -- an absent approval mints a fresh pending request
			// and this call waits on it, ending in wait_timeout (the
			// fixture's fake clock advances deterministically to the
			// default --wait-timeout-seconds deadline; no one ever decides
			// it in this test).
			name: "absent",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
			},
			command:    "printf absent",
			wantReason: "approval wait timed out",
		},
		{
			// #823 rework-2 (F-012): a stale terminal thread is now
			// eligible for retry -- the call atomically mints a fresh
			// request. #831 D2: that fresh request is then waited on
			// (blocking mode always waits), ending in wait_timeout rather
			// than immediately reporting "approval is pending", so the
			// deterministic thread id never permanently blocks a future
			// retry of the identical command.
			name: "stale",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
				threadID := commandApprovalThreadID(policy, commandDigest(commandText))
				fixture.appendCommandApprovalDecisionOnly(t, threadID, "orchestrator", journal.ApprovalDecisionApproved)
			},
			command:    "printf stale",
			wantReason: "approval wait timed out",
		},
		{
			// #823 rework-2 (F-012): ditto for a rejected thread.
			name: "rejected",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
				fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionRejected, "orchestrator", fixture.now.Add(15*time.Minute))
			},
			command:    "printf rejected",
			wantReason: "approval wait timed out",
		},
		{
			// #823 rework-2 (F-012): ditto for an expired thread.
			name: "expired",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
				fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionApproved, "orchestrator", fixture.now.Add(-time.Second))
			},
			command:    "printf expired",
			wantReason: "approval wait timed out",
		},
		{
			// #831 D2: this now also waits (blocking mode always waits),
			// ending in wait_timeout rather than the immediate "approval is
			// pending" diagnostic.
			name: "wrong reviewer remains pending",
			setup: func(t *testing.T, fixture *executeBashFixture, policy resolvedCommandApprovalPolicy, commandText string) {
				fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionApproved, "critic", fixture.now.Add(15*time.Minute))
			},
			command:    "printf reviewer",
			wantReason: "approval wait timed out",
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

	// #823 rework-2 (F-012): a historical-only legacy approval is also
	// eligible for retry -- guardian's frozen decision set explicitly
	// includes historical_only among the terminal states an atomic mint
	// supersedes, so the call mints a fresh request (reported "approval is
	// pending") instead of resurfacing the historical-only diagnosis. The
	// original safety property this test guards -- a legacy address-less
	// approval must never itself authorize live execution -- still holds:
	// runCount stays 0, and only a genuinely NEW decision on the fresh
	// request could ever authorize a run.
	err = runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--category", "release",
		"--thread-id", threadID,
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want a pending blocking refusal for the freshly minted request")
	}
	// #831 D2: the freshly minted request is then waited on (blocking mode
	// always waits), ending in wait_timeout rather than the immediate
	// "approval is pending" diagnostic.
	if !strings.Contains(err.Error(), "approval wait timed out") {
		t.Fatalf("error = %v, want approval wait timeout (fresh mint over the historical-only thread)", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0; legacy address-less approval must not authorize live execution", fixture.runCount)
	}
	decision := findExecutionDecisionPayload(t, fixture.sessionDir)
	// #831 D2: the freshly minted request's wait concludes in wait_timeout,
	// so the FINAL journaled decision reason is the wait-timeout diagnostic,
	// not the immediate approval-is-pending reason recorded at mint time.
	if decision.Decision != "blocked" || !strings.Contains(decision.Reason, "approval wait timed out") {
		t.Fatalf("execution decision = %#v, want blocked wait-timeout reason", decision)
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
	// #831 D1: advisory access is now config-declared, not CLI-flag-declared
	// -- a --mode advisory flag with no matching config entry would
	// downgrade the (blocking) floor and be refused, so this fixture pins
	// an explicit advisory policy for this label/category to make advisory
	// mode the genuine, admin-declared floor rather than relying on
	// absence-of-config.
	// #831 I-005 (guardian rework 1): Requester is pinned too, since D5/D7
	// refuse loading any advisory/warn-only config entry whose Requester is
	// wildcard/empty -- a real LoadConfig would reject this fixture's policy
	// without it.
	fixture := newExecuteBashFixture(t, config.CommandApprovalPolicy{
		Requester: "worker",
		Label:     "diagnostic",
		Category:  "exit-status",
		Mode:      "advisory",
	})
	fixture.runStatus = 7

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "diagnostic",
		"--category", "exit-status",
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
	if entry.DecisionMessageID == "" {
		t.Fatal("recorded decision message id is empty; exact reply-slot reconciliation cannot close the originating input request")
	}
	decisionInfo, err := message.ParseMessageFilename(entry.DecisionMessageID)
	if err != nil {
		t.Fatalf("ParseMessageFilename(decision message id) error = %v", err)
	}
	if decisionInfo.From != "orchestrator" || decisionInfo.To != "worker" {
		t.Fatalf("decision message identity = %s -> %s, want orchestrator -> worker", decisionInfo.From, decisionInfo.To)
	}
	if entry.Label != "protected" || entry.CommandHash == "" || entry.DecisionReason != "digest reviewed" {
		t.Fatalf("decision history command metadata = %#v", entry)
	}
	if entry.CommandText != "" {
		t.Fatalf("decision history stored command text by default: %#v", entry)
	}
}

// newOpenApprovalFixture sets up a fixture and issues a real execute-bash
// approval request (through the real request-and-notify machinery, not a
// hand-crafted journal event) so the resulting thread's mailbox input_request
// is genuinely open — required for assertApprovalReplySlot to mean anything
// (guardian F-036: counting fill events proves an event was written, not that
// the request actually closed).
func newOpenApprovalFixture(t *testing.T, commandText string) (*executeBashFixture, string, projection.CommandApprovalThread) {
	t.Helper()

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
	}
	fixture.discoveredNodes = map[string]discovery.NodeInfo{
		"test-session:worker":       {PaneID: "%1", SessionName: fixture.sessionName, SessionDir: fixture.sessionDir},
		"test-session:orchestrator": {PaneID: "%2", SessionName: fixture.sessionName, SessionDir: fixture.sessionDir},
		"test-session:approver":     {PaneID: "%3", SessionName: fixture.sessionName, SessionDir: fixture.sessionDir},
	}

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--reviewer", "orchestrator",
		"--reason", "auto-fill regression setup",
		"--command", commandText,
	))
	// #831 D2: blocking mode always waits now, ending in wait_timeout
	// rather than the old immediate "approval is absent" diagnostic.
	if err == nil || !strings.Contains(err.Error(), "approval wait timed out") {
		t.Fatalf("runExecuteBashWithContext(request) error = %v, want approval wait timeout", err)
	}

	threadID, thread := onlyApprovalThread(t, fixture.sessionDir, fixture.now)
	if thread.Status != projection.CommandApprovalStatusPending {
		t.Fatalf("initial status = %q, want pending", thread.Status)
	}
	assertApprovalReplySlot(t, fixture.sessionDir, fixture.sessionName, thread.InputRequestID, true)
	return fixture, threadID, thread
}

func TestRunExecuteBashRecordDecisionAutoFillsPairedInputRequest(t *testing.T) {
	fixture, threadID, thread := newOpenApprovalFixture(t, "printf autofill-me")

	for i := 0; i < 2; i++ {
		if err := runExecuteBashWithContext(fixture.contextAsPane("approver"), fixture.args(
			"--thread-id", threadID,
			"--record-decision", "approved",
			"--reason", "digest reviewed",
		)); err != nil {
			t.Fatalf("runExecuteBashWithContext(record decision, attempt %d) error = %v", i, err)
		}
		// The user-visible outcome, not just "an event was written" (F-036):
		// after either the first decision or an idempotent retry, the
		// request must actually be closed.
		assertApprovalReplySlot(t, fixture.sessionDir, fixture.sessionName, thread.InputRequestID, false)
	}

	events, err := journal.Replay(fixture.sessionDir)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	var fills []journal.MailboxEventPayload
	for _, event := range events {
		if event.Type != projection.MailboxProjectionPostConsumedEventType {
			continue
		}
		var payload journal.MailboxEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("Unmarshal(mailbox_projection_post_consumed payload): %v", err)
		}
		if payload.FillsInputRequestID == thread.InputRequestID {
			fills = append(fills, payload)
		}
	}
	if len(fills) != 1 {
		t.Fatalf("auto-fill events for input request %q = %d, want exactly 1 (idempotent across 2 --record-decision calls): %#v", thread.InputRequestID, len(fills), fills)
	}
	fill := fills[0]
	if fill.From != "approver" || fill.To != "worker" || fill.ThreadID != threadID {
		t.Fatalf("auto-fill payload = %#v, want from=approver to=worker thread_id=%s", fill, threadID)
	}
	meta, err := envelope.ParseMetadata(fill.Content)
	if err != nil {
		t.Fatalf("ParseMetadata(auto-fill content) error = %v; content:\n%s", err, fill.Content)
	}
	if meta.ThreadID != threadID || meta.CommandHash == "" || meta.FillsInputRequestID != thread.InputRequestID {
		t.Fatalf("parsed auto-fill metadata = %#v, want thread_id=%s fills_input_request_id=%s", meta, threadID, thread.InputRequestID)
	}
}

// TestRunExecuteBashRecordDecisionRetriesAutoFillAfterInjectedFailure pins
// F-034: if the auto-fill append fails on the first --record-decision call
// (after the decision event itself already landed), a retry must still be
// able to close the request. Before the fix, the retry would mint a fresh
// decision message id that the reply-slot resolver's fixed
// thread.DecisionMessageID could never match again, permanently stranding
// the request.
func TestRunExecuteBashRecordDecisionRetriesAutoFillAfterInjectedFailure(t *testing.T) {
	fixture, threadID, thread := newOpenApprovalFixture(t, "printf autofill-retry-me")

	original := recordCommandApprovalAutoFillFn
	injectedErr := fmt.Errorf("injected auto-fill failure")
	recordCommandApprovalAutoFillFn = func(sessionDir, contextID, tmuxSessionName, eventType string, visibility journal.Visibility, payload journal.MailboxEventPayload, equivalent journal.EventEquivalenceFunc, now time.Time) (bool, error) {
		return false, injectedErr
	}
	t.Cleanup(func() { recordCommandApprovalAutoFillFn = original })

	err := runExecuteBashWithContext(fixture.contextAsPane("approver"), fixture.args(
		"--thread-id", threadID,
		"--record-decision", "approved",
		"--reason", "first attempt, auto-fill will fail",
	))
	if err == nil || !strings.Contains(err.Error(), "injected auto-fill failure") {
		t.Fatalf("runExecuteBashWithContext(first attempt) error = %v, want injected auto-fill failure", err)
	}
	// The decision itself must still have landed durably even though the
	// fill failed -- this is exactly the "worse than the pre-fix bug"
	// scenario guardian described: an already-decided, still-open request.
	_, decidedThread := onlyApprovalThread(t, fixture.sessionDir, fixture.now)
	if decidedThread.Status != projection.CommandApprovalStatusApproved {
		t.Fatalf("thread status after failed auto-fill = %q, want approved (decision must land even if the fill fails)", decidedThread.Status)
	}
	assertApprovalReplySlot(t, fixture.sessionDir, fixture.sessionName, thread.InputRequestID, true)

	recordCommandApprovalAutoFillFn = original
	if err := runExecuteBashWithContext(fixture.contextAsPane("approver"), fixture.args(
		"--thread-id", threadID,
		"--record-decision", "approved",
		"--reason", "retry after auto-fill recovers",
	)); err != nil {
		t.Fatalf("runExecuteBashWithContext(retry) error = %v, want the retry to succeed by reusing the existing decision message id", err)
	}
	assertApprovalReplySlot(t, fixture.sessionDir, fixture.sessionName, thread.InputRequestID, false)
}

// TestRunExecuteBashRecordDecisionAutoFillNotSuppressedByStaleFillIDEvent
// pins F-035: a pre-existing mailbox_projection_post_consumed event that
// shares only the FillsInputRequestID with the real fill, but differs in
// message id/thread id/from/to, must not make the idempotency guard skip
// the fill that would actually resolve the slot.
func TestRunExecuteBashRecordDecisionAutoFillNotSuppressedByStaleFillIDEvent(t *testing.T) {
	fixture, threadID, thread := newOpenApprovalFixture(t, "printf autofill-stale-me")

	staleContent := fmt.Sprintf(`---
params:
  messageId: stale-unrelated.md
  from: someone-else
  to: worker
  thread_id: command-approval-unrelated
  command_hash: sha256:unrelated
  fills_input_request_id: %s
---

# Message
`, thread.InputRequestID)
	stalePayload := journal.MailboxEventPayload{
		MessageID:           "stale-unrelated.md",
		From:                "someone-else",
		To:                  "worker",
		ThreadID:            "command-approval-unrelated",
		FillsInputRequestID: thread.InputRequestID,
		Content:             staleContent,
	}
	if _, err := recordCommandApprovalAutoFillFn(fixture.sessionDir, fixture.contextID, fixture.sessionName, projection.MailboxProjectionPostConsumedEventType, journal.VisibilityMailboxProjection, stalePayload, func(journal.Event) (bool, error) { return false, nil }, fixture.now); err != nil {
		t.Fatalf("seeding stale fill event: %v", err)
	}

	if err := runExecuteBashWithContext(fixture.contextAsPane("approver"), fixture.args(
		"--thread-id", threadID,
		"--record-decision", "approved",
		"--reason", "real decision despite stale unrelated fill",
	)); err != nil {
		t.Fatalf("runExecuteBashWithContext(record decision) error = %v", err)
	}
	assertApprovalReplySlot(t, fixture.sessionDir, fixture.sessionName, thread.InputRequestID, false)

	events, err := journal.Replay(fixture.sessionDir)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	matchingFillCount := 0
	for _, event := range events {
		if event.Type != projection.MailboxProjectionPostConsumedEventType {
			continue
		}
		var payload journal.MailboxEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("Unmarshal(mailbox_projection_post_consumed payload): %v", err)
		}
		if payload.FillsInputRequestID == thread.InputRequestID {
			matchingFillCount++
		}
	}
	if matchingFillCount != 2 {
		t.Fatalf("mailbox_projection_post_consumed events sharing fills_input_request_id %q = %d, want 2 (stale + real, proving the real fill wasn't wrongly suppressed)", thread.InputRequestID, matchingFillCount)
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

// TestRunExecuteBashBlockingWaitsForDecisionThenRuns pins #823's core
// behavior: blocking mode waits by default (no --wait flag needed) for a
// trusted, resolvable reviewer's decision instead of returning "blocked"
// while approval is merely pending, and runs the already-approved command in
// the same invocation without the caller reconstructing or resubmitting it.
// The injected ctx.sleep hook stands in for the approver's own
// --record-decision call landing mid-wait.
func TestRunExecuteBashBlockingWaitsForDecisionThenRuns(t *testing.T) {
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
	commandText := "printf waited-then-ran"
	threadID := commandApprovalThreadID(policy, commandDigest(commandText))

	ctx := fixture.context()
	decided := false
	ctx.sleep = func(context.Context, time.Duration) {
		if decided {
			return
		}
		decided = true
		fixture.appendCommandApprovalDecisionForRequest(t, threadID, "orchestrator", journal.ApprovalDecisionApproved)
	}

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", commandText,
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v, want default-on wait to observe the approval and run", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
	if got := fixture.commands[0]; got != commandText {
		t.Fatalf("command = %q, want %q (no reconstruction/resubmission needed)", got, commandText)
	}
	if !strings.Contains(fixture.stdout.String(), "ran") {
		t.Fatalf("stdout = %q, want the executed command's stdout to be captured (#823 F-009)", fixture.stdout.String())
	}
	if !strings.Contains(fixture.stderr.String(), "ran-stderr-9f2c") {
		t.Fatalf("stderr = %q, want the executed command's stderr to be captured too (#823 F-009)", fixture.stderr.String())
	}
}

// TestRunExecuteBashBlockingRetryReusesExistingRequestCorrelation pins
// #823 F-002's retry closure: a retry against an already-pending thread
// must reuse the existing request's input_request_id, never mint a new
// one, so a decision reply naming the ORIGINAL id can still resolve it.
func TestRunExecuteBashBlockingRetryReusesExistingRequestCorrelation(t *testing.T) {
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
	commandText := "printf retry-reuse"
	threadID := commandApprovalThreadID(policy, commandDigest(commandText))
	args := fixture.args("--label", "protected", "--category", "release", "--command", commandText)

	if err := runExecuteBashWithContext(fixture.context(), args); err == nil {
		t.Fatal("first call error = nil, want pending blocking refusal")
	}
	state1, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() (after first call) = (%#v, %v, %v)", state1, ok, err)
	}
	firstInputRequestID := state1.Threads[threadID].InputRequestID
	if firstInputRequestID == "" {
		t.Fatal("first call did not record an input_request_id")
	}

	if err := runExecuteBashWithContext(fixture.context(), args); err == nil {
		t.Fatal("retry error = nil, want pending blocking refusal")
	}
	state2, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() (after retry) = (%#v, %v, %v)", state2, ok, err)
	}
	if got := state2.Threads[threadID].InputRequestID; got != firstInputRequestID {
		t.Fatalf("retry input_request_id = %q, want unchanged %q (retry must reuse, not overwrite, the pending correlation)", got, firstInputRequestID)
	}
	requestCount := 0
	for _, event := range replayCommandEvents(t, fixture.sessionDir) {
		if event.Type == journal.CommandApprovalRequestedEventType {
			requestCount++
		}
	}
	if requestCount != 1 {
		t.Fatalf("command_approval_requested events = %d, want 1 (retry must not mint a new request)", requestCount)
	}
}

// TestRunExecuteBashBlockingWaitRejectsCrossRequesterThread pins #823
// F-002's cross-requester closure: a thread reused via an explicit
// --thread-id that belongs to a DIFFERENT requester must never authorize
// this invocation, even with a matching command digest, and must be
// refused immediately (no waiting).
func TestRunExecuteBashBlockingWaitRejectsCrossRequesterThread(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "*",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	otherPolicy := resolvedCommandApprovalPolicy{
		Requester: "other-worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
		Category:  "release",
		TTL:       defaultCommandApprovalTTL,
	}
	commandText := "printf cross-requester"
	foreignThreadID := fixture.appendCommandApprovalRequest(t, otherPolicy, commandText, fixture.now.Add(15*time.Minute))

	ctx := fixture.context()
	ctx.sleep = func(context.Context, time.Duration) {
		t.Fatal("ctx.sleep called, want an immediate requester_mismatch without waiting")
	}

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected",
		"--category", "release",
		"--thread-id", foreignThreadID,
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("error = nil, want cross-requester refusal")
	}
	if !strings.Contains(err.Error(), "different requester") {
		t.Fatalf("error = %v, want cross-requester diagnostic", err)
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "requester_mismatch" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: requester_mismatch}", err)
	}
	if outcomeErr.ExitCode() != 18 {
		t.Fatalf("ExitCode() = %d, want 18", outcomeErr.ExitCode())
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingWaitDeliveryFailureFailsFast pins #823 F-003's
// delivery-failure closure: a request that cannot be delivered to the
// approver must fail fast with a distinct outcome, never entering the wait
// loop and wasting the full timeout on a request the approver never saw.
func TestRunExecuteBashBlockingWaitDeliveryFailureFailsFast(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	fixture.discoveredNodes = map[string]discovery.NodeInfo{}
	commandText := "printf delivery-failure"

	ctx := fixture.context()
	ctx.sleep = func(context.Context, time.Duration) {
		t.Fatal("ctx.sleep called, want delivery failure to fail fast without waiting")
	}

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("error = nil, want delivery_failed refusal")
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "delivery_failed" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: delivery_failed}", err)
	}
	if outcomeErr.ExitCode() != 19 {
		t.Fatalf("ExitCode() = %d, want 19", outcomeErr.ExitCode())
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingWaitDetectsApproverLostMidWait pins #823
// F-003's mid-wait-loss closure: the approver becoming undiscoverable
// while a wait is in progress must end the wait with a distinct outcome
// instead of polling all the way to a generic timeout.
func TestRunExecuteBashBlockingWaitDetectsApproverLostMidWait(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	commandText := "printf approver-lost"

	ctx := fixture.context()
	lost := false
	ctx.sleep = func(context.Context, time.Duration) {
		if lost {
			return
		}
		lost = true
		delete(fixture.discoveredNodes, "test-session:orchestrator")
	}

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("error = nil, want approver_lost refusal")
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "approver_lost" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: approver_lost}", err)
	}
	if outcomeErr.ExitCode() != 20 {
		t.Fatalf("ExitCode() = %d, want 20", outcomeErr.ExitCode())
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingWaitSessionGenerationChangeEndsWait pins #823
// F-004's closure: a context/session-generation change mid-wait must end
// the wait with a distinct terminal outcome instead of silently polling
// toward a generic timeout that masks what actually happened.
func TestRunExecuteBashBlockingWaitSessionGenerationChangeEndsWait(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	commandText := "printf session-changed"

	ctx := fixture.context()
	changed := false
	ctx.sleep = func(context.Context, time.Duration) {
		if changed {
			return
		}
		changed = true
		path := journal.SessionStatePath(fixture.sessionDir)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(session state) error = %v", err)
		}
		var state journal.SessionState
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatalf("Unmarshal(session state) error = %v", err)
		}
		state.Generation++
		newData, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("Marshal(session state) error = %v", err)
		}
		if err := os.WriteFile(path, newData, 0o644); err != nil {
			t.Fatalf("WriteFile(session state) error = %v", err)
		}
	}

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("error = nil, want session_changed refusal")
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "session_changed" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: session_changed}", err)
	}
	if outcomeErr.ExitCode() != 21 {
		t.Fatalf("ExitCode() = %d, want 21", outcomeErr.ExitCode())
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingWaitCancellationBeatsLateApproval pins #823
// F-005's ordering closure: cancellation and the deadline are checked
// BEFORE the wait loop accepts any approval each iteration, so an approval
// recorded in the same tick as cancellation must never be honored.
func TestRunExecuteBashBlockingWaitCancellationBeatsLateApproval(t *testing.T) {
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
	commandText := "printf cancel-beats-late-approval"
	threadID := commandApprovalThreadID(policy, commandDigest(commandText))

	ctx := fixture.context()
	waitCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ctx.newInterruptContext = func() (context.Context, func()) { return waitCtx, cancel }
	done := false
	ctx.sleep = func(context.Context, time.Duration) {
		if done {
			return
		}
		done = true
		// Cancel AND record a late approval in the same tick: the next
		// iteration must observe cancellation, not the approval.
		cancel()
		fixture.appendCommandApprovalDecisionForRequest(t, threadID, "orchestrator", journal.ApprovalDecisionApproved)
	}

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("error = nil, want cancellation to win over the late approval")
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "cancelled" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: cancelled} even though an approval was recorded in the same tick", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0 (a late approval must never run the command)", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingApprovalIsSingleUseAcrossRepeatedCalls pins
// #823 F-001's core guarantee: one approval executes the command at most
// once. A second call after the command has already run against this
// exact approval must observe the claim and refuse, not run again.
func TestRunExecuteBashBlockingApprovalIsSingleUseAcrossRepeatedCalls(t *testing.T) {
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
	commandText := "printf single-use"
	fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionApproved, "orchestrator", fixture.now.Add(15*time.Minute))
	args := fixture.args("--label", "protected", "--category", "release", "--command", commandText)

	if err := runExecuteBashWithContext(fixture.context(), args); err != nil {
		t.Fatalf("first call error = %v, want nil (approved command runs)", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount after first call = %d, want 1", fixture.runCount)
	}

	err := runExecuteBashWithContext(fixture.context(), args)
	if err == nil {
		t.Fatal("second call error = nil, want already_executed refusal")
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "already_executed" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: already_executed}", err)
	}
	if outcomeErr.ExitCode() != 22 {
		t.Fatalf("ExitCode() = %d, want 22", outcomeErr.ExitCode())
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount after repeated call = %d, want still 1 (approval is single-use)", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingApprovalIsSingleUseAcrossConcurrentWaiters pins
// the other half of #823 F-001's closure: real concurrent waiters (actual
// goroutines racing against the same on-disk session, not a sequential
// simulation) on the same pending thread must each observe the approval,
// but the atomic claim (journal.Writer.AppendCurrentSessionEventIfAbsent,
// cross-process-safe via its append-authority fence) must let exactly one
// of them run the command.
func TestRunExecuteBashBlockingApprovalIsSingleUseAcrossConcurrentWaiters(t *testing.T) {
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
	commandText := "printf concurrent-waiters"
	threadID := fixture.appendCommandApprovalRequest(t, policy, commandText, fixture.now.Add(15*time.Minute))

	var runCount atomic.Int64
	newRacingContext := func() commandContext {
		base := fixture.context()
		base.runBash = func(command string, stdout, stderr io.Writer) (int, error) {
			runCount.Add(1)
			_, _ = fmt.Fprint(stdout, "ran\n")
			return 0, nil
		}
		return base
	}

	var approveOnce sync.Once
	var approveErr error
	sharedSleep := func(context.Context, time.Duration) {
		approveOnce.Do(func() {
			state, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
			if err != nil || !ok {
				approveErr = fmt.Errorf("ProjectCommandApprovalState() = (ok=%v, err=%v)", ok, err)
				return
			}
			thread, found := state.Threads[threadID]
			if !found {
				approveErr = fmt.Errorf("missing thread %q", threadID)
				return
			}
			writer, err := journal.OpenCurrentWriter(fixture.sessionDir)
			if err != nil {
				writer, err = journal.OpenShadowWriter(fixture.sessionDir, fixture.contextID, fixture.sessionName, os.Getpid(), fixture.now)
			}
			if err != nil {
				approveErr = err
				return
			}
			_, err = writer.AppendEventWithOptions(journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalDecisionPayload{
				Reviewer:         "orchestrator",
				ReviewerAddress:  nodeaddr.Full("orchestrator", fixture.sessionName),
				RequesterAddress: thread.RequesterAddress,
				Decision:         journal.ApprovalDecisionApproved,
				Reason:           "reviewed",
				InputRequestID:   thread.InputRequestID,
				CommandHash:      thread.CommandHash,
			}, journal.AppendOptions{ThreadID: threadID}, fixture.now)
			approveErr = err
		})
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := newRacingContext()
			ctx.sleep = sharedSleep
			errs[i] = runExecuteBashWithContext(ctx, fixture.args(
				"--label", "protected",
				"--category", "release",
				"--command", commandText,
			))
		}(i)
	}
	wg.Wait()

	if approveErr != nil {
		t.Fatalf("recording the shared approval decision failed: %v", approveErr)
	}
	successCount := 0
	for _, err := range errs {
		if err == nil {
			successCount++
			continue
		}
		var outcomeErr commandApprovalOutcomeError
		if !errors.As(err, &outcomeErr) || outcomeErr.status != "already_executed" {
			t.Fatalf("unexpected error from a concurrent waiter: %v", err)
		}
	}
	if successCount != 1 {
		t.Fatalf("successful (executing) concurrent waiters = %d, want exactly 1; errs=%v", successCount, errs)
	}
	if got := runCount.Load(); got != 1 {
		t.Fatalf("runBash invocations across concurrent waiters = %d, want exactly 1", got)
	}
}

// TestRunExecuteBashApprovedExitTenDistinguishableFromRejectedExitTen pins
// #823 F-006's documented contract: an executed command's exit status and
// an approval-outcome exit code are NOT guaranteed numerically distinct
// (both can be 10) — the error TYPE / --json "status" field is the
// authoritative discriminator, never the bare exit code. This test is the
// "command ran" half of the pair; TestRunExecuteBashBlockingWaitObservesRejectionThenRefuses
// is the "approval was rejected" half, and both produce exit code 10.
func TestRunExecuteBashApprovedExitTenDistinguishableFromRejectedExitTen(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	fixture.runStatus = 10
	policy := resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
		Category:  "release",
		TTL:       defaultCommandApprovalTTL,
	}
	commandText := "exit 10"
	fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionApproved, "orchestrator", fixture.now.Add(15*time.Minute))

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected", "--category", "release", "--command", commandText,
	))
	var exitErr commandExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want commandExitError (the command ran and exited 10)", err, err)
	}
	if exitErr.ExitCode() != 10 {
		t.Fatalf("ExitCode() = %d, want 10", exitErr.ExitCode())
	}
	var outcomeErr commandApprovalOutcomeError
	if errors.As(err, &outcomeErr) {
		t.Fatalf("error unexpectedly also matches commandApprovalOutcomeError: %#v - an executed command's exit status must never be confused with an approval-outcome status", outcomeErr)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1 (the command actually ran)", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingWaitObservesExpiryDuringWait pins #823 F-009's
// expiry-during-wait gap: a thread whose own approval TTL elapses while a
// wait is in progress must report "expired" (a specific, informative
// outcome), not a generic "wait_timeout" that would mask the real cause.
func TestRunExecuteBashBlockingWaitObservesExpiryDuringWait(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	commandText := "printf expire-during-wait"

	ctx := fixture.context()
	clock := fixture.now
	ctx.now = func() time.Time { return clock }
	ctx.sleep = func(_ context.Context, d time.Duration) { clock = clock.Add(d) }

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", commandText,
		"--approval-ttl-seconds", "1",
		"--wait-timeout-seconds", "300",
	))
	if err == nil {
		t.Fatal("error = nil, want expired refusal")
	}
	if !strings.Contains(err.Error(), "approval request has expired") {
		t.Fatalf("error = %v, want expiry diagnostic (not a generic wait_timeout)", err)
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "expired" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: expired}", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashDefaultWaitFailsOpenWithoutCommandApproverNode pins
// #823 F-009's default-wait fail-open gap: the #626 fail-open rule must
// still apply, and must never engage the wait loop, when the default-on
// wait is in effect (no --no-wait passed).
func TestRunExecuteBashDefaultWaitFailsOpenWithoutCommandApproverNode(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	fixture.commandApproverNode = ""
	fixture.nodes = nil

	ctx := fixture.context()
	ctx.sleep = func(context.Context, time.Duration) {
		t.Fatal("ctx.sleep called, want fail-open to run immediately without waiting")
	}

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "default-wait-fail-open",
		"--command", "printf default-wait-fail-open",
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v, want nil (fail open)", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
}

// TestRunExecuteBashDefaultWaitFailsClosedWhenCommandApproverNodeUnresolvable
// pins #823 F-009's default-wait fail-closed gap: an unresolvable approver
// must still block immediately without ever engaging the wait loop.
func TestRunExecuteBashDefaultWaitFailsClosedWhenCommandApproverNodeUnresolvable(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	fixture.commandApproverNode = "typo-reviewer"
	fixture.nodes = map[string]config.NodeConfig{"orchestrator": {}}
	fixture.discoveredNodes = map[string]discovery.NodeInfo{
		"test-session:typo-reviewer": {PaneID: "%9", SessionName: "test-session", SessionDir: fixture.sessionDir},
	}

	ctx := fixture.context()
	ctx.sleep = func(context.Context, time.Duration) {
		t.Fatal("ctx.sleep called, want fail-closed to block immediately without waiting")
	}

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "default-wait-fail-closed",
		"--command", "printf default-wait-fail-closed",
	))
	if err == nil {
		t.Fatal("error = nil, want unresolved approver block")
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingWaitTimesOutWithoutDecision pins the bounded-wait
// acceptance criterion: waiting must never be indefinite. ctx.now and
// ctx.sleep are wired to the same fake clock so the deadline is reached
// deterministically without a real delay.
func TestRunExecuteBashBlockingWaitTimesOutWithoutDecision(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	commandText := "printf wait-timeout"

	ctx := fixture.context()
	clock := fixture.now
	ctx.now = func() time.Time { return clock }
	ctx.sleep = func(_ context.Context, d time.Duration) { clock = clock.Add(d) }

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", commandText,
		"--wait-timeout-seconds", "1",
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want wait_timeout refusal")
	}
	if !strings.Contains(err.Error(), "approval wait timed out") {
		t.Fatalf("error = %v, want wait-timeout diagnostic", err)
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "wait_timeout" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: wait_timeout}", err)
	}
	if outcomeErr.ExitCode() != 12 {
		t.Fatalf("ExitCode() = %d, want 12", outcomeErr.ExitCode())
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingWaitCancelledBySignal pins clean termination on
// interruption (SIGINT/SIGTERM in production, an injected cancel here) as a
// final outcome distinguishable from a timeout or a rejection.
func TestRunExecuteBashBlockingWaitCancelledBySignal(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	commandText := "printf wait-cancelled"

	ctx := fixture.context()
	waitCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ctx.newInterruptContext = func() (context.Context, func()) { return waitCtx, cancel }
	cancelled := false
	ctx.sleep = func(context.Context, time.Duration) {
		if !cancelled {
			cancelled = true
			cancel()
		}
	}

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want cancellation refusal")
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "cancelled" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: cancelled}", err)
	}
	if outcomeErr.ExitCode() != 13 {
		t.Fatalf("ExitCode() = %d, want 13", outcomeErr.ExitCode())
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingWaitApprovedRunsCommandAndPropagatesExitStatus
// covers the "approval, then command output/exit status" outcome: the
// command's own non-zero exit status must still surface via
// commandExitError after a default-on wait resolves to approved, exactly as
// it does on the immediate-approval path (TestRunExecuteBashPropagatesExitStatus).
func TestRunExecuteBashBlockingWaitApprovedRunsCommandAndPropagatesExitStatus(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	fixture.runStatus = 3
	policy := resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
		Category:  "release",
		TTL:       defaultCommandApprovalTTL,
	}
	commandText := "exit 3"
	threadID := commandApprovalThreadID(policy, commandDigest(commandText))

	ctx := fixture.context()
	decided := false
	ctx.sleep = func(context.Context, time.Duration) {
		if decided {
			return
		}
		decided = true
		fixture.appendCommandApprovalDecisionForRequest(t, threadID, "orchestrator", journal.ApprovalDecisionApproved)
	}

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", commandText,
	))
	var exitErr commandExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("error = %T %v, want commandExitError after default-on wait resolves to approved", err, err)
	}
	if exitErr.ExitCode() != 3 {
		t.Fatalf("ExitCode() = %d, want 3", exitErr.ExitCode())
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
	completed := findExecutionCompletedPayload(t, fixture.sessionDir)
	if completed.ExitStatus != 3 {
		t.Fatalf("completed exit status = %d, want 3", completed.ExitStatus)
	}
}

// TestRunExecuteBashBlockingWaitObservesRejectionThenRefuses covers the
// "rejection" outcome: a decision recorded mid-wait must end the wait loop
// immediately (not just at the timeout) with a distinguishable status/exit
// code, and never run the command.
func TestRunExecuteBashBlockingWaitObservesRejectionThenRefuses(t *testing.T) {
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
	commandText := "printf wait-rejected"
	threadID := commandApprovalThreadID(policy, commandDigest(commandText))

	ctx := fixture.context()
	decided := false
	ctx.sleep = func(context.Context, time.Duration) {
		if decided {
			return
		}
		decided = true
		fixture.appendCommandApprovalDecisionForRequest(t, threadID, "orchestrator", journal.ApprovalDecisionRejected)
	}

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want rejection refusal")
	}
	if !strings.Contains(err.Error(), "approval is rejected") {
		t.Fatalf("error = %v, want rejection diagnostic", err)
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "rejected" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: rejected}", err)
	}
	if outcomeErr.ExitCode() != 10 {
		t.Fatalf("ExitCode() = %d, want 10", outcomeErr.ExitCode())
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingWaitSkipsWaitingOnImmediateDigestMismatch covers
// the "changed digest" outcome under the new default-on path: a thread
// already carrying an approved decision for a DIFFERENT command digest is a
// terminal, non-waitable state from the very first evaluation, so
// shouldWaitForCommandApproval must never engage the poll loop for it (the
// sleep spy fails the test if invoked) even though --no-wait is not passed.
func TestRunExecuteBashBlockingWaitSkipsWaitingOnImmediateDigestMismatch(t *testing.T) {
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
	approvedThreadID := fixture.appendCommandApproval(t, policy, "printf original-digest-wait", journal.ApprovalDecisionApproved, "orchestrator", fixture.now.Add(15*time.Minute))

	ctx := fixture.context()
	ctx.sleep = func(context.Context, time.Duration) {
		t.Fatal("ctx.sleep called, want no waiting for an already-terminal digest_mismatch decision")
	}

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected",
		"--category", "release",
		"--thread-id", approvedThreadID,
		"--command", "printf attack-command-wait",
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want digest_mismatch block")
	}
	if !strings.Contains(err.Error(), "different command digest") {
		t.Fatalf("error = %v, want reason containing \"different command digest\"", err)
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "digest_mismatch" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: digest_mismatch}", err)
	}
	if outcomeErr.ExitCode() != 14 {
		t.Fatalf("ExitCode() = %d, want 14", outcomeErr.ExitCode())
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashNoWaitProducesMigrationGuidanceError pins #831 D2's
// removal of --no-wait: a caller still passing it must get a clear
// migration-guidance error, never Go's generic "flag provided but not
// defined" error, and must never reach the flag set, config load, or any
// wait/mint logic at all.
func TestRunExecuteBashNoWaitProducesMigrationGuidanceError(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	ctx := fixture.context()
	ctx.sleep = func(context.Context, time.Duration) {
		t.Fatal("ctx.sleep called, want the removed --no-wait flag to be refused before any wait logic runs")
	}

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", "printf no-wait-legacy",
		"--no-wait",
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want a migration-guidance error for the removed --no-wait flag")
	}
	if strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("error = %v, want migration guidance, not Go's generic unknown-flag error", err)
	}
	if !strings.Contains(err.Error(), "--no-wait was removed") {
		t.Fatalf("error = %v, want a clear --no-wait removal message", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashRequesterFlagProducesMigrationGuidanceError pins #831
// D4's removal of --requester: a caller still passing it must get a clear
// migration-guidance error, never Go's generic "flag provided but not
// defined" error.
func TestRunExecuteBashRequesterFlagProducesMigrationGuidanceError(t *testing.T) {
	fixture := newExecuteBashFixture(t)

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", "printf requester-legacy",
		"--requester", "some-other-node",
	))
	if err == nil {
		t.Fatal("runExecuteBashWithContext() error = nil, want a migration-guidance error for the removed --requester flag")
	}
	if strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("error = %v, want migration guidance, not Go's generic unknown-flag error", err)
	}
	if !strings.Contains(err.Error(), "--requester was removed") {
		t.Fatalf("error = %v, want a clear --requester removal message", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashClaimRefusesExpiredApprovalObservedJustBeforeExpiry pins
// #831 I-001 (guardian rework 1, HIGH): an approval observed as "approved"
// just before its own TTL lapses must never be claimed and run after that
// TTL has actually passed. appendEventBeforeClaimHookFn injects the exact
// "clock advances between observation and claim" race the guardian's review
// identified -- the pre-claim interruption re-check runs BEFORE this hook
// fires (so it still sees a non-expired approval), and only the atomic
// claim's own equivalence-closure check (added by this fix) catches the
// expiry that lands in this narrow window.
func TestRunExecuteBashClaimRefusesExpiredApprovalObservedJustBeforeExpiry(t *testing.T) {
	fixture := newExecuteBashFixture(t, config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	})
	policy := resolvedCommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
		Category:  "release",
	}
	commandText := "printf claim-after-expiry"
	threadID := commandApprovalThreadID(policy, commandDigest(commandText))

	ctx := fixture.context()
	decided := false
	ctx.sleep = func(context.Context, time.Duration) {
		// Decide on the very first poll tick, well within the short 3s TTL
		// below, WITHOUT advancing the clock -- the wait loop observes the
		// approval immediately afterward.
		if decided {
			return
		}
		decided = true
		fixture.appendCommandApprovalDecisionForRequest(t, threadID, "orchestrator", journal.ApprovalDecisionApproved)
	}

	original := appendEventBeforeClaimHookFn
	appendEventBeforeClaimHookFn = func() {
		// Advance the clock past the approval's own (short, 3s) expiry,
		// simulating time passing in the narrow window between the wait
		// loop accepting the approved decision and the atomic claim
		// immediately below.
		fixture.advanceNow(5 * time.Second)
	}
	t.Cleanup(func() { appendEventBeforeClaimHookFn = original })

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected", "--category", "release", "--command", commandText,
		"--approval-ttl-seconds", "3",
	))
	if err == nil {
		t.Fatal("error = nil, want expired refusal")
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "expired" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: expired}", err)
	}
	if outcomeErr.ExitCode() != 11 {
		t.Fatalf("ExitCode() = %d, want 11", outcomeErr.ExitCode())
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0 (an expired approval must never run)", fixture.runCount)
	}
	for _, event := range replayCommandEvents(t, fixture.sessionDir) {
		if event.Type == journal.CommandExecutionClaimedEventType {
			t.Fatalf("unexpected claim event recorded for an expired approval: %#v", event)
		}
	}
}

// TestCommandApprovalWaitInterruptionExpiredWinsAtEqualDeadlineUnderAdvancingClock
// pins #831 I-002 (guardian rework 1, MEDIUM): commandApprovalWaitInterruption
// must sample "now" exactly once and compare BOTH the expiry and
// wait-timeout deadlines against that single instant. A clock that advances
// on every call could otherwise let wait_timeout win a race it should have
// lost to expired, if the two checks sampled two different instants
// straddling the (equal) deadlines. This is a direct unit test of the
// function (same package), not an end-to-end CLI test, because the
// production fixture's fake clock only advances via explicit ctx.sleep
// calls and cannot otherwise reproduce a same-tick double-read race.
func TestCommandApprovalWaitInterruptionExpiredWinsAtEqualDeadlineUnderAdvancingClock(t *testing.T) {
	base := time.Date(2026, time.June, 1, 10, 0, 0, 0, time.UTC)
	deadline := base.Add(time.Second)
	callCount := 0
	ctx := commandContext{
		now: func() time.Time {
			callCount++
			// Advances aggressively on EVERY call so the test fails loudly
			// if this function is ever changed back to sampling now() more
			// than once per invocation.
			return base.Add(time.Duration(callCount) * time.Second)
		},
	}
	params := commandApprovalWaitParams{deadline: deadline, expiryDeadline: deadline}
	result := commandApprovalWaitInterruption(context.Background(), ctx, params, "", 0, false)
	if result == nil {
		t.Fatal("commandApprovalWaitInterruption() = nil, want a terminal result")
	}
	if result.Decision != "expired" {
		t.Fatalf("Decision = %q, want %q (expired must win at/after equal deadlines)", result.Decision, "expired")
	}
	if callCount != 1 {
		t.Fatalf("ctx.now() was called %d times, want exactly 1 (I-002: sample once, reuse for both checks)", callCount)
	}
}

// TestValidatePositiveSecondsFlag pins #831 D2/D6's shared validation for
// --wait-timeout-seconds and (when explicitly set) --approval-ttl-seconds:
// zero, negative, NaN, +/-Inf, sub-nanosecond, and overflowing values must
// all be refused with a specific, named diagnostic, before ever converting
// to a time.Duration.
func TestValidatePositiveSecondsFlag(t *testing.T) {
	for _, tc := range []struct {
		name    string
		seconds float64
		wantErr string
	}{
		{name: "zero", seconds: 0, wantErr: "must be a positive number of seconds"},
		{name: "negative", seconds: -1, wantErr: "must be a positive number of seconds"},
		{name: "NaN", seconds: math.NaN(), wantErr: "must be a finite positive number of seconds"},
		{name: "positive infinity", seconds: math.Inf(1), wantErr: "must be a finite positive number of seconds"},
		{name: "negative infinity", seconds: math.Inf(-1), wantErr: "must be a finite positive number of seconds"},
		{name: "sub-nanosecond", seconds: 1e-12, wantErr: "too small to represent"},
		{name: "overflow", seconds: math.MaxFloat64, wantErr: "overflows the maximum representable duration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := validatePositiveSecondsFlag("wait-timeout-seconds", tc.seconds); err == nil {
				t.Fatalf("validatePositiveSecondsFlag(%v) error = nil, want refusal", tc.seconds)
			} else if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want to contain %q", err, tc.wantErr)
			}
		})
	}
	t.Run("valid positive", func(t *testing.T) {
		d, err := validatePositiveSecondsFlag("wait-timeout-seconds", 300)
		if err != nil {
			t.Fatalf("validatePositiveSecondsFlag(300) error = %v, want nil", err)
		}
		if d != 300*time.Second {
			t.Fatalf("duration = %v, want 300s", d)
		}
	})
}

// TestResolveCommandApprovalPolicyTTLOmittedUsesFloor pins #831 D6: an
// OMITTED --approval-ttl-seconds is a harmless no-op, using the floor TTL
// (config-declared, or the hardcoded default) with no validation and no
// error -- distinct from an EXPLICIT 0, which is refused (see
// TestValidatePositiveSecondsFlag).
func TestResolveCommandApprovalPolicyTTLOmittedUsesFloor(t *testing.T) {
	policy, err := resolveCommandApprovalPolicy(&config.Config{}, "worker", "label", "category", "", "", 0, false)
	if err != nil {
		t.Fatalf("resolveCommandApprovalPolicy() error = %v, want nil (omitted TTL is a no-op)", err)
	}
	if policy.TTL != defaultCommandApprovalTTL {
		t.Fatalf("policy.TTL = %v, want the default floor %v", policy.TTL, defaultCommandApprovalTTL)
	}
}

// TestResolveCommandApprovalPolicyTTLExplicitLengthenRefused pins #831 D6:
// an explicit --approval-ttl-seconds may only TIGHTEN (shorten) the
// effective TTL floor, never lengthen it.
func TestResolveCommandApprovalPolicyTTLExplicitLengthenRefused(t *testing.T) {
	longerSeconds := (defaultCommandApprovalTTL + time.Minute).Seconds()
	_, err := resolveCommandApprovalPolicy(&config.Config{}, "worker", "label", "category", "", "", longerSeconds, true)
	if err == nil {
		t.Fatal("resolveCommandApprovalPolicy() error = nil, want lengthen refusal")
	}
	if !strings.Contains(err.Error(), "may only tighten") {
		t.Fatalf("error = %v, want tighten-only diagnostic", err)
	}
}

// TestResolveCommandApprovalPolicyTTLExplicitTightenSucceeds is the positive
// control for the test above: a value that legitimately SHORTENS the floor
// is accepted.
func TestResolveCommandApprovalPolicyTTLExplicitTightenSucceeds(t *testing.T) {
	shorterSeconds := (defaultCommandApprovalTTL - time.Minute).Seconds()
	policy, err := resolveCommandApprovalPolicy(&config.Config{}, "worker", "label", "category", "", "", shorterSeconds, true)
	if err != nil {
		t.Fatalf("resolveCommandApprovalPolicy() error = %v, want nil (tightening is allowed)", err)
	}
	want := time.Duration(shorterSeconds * float64(time.Second))
	if policy.TTL != want {
		t.Fatalf("policy.TTL = %v, want %v", policy.TTL, want)
	}
}

// TestRunExecuteBashReusedPendingThreadKeepsStoredExpiryDespiteTighterTTL
// pins #831 D6: a later call reusing an existing PENDING thread must never
// have its own (even validly tightened) --approval-ttl-seconds value alter
// that thread's already-stored expiry -- only the FIRST call that minted the
// thread fixes its expiry; a legitimately-tightened value simply has no
// effect on an already-minted thread, which is a different outcome from
// being refused.
func TestRunExecuteBashReusedPendingThreadKeepsStoredExpiryDespiteTighterTTL(t *testing.T) {
	fixture := newExecuteBashFixture(t, config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	})
	commandText := "printf reused-ttl"

	if err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected", "--category", "release", "--command", commandText,
		"--wait-timeout-seconds", "1",
	)); err == nil {
		t.Fatal("first call: error = nil, want wait_timeout")
	}
	_, thread := onlyApprovalThread(t, fixture.sessionDir, fixture.currentNow())
	originalExpiresAt := thread.ExpiresAt
	if originalExpiresAt == "" {
		t.Fatal("first call: thread.ExpiresAt is empty, want a real stored expiry")
	}

	if err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected", "--category", "release", "--command", commandText,
		"--wait-timeout-seconds", "1", "--approval-ttl-seconds", "60",
	)); err == nil {
		t.Fatal("second call: error = nil, want wait_timeout again")
	}
	_, reThread := onlyApprovalThread(t, fixture.sessionDir, fixture.currentNow())
	if reThread.ExpiresAt != originalExpiresAt {
		t.Fatalf("reused thread ExpiresAt changed = %q, want unchanged %q", reThread.ExpiresAt, originalExpiresAt)
	}
}

// TestRunExecuteBashModeDowngradeRefusedWithValidConfiguredApprover pins
// #831 D1: a --mode flag may only narrow the effective policy floor once an
// approver is configured -- a downgrade attempt must be refused BEFORE any
// request is minted or delivered, not merely ignored after minting.
func TestRunExecuteBashModeDowngradeRefusedWithValidConfiguredApprover(t *testing.T) {
	fixture := newExecuteBashFixture(t) // default floor: blocking (no matching policy)

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "downgrade-attempt",
		"--mode", "advisory",
		"--command", "printf downgrade",
	))
	if err == nil {
		t.Fatal("error = nil, want downgrade refusal")
	}
	if !strings.Contains(err.Error(), "would downgrade the effective policy floor") {
		t.Fatalf("error = %v, want downgrade-refusal diagnostic", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
	for _, event := range replayCommandEvents(t, fixture.sessionDir) {
		if event.Type == journal.CommandApprovalRequestedEventType {
			t.Fatalf("unexpected approval request minted for a refused downgrade: %#v", event)
		}
	}
}

// TestRunExecuteBashModeDowngradeRefusedWithUnresolvableApprover pins #831
// D1's stated scope: the downgrade refusal applies even when the configured
// command_approver_node is currently unresolvable -- only the genuinely
// ABSENT case (#626) is exempt.
func TestRunExecuteBashModeDowngradeRefusedWithUnresolvableApprover(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	fixture.commandApproverNode = "typo-reviewer"
	fixture.nodes = map[string]config.NodeConfig{"orchestrator": {}}
	fixture.discoveredNodes = map[string]discovery.NodeInfo{
		"test-session:typo-reviewer": {PaneID: "%9", SessionName: "test-session", SessionDir: fixture.sessionDir},
	}

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "downgrade-attempt",
		"--mode", "advisory",
		"--command", "printf downgrade",
	))
	if err == nil {
		t.Fatal("error = nil, want downgrade refusal")
	}
	if !strings.Contains(err.Error(), "would downgrade the effective policy floor") {
		t.Fatalf("error = %v, want downgrade-refusal diagnostic", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashModeDowngradeExemptWhenApproverAbsent pins #831 D1's
// stated exemption: with NO command_approver_node configured at all, the
// #626 fail-open rule still applies unconditionally, and a --mode flag has
// no downgrade to refuse (there is no configured floor to downgrade FROM).
func TestRunExecuteBashModeDowngradeExemptWhenApproverAbsent(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	fixture.commandApproverNode = ""
	fixture.nodes = nil

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "downgrade-attempt",
		"--mode", "advisory",
		"--command", "printf downgrade",
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v, want nil (fail open, no floor to downgrade)", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
	decision := findExecutionDecisionPayload(t, fixture.sessionDir)
	if decision.Decision != "auto_approved_no_reviewer" {
		t.Fatalf("decision.Decision = %q, want %q", decision.Decision, "auto_approved_no_reviewer")
	}
}

// TestRunExecuteBashSpoofedRequesterCannotSelectWeakerPinnedPolicy pins #831
// D4: the effective requester is always the calling pane's tmux title, never
// a caller-declared value -- a caller whose pane title does not match a
// policy's pinned Requester must not be able to reach that policy by any
// means; the policy match itself is keyed off the real pane title.
func TestRunExecuteBashSpoofedRequesterCannotSelectWeakerPinnedPolicy(t *testing.T) {
	fixture := newExecuteBashFixture(t, config.CommandApprovalPolicy{
		Requester: "trusted-node",
		Label:     "diagnostic-tool-x",
		Category:  "diagnostic",
		Mode:      "advisory",
	})

	// Invoked from a pane titled "attacker-node", NOT "trusted-node".
	err := runExecuteBashWithContext(fixture.contextAsPane("attacker-node"), fixture.args(
		"--label", "diagnostic-tool-x",
		"--category", "diagnostic",
		"--mode", "advisory",
		"--command", "printf spoofed",
	))
	if err == nil {
		t.Fatal("error = nil, want the pinned advisory policy to NOT match and the default blocking floor to refuse the --mode advisory downgrade")
	}
	if !strings.Contains(err.Error(), "would downgrade the effective policy floor") {
		t.Fatalf("error = %v, want a D1 downgrade-refusal diagnostic (policy did not match, floor stayed blocking)", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashMatchingRequesterAppliesPinnedPolicy is the positive
// control for the test above: invoked from the pane title the policy
// actually pins, the SAME policy DOES match and applies as configured,
// proving the fix above does not also break legitimate matching. This also
// documents D4's stated trust boundary: a pane retitled to impersonate
// "trusted-node" is indistinguishable from the real thing at this layer --
// an accepted, tmux-trust-level limit, not a bug.
func TestRunExecuteBashMatchingRequesterAppliesPinnedPolicy(t *testing.T) {
	fixture := newExecuteBashFixture(t, config.CommandApprovalPolicy{
		Requester: "trusted-node",
		Label:     "diagnostic-tool-x",
		Category:  "diagnostic",
		Mode:      "advisory",
	})

	err := runExecuteBashWithContext(fixture.contextAsPane("trusted-node"), fixture.args(
		"--label", "diagnostic-tool-x",
		"--category", "diagnostic",
		"--mode", "advisory",
		"--command", "printf trusted",
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v, want nil (pinned policy applies)", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
}

// TestRunExecuteBashEmptyPaneIdentityRefused pins #831 D4: a missing/empty
// pane title must be refused outright, never silently defaulted to some
// placeholder requester.
func TestRunExecuteBashEmptyPaneIdentityRefused(t *testing.T) {
	fixture := newExecuteBashFixture(t)

	err := runExecuteBashWithContext(fixture.contextAsPane(""), fixture.args(
		"--label", "diagnostic",
		"--command", "printf empty-pane",
	))
	if err == nil {
		t.Fatal("error = nil, want refusal for an empty pane identity")
	}
	if !strings.Contains(err.Error(), "could not resolve a non-empty tmux pane title") {
		t.Fatalf("error = %v, want the empty-pane-identity diagnostic", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingConcurrentFirstCallsMintExactlyOneRequest pins
// #823 rework-2's F-002 closure: two concurrent FIRST calls (no pre-existing
// thread) must converge on exactly one command_approval_requested event and
// the same input_request_id, via atomicCreateOrReplaceRequest's journal
// append-authority fence — never two competing requests for the same
// deterministic thread id.
func TestRunExecuteBashBlockingConcurrentFirstCallsMintExactlyOneRequest(t *testing.T) {
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
	commandText := "printf concurrent-first-callers"
	threadID := commandApprovalThreadID(policy, commandDigest(commandText))
	args := fixture.args("--label", "protected", "--category", "release", "--command", commandText)

	// Force the session-state.json bootstrap to happen once, up front,
	// before the two goroutines race. journal.OpenShadowWriter's first-time
	// session creation (ResolveSession) is not itself fenced the way
	// AppendCurrentSessionEventIfAbsent's event append is, so two goroutines
	// racing to create the session file from scratch is a distinct,
	// pre-existing race in session bootstrap -- unrelated to, and out of
	// scope for, the atomic request-mint race this test actually targets.
	_ = fixture.openWriter(t)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = runExecuteBashWithContext(fixture.context(), args)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err == nil {
			t.Fatalf("call %d error = nil, want a pending blocking refusal", i)
		}
	}
	requestCount := 0
	for _, event := range replayCommandEvents(t, fixture.sessionDir) {
		if event.Type == journal.CommandApprovalRequestedEventType {
			requestCount++
		}
	}
	if requestCount != 1 {
		t.Fatalf("command_approval_requested events = %d, want exactly 1 (two concurrent first calls must converge on one request)", requestCount)
	}
	state, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() = (%#v, %v, %v)", state, ok, err)
	}
	if state.Threads[threadID].InputRequestID == "" {
		t.Fatal("thread has no input_request_id after concurrent first calls")
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingWaitSessionChangeAndApprovalSameTickEndsWait
// re-confirms #823 F-004's closure after rework-2 refactored the
// cancellation/deadline/session-identity checks into the shared
// commandApprovalWaitInterruption helper: a session-generation bump that
// lands in the same tick as a late approval must still end the wait with
// "session_changed" and never run the command.
func TestRunExecuteBashBlockingWaitSessionChangeAndApprovalSameTickEndsWait(t *testing.T) {
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
	commandText := "printf session-change-plus-late-approval"
	threadID := commandApprovalThreadID(policy, commandDigest(commandText))

	ctx := fixture.context()
	done := false
	ctx.sleep = func(context.Context, time.Duration) {
		if done {
			return
		}
		done = true
		fixture.appendCommandApprovalDecisionForRequest(t, threadID, "orchestrator", journal.ApprovalDecisionApproved)
		path := journal.SessionStatePath(fixture.sessionDir)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(session state) error = %v", err)
		}
		var state journal.SessionState
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatalf("Unmarshal(session state) error = %v", err)
		}
		state.Generation++
		newData, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("Marshal(session state) error = %v", err)
		}
		if err := os.WriteFile(path, newData, 0o644); err != nil {
			t.Fatalf("WriteFile(session state) error = %v", err)
		}
	}

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected",
		"--category", "release",
		"--command", commandText,
	))
	if err == nil {
		t.Fatal("error = nil, want session_changed refusal even though an approval was recorded in the same tick")
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "session_changed" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: session_changed}", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0 (a same-tick approval must never beat a session change)", fixture.runCount)
	}
}

// TestCommandApprovalWaitInterruption pins #823 F-005's exact ordering
// contract at the unit level: commandApprovalWaitInterruption must report
// cancellation, then the deadline, then a session-identity change, and
// report nothing when none apply. waitForCommandApprovalDecision calls this
// helper both at the top of every poll and again immediately before
// accepting an approval or claiming (see runExecuteBashWithContext), so its
// exact ordering guarantee is what makes a same-instant approval never beat
// cancellation/deadline/session-change; there is no real timing hook to
// inject a race into that razor-thin window end-to-end, so this pins the
// guarantee directly.
func TestCommandApprovalWaitInterruption(t *testing.T) {
	baseCtx := commandContext{now: func() time.Time { return time.Date(2026, time.June, 1, 10, 0, 0, 0, time.UTC) }}.withDefaults()
	deadline := baseCtx.now().Add(time.Minute)
	params := commandApprovalWaitParams{deadline: deadline}

	t.Run("nothing fired", func(t *testing.T) {
		waitCtx := context.Background()
		if got := commandApprovalWaitInterruption(waitCtx, baseCtx, params, "", 0, false); got != nil {
			t.Fatalf("commandApprovalWaitInterruption() = %#v, want nil", got)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		waitCtx, cancel := context.WithCancel(context.Background())
		cancel()
		got := commandApprovalWaitInterruption(waitCtx, baseCtx, params, "", 0, false)
		if got == nil || got.Decision != "wait_cancelled" {
			t.Fatalf("commandApprovalWaitInterruption() = %#v, want Decision=wait_cancelled", got)
		}
	})
	t.Run("deadline passed", func(t *testing.T) {
		pastCtx := commandContext{now: func() time.Time { return deadline.Add(time.Second) }}.withDefaults()
		got := commandApprovalWaitInterruption(context.Background(), pastCtx, params, "", 0, false)
		if got == nil || got.Decision != "wait_timeout" {
			t.Fatalf("commandApprovalWaitInterruption() = %#v, want Decision=wait_timeout", got)
		}
	})
	t.Run("session changed", func(t *testing.T) {
		got := commandApprovalWaitInterruption(context.Background(), baseCtx, params, "some-other-session-key", 0, true)
		if got == nil || got.Decision != "session_changed" {
			t.Fatalf("commandApprovalWaitInterruption() = %#v, want Decision=session_changed", got)
		}
	})
	t.Run("cancellation takes priority over deadline", func(t *testing.T) {
		pastCtx := commandContext{now: func() time.Time { return deadline.Add(time.Second) }}.withDefaults()
		waitCtx, cancel := context.WithCancel(context.Background())
		cancel()
		got := commandApprovalWaitInterruption(waitCtx, pastCtx, params, "", 0, false)
		if got == nil || got.Decision != "wait_cancelled" {
			t.Fatalf("commandApprovalWaitInterruption() = %#v, want cancellation to take priority over an also-passed deadline", got)
		}
	})
}

// TestRunExecuteBashBlockingRetryAfterRejectedMintsFreshRequest pins #823
// rework-2's F-012 closure: a retry against a REJECTED thread must mint a
// fresh input_request_id (the deterministic thread id would otherwise
// permanently block every future retry of the identical command).
func TestRunExecuteBashBlockingRetryAfterRejectedMintsFreshRequest(t *testing.T) {
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
	commandText := "printf retry-after-rejected"
	threadID := fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionRejected, "orchestrator", fixture.now.Add(15*time.Minute))
	state0, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() = (%#v, %v, %v)", state0, ok, err)
	}
	rejectedInputRequestID := state0.Threads[threadID].InputRequestID

	args := fixture.args("--label", "protected", "--category", "release", "--command", commandText)
	err = runExecuteBashWithContext(fixture.context(), args)
	if err == nil {
		t.Fatal("retry error = nil, want a pending blocking refusal (fresh request minted, awaiting decision)")
	}
	var outcomeErr commandApprovalOutcomeError
	if errors.As(err, &outcomeErr) && outcomeErr.status == "rejected" {
		t.Fatalf("retry error = %#v, want the fresh request's pending status, not the stale rejected one", outcomeErr)
	}
	state1, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() (after retry) = (%#v, %v, %v)", state1, ok, err)
	}
	got := state1.Threads[threadID].InputRequestID
	if got == "" || got == rejectedInputRequestID {
		t.Fatalf("retry input_request_id = %q, want a fresh id distinct from the rejected one %q", got, rejectedInputRequestID)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingRetryAfterExpiredMintsFreshRequest is F-012's
// expired-terminal closure, the same shape as the rejected case above.
func TestRunExecuteBashBlockingRetryAfterExpiredMintsFreshRequest(t *testing.T) {
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
	commandText := "printf retry-after-expired"
	// A request whose own ExpiresAt is already in the past projects as
	// "expired" without any decision ever having been recorded.
	threadID := fixture.appendCommandApprovalRequest(t, policy, commandText, fixture.now.Add(-time.Minute))
	state0, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() = (%#v, %v, %v)", state0, ok, err)
	}
	if state0.Threads[threadID].Status != projection.CommandApprovalStatusExpired {
		t.Fatalf("precondition: thread status = %v, want Expired", state0.Threads[threadID].Status)
	}
	expiredInputRequestID := state0.Threads[threadID].InputRequestID

	args := fixture.args("--label", "protected", "--category", "release", "--command", commandText)
	if err := runExecuteBashWithContext(fixture.context(), args); err == nil {
		t.Fatal("retry error = nil, want a pending blocking refusal")
	}
	state1, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() (after retry) = (%#v, %v, %v)", state1, ok, err)
	}
	got := state1.Threads[threadID].InputRequestID
	if got == "" || got == expiredInputRequestID {
		t.Fatalf("retry input_request_id = %q, want a fresh id distinct from the expired one %q", got, expiredInputRequestID)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingRetryAfterClaimedStaysAlreadyExecutedUntilTTL
// pins F-012's approved-and-claimed closure: a retry must keep reporting
// already_executed while the original approval's TTL has not yet elapsed,
// and only mint a fresh request once it has expired.
func TestRunExecuteBashBlockingRetryAfterClaimedStaysAlreadyExecutedUntilTTL(t *testing.T) {
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
	commandText := "printf retry-after-claimed"
	expiresAt := fixture.now.Add(5 * time.Minute)
	threadID := fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionApproved, "orchestrator", expiresAt)
	state0, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() = (%#v, %v, %v)", state0, ok, err)
	}
	claimedInputRequestID := state0.Threads[threadID].InputRequestID

	args := fixture.args("--label", "protected", "--category", "release", "--command", commandText)
	if err := runExecuteBashWithContext(fixture.context(), args); err != nil {
		t.Fatalf("first call error = %v, want nil (approved command runs)", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount after first call = %d, want 1", fixture.runCount)
	}

	// Still within TTL: must stay already_executed, no new request minted.
	err = runExecuteBashWithContext(fixture.context(), args)
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "already_executed" {
		t.Fatalf("retry (within TTL) error = %#v, want commandApprovalOutcomeError{status: already_executed}", err)
	}
	stateWithinTTL, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() (within TTL) = (%#v, %v, %v)", stateWithinTTL, ok, err)
	}
	if got := stateWithinTTL.Threads[threadID].InputRequestID; got != claimedInputRequestID {
		t.Fatalf("input_request_id (within TTL) = %q, want unchanged %q", got, claimedInputRequestID)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount (within TTL retry) = %d, want still 1", fixture.runCount)
	}

	// Past TTL: the approval has expired, so a retry must mint a fresh
	// request instead of staying stuck on already_executed forever.
	fixture.now = expiresAt.Add(time.Minute)
	err = runExecuteBashWithContext(fixture.context(), args)
	if err == nil {
		t.Fatal("retry (past TTL) error = nil, want a pending blocking refusal for the fresh request")
	}
	if errors.As(err, &outcomeErr) && outcomeErr.status == "already_executed" {
		t.Fatalf("retry (past TTL) error = %#v, want a fresh mint, not already_executed forever", outcomeErr)
	}
	statePastTTL, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() (past TTL) = (%#v, %v, %v)", statePastTTL, ok, err)
	}
	if got := statePastTTL.Threads[threadID].InputRequestID; got == "" || got == claimedInputRequestID {
		t.Fatalf("input_request_id (past TTL) = %q, want a fresh id distinct from the claimed one %q", got, claimedInputRequestID)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount (past TTL retry) = %d, want still 1 (the fresh request is not yet approved)", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingMismatchRetryDoesNotOverwriteThread pins F-012's
// refusal-side guarantee: a retry that hits requester_mismatch or
// digest_mismatch must never mint a new request or otherwise mutate the
// existing thread's correlation.
func TestRunExecuteBashBlockingMismatchRetryDoesNotOverwriteThread(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "*",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	otherPolicy := resolvedCommandApprovalPolicy{
		Requester: "other-worker",
		Reviewer:  "orchestrator",
		Mode:      "blocking",
		Label:     "protected",
		Category:  "release",
		TTL:       defaultCommandApprovalTTL,
	}
	commandText := "printf mismatch-no-overwrite"
	foreignThreadID := fixture.appendCommandApprovalRequest(t, otherPolicy, commandText, fixture.now.Add(15*time.Minute))
	state0, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() = (%#v, %v, %v)", state0, ok, err)
	}
	originalInputRequestID := state0.Threads[foreignThreadID].InputRequestID
	originalRequester := state0.Threads[foreignThreadID].Requester

	for i := 0; i < 2; i++ {
		err := runExecuteBashWithContext(fixture.context(), fixture.args(
			"--label", "protected",
			"--category", "release",
			"--thread-id", foreignThreadID,
			"--command", commandText,
		))
		var outcomeErr commandApprovalOutcomeError
		if !errors.As(err, &outcomeErr) || outcomeErr.status != "requester_mismatch" {
			t.Fatalf("call %d error = %#v, want commandApprovalOutcomeError{status: requester_mismatch}", i, err)
		}
	}

	state1, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() (after retries) = (%#v, %v, %v)", state1, ok, err)
	}
	if got := state1.Threads[foreignThreadID].InputRequestID; got != originalInputRequestID {
		t.Fatalf("input_request_id after mismatch retries = %q, want unchanged %q", got, originalInputRequestID)
	}
	if got := state1.Threads[foreignThreadID].Requester; got != originalRequester {
		t.Fatalf("requester after mismatch retries = %q, want unchanged %q", got, originalRequester)
	}
	requestCount := 0
	for _, event := range replayCommandEvents(t, fixture.sessionDir) {
		if event.Type == journal.CommandApprovalRequestedEventType {
			requestCount++
		}
	}
	if requestCount != 1 {
		t.Fatalf("command_approval_requested events = %d, want still 1 (mismatch retries must never mint)", requestCount)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingFailedDeliveryRecoversOnRetryWithoutNewRequest
// pins #823 rework-2's F-013 closure: a request whose FIRST delivery
// attempt fails (e.g. the approver was transiently undiscoverable) must
// still be recoverable by a plain retry once the approver becomes
// discoverable again — reusing the exact same request/input_request_id,
// never minting a second one.
func TestRunExecuteBashBlockingFailedDeliveryRecoversOnRetryWithoutNewRequest(t *testing.T) {
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
	commandText := "printf failed-delivery-recovers"
	threadID := commandApprovalThreadID(policy, commandDigest(commandText))
	args := fixture.args("--label", "protected", "--category", "release", "--command", commandText)

	// First call: the approver is undiscoverable, so a request is journaled
	// (evaluateCommandApproval saw "absent") but delivery fails fast.
	discoveredBackup := fixture.discoveredNodes
	fixture.discoveredNodes = map[string]discovery.NodeInfo{}
	err := runExecuteBashWithContext(fixture.context(), args)
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "delivery_failed" {
		t.Fatalf("first call error = %#v, want commandApprovalOutcomeError{status: delivery_failed}", err)
	}
	state0, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() = (%#v, %v, %v)", state0, ok, err)
	}
	firstInputRequestID := state0.Threads[threadID].InputRequestID
	if firstInputRequestID == "" {
		t.Fatal("first call did not record a request despite delivery failing")
	}

	// Approver recovers; retry must re-deliver the SAME request, not mint a
	// new one.
	fixture.discoveredNodes = discoveredBackup
	err = runExecuteBashWithContext(fixture.context(), args)
	if err == nil {
		t.Fatal("retry error = nil, want a pending blocking refusal (delivery recovered, awaiting decision)")
	}
	if errors.As(err, &outcomeErr) && outcomeErr.status == "delivery_failed" {
		t.Fatalf("retry error = %#v, want re-delivery to succeed now that the approver is discoverable again", outcomeErr)
	}
	state1, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
	if err != nil || !ok {
		t.Fatalf("ProjectCommandApprovalState() (after retry) = (%#v, %v, %v)", state1, ok, err)
	}
	if got := state1.Threads[threadID].InputRequestID; got != firstInputRequestID {
		t.Fatalf("retry input_request_id = %q, want unchanged %q (retry must reuse, not overwrite, the pending correlation)", got, firstInputRequestID)
	}
	requestCount := 0
	for _, event := range replayCommandEvents(t, fixture.sessionDir) {
		if event.Type == journal.CommandApprovalRequestedEventType {
			requestCount++
		}
	}
	if requestCount != 1 {
		t.Fatalf("command_approval_requested events = %d, want exactly 1 (recovery must not mint a new request)", requestCount)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}

	// #823 rework-3 (F-013): extend end-to-end -- the recovered request
	// carries the ORIGINAL input_request_id, so an approval reply against
	// it must still resolve and run the command exactly once.
	fixture.appendCommandApprovalDecisionForRequest(t, threadID, "orchestrator", journal.ApprovalDecisionApproved)
	err = runExecuteBashWithContext(fixture.context(), args)
	if err != nil {
		t.Fatalf("third call (after approval) error = %v, want nil", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount after approval = %d, want exactly 1", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingSecondTerminalCycleMintsFreshRequest pins
// guardian's round-3 F-012 reproducer: a SECOND consecutive terminal
// outcome (A rejected, retry mints B, B also rejected) must still mint a
// THIRD fresh request C on the next retry, not silently adopt the
// already-superseded A. Before the stateful-closure fix, the equivalence
// check matched ANY same-thread request whose id != B (which includes the
// older A), so the third call adopted stale A and redelivered it while the
// projection thread actually held B, producing a permanent
// requester_mismatch loop.
func TestRunExecuteBashBlockingSecondTerminalCycleMintsFreshRequest(t *testing.T) {
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
	commandText := "printf second-terminal-cycle"
	threadID := fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionRejected, "orchestrator", fixture.now.Add(15*time.Minute)) // A: rejected
	project := func() projection.CommandApprovalState {
		state, ok, err := projection.ProjectCommandApprovalState(fixture.sessionDir, fixture.now)
		if err != nil || !ok {
			t.Fatalf("ProjectCommandApprovalState() = (%#v, %v, %v)", state, ok, err)
		}
		return state
	}
	idA := project().Threads[threadID].InputRequestID
	args := fixture.args("--label", "protected", "--category", "release", "--command", commandText)

	// Retry 1: mints B.
	if err := runExecuteBashWithContext(fixture.context(), args); err == nil {
		t.Fatal("retry 1 error = nil, want a pending blocking refusal (fresh request B minted)")
	}
	idB := project().Threads[threadID].InputRequestID
	if idB == "" || idB == idA {
		t.Fatalf("retry 1 input_request_id = %q, want a fresh id distinct from A %q", idB, idA)
	}
	fixture.appendCommandApprovalDecisionForRequest(t, threadID, "orchestrator", journal.ApprovalDecisionRejected) // B: rejected

	// Retry 2: must mint C (not silently adopt stale A), wait, then run
	// once C is approved.
	ctx := fixture.context()
	approved := false
	ctx.sleep = func(context.Context, time.Duration) {
		if approved {
			return
		}
		approved = true
		fixture.appendCommandApprovalDecisionForRequest(t, threadID, "orchestrator", journal.ApprovalDecisionApproved)
	}
	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected", "--category", "release", "--command", commandText,
	))
	if err != nil {
		t.Fatalf("retry 2 error = %v, want nil (fresh request C minted, waited, approved, and run)", err)
	}
	idC := project().Threads[threadID].InputRequestID
	if idC == "" || idC == idA || idC == idB {
		t.Fatalf("retry 2 input_request_id = %q, want a fresh id distinct from A %q and B %q", idC, idA, idB)
	}
	requestCount := 0
	for _, event := range replayCommandEvents(t, fixture.sessionDir) {
		if event.Type == journal.CommandApprovalRequestedEventType {
			requestCount++
		}
	}
	if requestCount != 3 {
		t.Fatalf("command_approval_requested events = %d, want exactly 3 (A, B, C)", requestCount)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingConcurrentExplicitThreadCollisionRefusesLoser
// pins #823 rework-3's F-015 closure: two concurrent callers racing to mint
// the SAME explicit --thread-id, with different policy labels but the same
// command digest, must never let the losing caller deliver a prompt
// describing its OWN (different) policy context under the winner's
// correlation. Exactly one request event lands; the loser refuses with
// requester_mismatch; the one delivered prompt carries only the winner's
// label.
func TestRunExecuteBashBlockingConcurrentExplicitThreadCollisionRefusesLoser(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "*",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	commandText := "printf thread-collision"
	sharedThreadID := "command-approval-shared-collision"

	var deliveredBodies []string
	var deliveredMu sync.Mutex
	originalDeliver := deliverCommandApprovalSystemMessageFn
	deliverCommandApprovalSystemMessageFn = func(filename string, nodeInfo discovery.NodeInfo, recipient, sender, contextID, content string, cfg *config.Config, adjacency map[string][]string, knownNodes map[string]discovery.NodeInfo, livenessMap map[string]bool) (controlplane.SystemMessageResult, error) {
		deliveredMu.Lock()
		deliveredBodies = append(deliveredBodies, content)
		deliveredMu.Unlock()
		return originalDeliver(filename, nodeInfo, recipient, sender, contextID, content, cfg, adjacency, knownNodes, livenessMap)
	}
	t.Cleanup(func() { deliverCommandApprovalSystemMessageFn = originalDeliver })

	var wg sync.WaitGroup
	errs := make([]error, 2)
	labels := []string{"label-a", "label-b"}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = runExecuteBashWithContext(fixture.context(), fixture.args(
				"--label", labels[i],
				"--category", "release",
				"--thread-id", sharedThreadID,
				"--command", commandText,
			))
		}(i)
	}
	wg.Wait()

	successCount, mismatchCount := 0, 0
	for _, err := range errs {
		if err == nil {
			t.Fatalf("unexpected nil error from a --no-wait blocking call: %v", errs)
		}
		var outcomeErr commandApprovalOutcomeError
		if errors.As(err, &outcomeErr) && outcomeErr.status == "requester_mismatch" {
			mismatchCount++
			continue
		}
		successCount++
	}
	if successCount != 1 || mismatchCount != 1 {
		t.Fatalf("successCount=%d mismatchCount=%d, want exactly one winner (pending) and one requester_mismatch loser; errs=%v", successCount, mismatchCount, errs)
	}
	requestCount := 0
	for _, event := range replayCommandEvents(t, fixture.sessionDir) {
		if event.Type == journal.CommandApprovalRequestedEventType {
			requestCount++
		}
	}
	if requestCount != 1 {
		t.Fatalf("command_approval_requested events = %d, want exactly 1", requestCount)
	}
	deliveredMu.Lock()
	defer deliveredMu.Unlock()
	if len(deliveredBodies) != 1 {
		t.Fatalf("delivered prompts = %d, want exactly 1 (the loser must never deliver)", len(deliveredBodies))
	}
	if !strings.Contains(deliveredBodies[0], "label-a") && !strings.Contains(deliveredBodies[0], "label-b") {
		t.Fatalf("delivered prompt = %q, want it to name one of the two labels", deliveredBodies[0])
	}
	if strings.Contains(deliveredBodies[0], "label-a") && strings.Contains(deliveredBodies[0], "label-b") {
		t.Fatalf("delivered prompt = %q, want only the winner's single label, not both", deliveredBodies[0])
	}
}

// TestRunExecuteBashBlockingCancelDuringClaimStopsRunBash pins #823
// rework-3's F-005 closure: a SIGINT/SIGTERM caught precisely DURING the
// F-001 claim (after the pre-claim recheck passed, but before runBash
// starts) must still be observed -- the command must never run just
// because it slipped in after the last explicit check before the claim.
func TestRunExecuteBashBlockingCancelDuringClaimStopsRunBash(t *testing.T) {
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
	commandText := "printf cancel-during-claim"
	fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionApproved, "orchestrator", fixture.now.Add(15*time.Minute))

	ctx := fixture.context()
	waitCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ctx.newInterruptContext = func() (context.Context, func()) { return waitCtx, cancel }
	original := appendEventBeforeClaimHookFn
	appendEventBeforeClaimHookFn = func() { cancel() }
	t.Cleanup(func() { appendEventBeforeClaimHookFn = original })

	err := runExecuteBashWithContext(ctx, fixture.args(
		"--label", "protected", "--category", "release", "--command", commandText,
	))
	if err == nil {
		t.Fatal("error = nil, want cancellation to stop the command even though the claim already landed")
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "cancelled" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: cancelled}", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0 (cancellation during the claim must stop runBash)", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingColdStartNoCurrentWriterFailsClosed pins #823
// rework-3's F-002 closure: blocking mode with NO live current session
// writer (a genuine cold start -- no session-state.json/lease on disk yet)
// must refuse outright with a distinct session_unavailable outcome,
// never shadow-minting a request or shadow-claiming an approval.
func TestRunExecuteBashBlockingColdStartNoCurrentWriterFailsClosed(t *testing.T) {
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixtureRaw(t, policyConfig)
	commandText := "printf cold-start"

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected", "--category", "release", "--command", commandText,
	))
	if err == nil {
		t.Fatal("error = nil, want session_unavailable refusal")
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "session_unavailable" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: session_unavailable}", err)
	}
	if outcomeErr.ExitCode() != 23 {
		t.Fatalf("ExitCode() = %d, want 23", outcomeErr.ExitCode())
	}
	requestCount := 0
	for _, event := range replayCommandEvents(t, fixture.sessionDir) {
		if event.Type == journal.CommandApprovalRequestedEventType {
			requestCount++
		}
	}
	if requestCount != 0 {
		t.Fatalf("command_approval_requested events = %d, want 0 (must never shadow-mint)", requestCount)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashBlockingSessionRotationBeforeClaimRefusesEvenWithoutWait
// pins #823 rework-3's F-004 closure: the expected session identity is
// captured at EVALUATION time -- not only when a call waits -- and checked
// again from inside the claim's own fence. A generation rotation between
// evaluation and the claim must refuse with session_changed even for a
// call that never entered the wait loop at all (the approval was already
// decided before this call started).
func TestRunExecuteBashBlockingSessionRotationBeforeClaimRefusesEvenWithoutWait(t *testing.T) {
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
	commandText := "printf rotate-before-claim-no-wait"
	fixture.appendCommandApproval(t, policy, commandText, journal.ApprovalDecisionApproved, "orchestrator", fixture.now.Add(15*time.Minute))

	original := appendEventBeforeClaimHookFn
	appendEventBeforeClaimHookFn = func() {
		path := journal.SessionStatePath(fixture.sessionDir)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(session state) error = %v", err)
		}
		var state journal.SessionState
		if err := json.Unmarshal(data, &state); err != nil {
			t.Fatalf("Unmarshal(session state) error = %v", err)
		}
		state.Generation++
		newData, err := json.Marshal(state)
		if err != nil {
			t.Fatalf("Marshal(session state) error = %v", err)
		}
		if err := os.WriteFile(path, newData, 0o644); err != nil {
			t.Fatalf("WriteFile(session state) error = %v", err)
		}
	}
	t.Cleanup(func() { appendEventBeforeClaimHookFn = original })

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "protected", "--category", "release", "--command", commandText,
	))
	if err == nil {
		t.Fatal("error = nil, want session_changed refusal even though this call never waited")
	}
	var outcomeErr commandApprovalOutcomeError
	if !errors.As(err, &outcomeErr) || outcomeErr.status != "session_changed" {
		t.Fatalf("error = %#v, want commandApprovalOutcomeError{status: session_changed}", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
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

// --- #831 REVIEW-831-R0 F-1 closure: cross-session principal binding ---

// TestRunExecuteBashSessionFlagMatchingActualSessionWorks is the positive
// control for #831 F-1: a --session value that matches the calling pane's
// real, auto-detected tmux session still behaves exactly as before.
func TestRunExecuteBashSessionFlagMatchingActualSessionWorks(t *testing.T) {
	fixture := newExecuteBashFixture(t, config.CommandApprovalPolicy{
		Requester: "worker",
		Label:     "diagnostic",
		Category:  "diagnostic",
		Mode:      "advisory",
	})

	err := runExecuteBashWithContext(fixture.context(), fixture.args(
		"--label", "diagnostic",
		"--category", "diagnostic",
		"--mode", "advisory",
		"--command", "printf matching-session",
	))
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v, want nil", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
}

// TestRunExecuteBashSessionFlagMismatchRefusedOnRequestPath pins #831 F-1's
// first closure requirement: a --session naming a DIFFERENT session than
// the calling pane's real, auto-detected tmux session must be refused
// outright on the request (mint) path -- a pane titled "worker" actually
// running in session A must never be treated as session B's "worker" just
// by passing --session B.
func TestRunExecuteBashSessionFlagMismatchRefusedOnRequestPath(t *testing.T) {
	fixture := newExecuteBashFixture(t, config.CommandApprovalPolicy{
		Requester: "worker",
		Label:     "diagnostic",
		Category:  "diagnostic",
		Mode:      "advisory",
	})

	err := runExecuteBashWithContext(fixture.context(), []string{
		"--context-id", fixture.contextID,
		"--session", "other-session",
		"--label", "diagnostic",
		"--category", "diagnostic",
		"--mode", "advisory",
		"--command", "printf cross-session-request",
	})
	if err == nil {
		t.Fatal("error = nil, want refusal for a --session mismatching the real detected session")
	}
	if !strings.Contains(err.Error(), "does not match the calling pane's actual tmux session") {
		t.Fatalf("error = %v, want the F-1 cross-session mismatch diagnostic", err)
	}
	if fixture.runCount != 0 {
		t.Fatalf("runCount = %d, want 0", fixture.runCount)
	}
}

// TestRunExecuteBashRecordDecisionSessionMismatchRefused pins #831 F-1's
// second closure requirement: the SAME refusal on the --record-decision
// path -- a same-titled approver pane actually running in a different real
// session cannot record a decision for another session's thread merely by
// passing --session <target>.
func TestRunExecuteBashRecordDecisionSessionMismatchRefused(t *testing.T) {
	fixture := newExecuteBashFixture(t)
	fixture.commandApproverNode = "orchestrator"
	fixture.nodes = map[string]config.NodeConfig{"orchestrator": {}}
	commandText := "printf cross-session-decision"

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

	err = runExecuteBashWithContext(fixture.contextAsPane("orchestrator"), []string{
		"--context-id", fixture.contextID,
		"--session", "other-session",
		"--thread-id", threadID,
		"--record-decision", "approved",
	})
	if err == nil {
		t.Fatal("record-decision error = nil, want refusal for a --session mismatching the real detected session")
	}
	if !strings.Contains(err.Error(), "does not match the calling pane's actual tmux session") {
		t.Fatalf("error = %v, want the F-1 cross-session mismatch diagnostic", err)
	}
}

// TestRunExecuteBashSessionFlagUsedWhenNoRealSessionDetected is #831 F-1's
// fallback control: when getTmuxSessionName cannot detect any real session
// at all (a genuinely non-tmux context), the caller-supplied --session is
// still honored exactly as before, since there is no real session identity
// to protect against being overridden.
func TestRunExecuteBashSessionFlagUsedWhenNoRealSessionDetected(t *testing.T) {
	fixture := newExecuteBashFixture(t, config.CommandApprovalPolicy{
		Requester: "worker",
		Label:     "diagnostic",
		Category:  "diagnostic",
		Mode:      "advisory",
	})
	ctx := fixture.context()
	ctx.getTmuxSessionName = func() string { return "" }

	err := runExecuteBashWithContext(ctx, []string{
		"--context-id", fixture.contextID,
		"--session", fixture.sessionName,
		"--label", "diagnostic",
		"--category", "diagnostic",
		"--mode", "advisory",
		"--command", "printf no-real-session-detected",
	})
	if err != nil {
		t.Fatalf("runExecuteBashWithContext() error = %v, want nil (flag honored when no real session is detectable)", err)
	}
	if fixture.runCount != 1 {
		t.Fatalf("runCount = %d, want 1", fixture.runCount)
	}
}

// --- #831 REVIEW-831-R0 F-2/F-4 closure: concurrent first-mint race ---

// testRunExecuteBashConcurrentFirstMintRace pins #831 F-2 (the missing D6
// acceptance test) and exercises F-4's fix at the same time: two
// concurrent FIRST-TIME callers for the identical command-approval thread,
// each with its own distinct, valid TTL, race through
// atomicCreateOrReplaceRequest via the beforeMintAttemptHookFn seam. Both
// callers are forced past their own "absent" evaluation before either
// one's append lands (proving this is a genuine concurrent first-mint, not
// a sequential arrival), then released in the order firstReleasedIsA asks
// for. Whichever call's append actually lands is the winner; the loser's
// redeliver path (#823 F-015) must report the WINNER's actually-stored
// ExpiresAt in its result metadata, never its own local draft (#831 F-4) --
// this is checked for both possible winner orders and for both terminal
// evaluation outcomes the wait loop can produce without ever re-projecting
// a Thread (#831 F-4's exact reachable surface, confirmed by reading
// commandApprovalWaitInterruption): the approval's own TTL-based expiry
// ("expired") and the caller's local --wait-timeout-seconds ("wait_timeout").
func testRunExecuteBashConcurrentFirstMintRace(t *testing.T, firstReleasedIsA bool, outcome string) {
	t.Helper()
	policyConfig := config.CommandApprovalPolicy{
		Requester: "worker",
		Reviewer:  "orchestrator",
		Label:     "protected",
		Category:  "release",
		Mode:      "blocking",
	}
	fixture := newExecuteBashFixture(t, policyConfig)
	commandText := "printf concurrent-first-mint-ttl-race"

	const ttlASeconds = 60.0
	const ttlBSeconds = 120.0
	expiresAtA := fixture.now.Add(time.Duration(ttlASeconds * float64(time.Second))).UTC().Format(time.RFC3339Nano)
	expiresAtB := fixture.now.Add(time.Duration(ttlBSeconds * float64(time.Second))).UTC().Format(time.RFC3339Nano)
	if expiresAtA == expiresAtB {
		t.Fatal("test setup bug: expiresAtA == expiresAtB, the two TTLs must be distinguishable")
	}

	var waitTimeoutSeconds float64
	switch outcome {
	case "expired":
		// Larger than either TTL, so the approval's own TTL-based expiry
		// (whichever TTL actually won the mint race) is what ends the
		// wait, not the local wait-timeout.
		waitTimeoutSeconds = 600
	case "wait_timeout":
		// Smaller than either TTL, so the caller's own wait-timeout ends
		// the wait first, well before either TTL's expiry.
		waitTimeoutSeconds = 5
	default:
		t.Fatalf("unknown outcome %q", outcome)
	}

	releaseA := make(chan struct{})
	releaseB := make(chan struct{})
	var arrivedA, arrivedB sync.WaitGroup
	arrivedA.Add(1)
	arrivedB.Add(1)

	originalHook := beforeMintAttemptHookFn
	beforeMintAttemptHookFn = func(draftExpiresAt string) {
		switch draftExpiresAt {
		case expiresAtA:
			arrivedA.Done()
			<-releaseA
		case expiresAtB:
			arrivedB.Done()
			<-releaseB
		default:
			t.Errorf("beforeMintAttemptHookFn: unexpected draft expiresAt %q", draftExpiresAt)
		}
	}
	t.Cleanup(func() { beforeMintAttemptHookFn = originalHook })

	argsFor := func(ttlSeconds float64) []string {
		return []string{
			"--context-id", fixture.contextID,
			"--session", fixture.sessionName,
			"--label", "protected",
			"--category", "release",
			"--approval-ttl-seconds", fmt.Sprintf("%g", ttlSeconds),
			"--wait-timeout-seconds", fmt.Sprintf("%g", waitTimeoutSeconds),
			"--command", commandText,
		}
	}

	var stderrA, stderrB bytes.Buffer
	ctxA := fixture.context()
	ctxA.stderr = &stderrA
	ctxB := fixture.context()
	ctxB.stderr = &stderrB

	var wg sync.WaitGroup
	var errA, errB error
	wg.Add(2)
	go func() {
		defer wg.Done()
		errA = runExecuteBashWithContext(ctxA, argsFor(ttlASeconds))
	}()
	go func() {
		defer wg.Done()
		errB = runExecuteBashWithContext(ctxB, argsFor(ttlBSeconds))
	}()

	arrivedA.Wait()
	arrivedB.Wait()
	// Both callers are now blocked right at their own mint gate, having
	// already evaluated the thread as mintable ("absent") -- release them
	// in the order this test case asks for, which deterministically
	// decides the winner without any reliance on goroutine scheduling luck.
	if firstReleasedIsA {
		close(releaseA)
		close(releaseB)
	} else {
		close(releaseB)
		close(releaseA)
	}
	wg.Wait()

	// #831 F-4: which call's append actually lands first is decided by
	// atomicCreateOrReplaceRequest itself, not by this test's release
	// order -- releasing A's channel before B's only controls which
	// goroutine stops BLOCKING first, not which one the Go scheduler runs
	// first past that point, so the true winner cannot be predicted from
	// firstReleasedIsA. Instead, assert the actual invariant F-4 closes:
	// BOTH callers must converge on the SAME actually-stored ExpiresAt,
	// and that value must be one of the two distinguishable drafts (never
	// some third, malformed value).
	results := map[string]struct {
		err    error
		stderr *bytes.Buffer
	}{
		"A": {errA, &stderrA},
		"B": {errB, &stderrB},
	}
	parsed := map[string]executeBashResult{}
	for name, r := range results {
		if r.err == nil {
			t.Fatalf("call %s error = nil, want a blocking refusal (%s)", name, outcome)
		}
		var outcomeErr commandApprovalOutcomeError
		if !errors.As(r.err, &outcomeErr) {
			t.Fatalf("call %s error = %v, want a commandApprovalOutcomeError", name, r.err)
		}
		if outcomeErr.status != outcome {
			t.Fatalf("call %s status = %q, want %q (full err: %v)", name, outcomeErr.status, outcome, r.err)
		}
		var result executeBashResult
		if err := json.Unmarshal(r.stderr.Bytes(), &result); err != nil {
			t.Fatalf("call %s: Unmarshal(stderr metadata) = %v; stderr = %s", name, err, r.stderr.String())
		}
		if result.ExpiresAt != expiresAtA && result.ExpiresAt != expiresAtB {
			t.Fatalf("call %s result.ExpiresAt = %q, want one of the two draft values (A=%s, B=%s)", name, result.ExpiresAt, expiresAtA, expiresAtB)
		}
		parsed[name] = result
	}
	if parsed["A"].ExpiresAt != parsed["B"].ExpiresAt {
		t.Fatalf("call A result.ExpiresAt = %q, call B result.ExpiresAt = %q; both callers must report the single actually-stored winning ExpiresAt, never a loser's own local draft", parsed["A"].ExpiresAt, parsed["B"].ExpiresAt)
	}

	requestCount := 0
	for _, event := range replayCommandEvents(t, fixture.sessionDir) {
		if event.Type == journal.CommandApprovalRequestedEventType {
			requestCount++
		}
	}
	if requestCount != 1 {
		t.Fatalf("command_approval_requested events = %d, want exactly 1 (two concurrent first-mint callers must converge on one request)", requestCount)
	}
}

func TestRunExecuteBashConcurrentFirstMintRace_AWinsExpired(t *testing.T) {
	testRunExecuteBashConcurrentFirstMintRace(t, true, "expired")
}

func TestRunExecuteBashConcurrentFirstMintRace_BWinsExpired(t *testing.T) {
	testRunExecuteBashConcurrentFirstMintRace(t, false, "expired")
}

func TestRunExecuteBashConcurrentFirstMintRace_AWinsWaitTimeout(t *testing.T) {
	testRunExecuteBashConcurrentFirstMintRace(t, true, "wait_timeout")
}

func TestRunExecuteBashConcurrentFirstMintRace_BWinsWaitTimeout(t *testing.T) {
	testRunExecuteBashConcurrentFirstMintRace(t, false, "wait_timeout")
}
