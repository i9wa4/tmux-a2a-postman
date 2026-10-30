package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func approvalResolver(role string) ApprovalResolver {
	return func(context.Context, Snapshot) (string, error) { return role, nil }
}

func approvalReply(t *testing.T, m Mail, req Message, hash, decision string) Message {
	t.Helper()
	claimStateMessage(t, m, "peer", req.ID)
	body, _ := json.Marshal(ApprovalDecision{1, hash, decision})
	return sendStateMessage(t, m, "peer", "worker", string(body), req.ID, req.InputRequestID, false)
}

func pendingApproval(t *testing.T, m Mail) Message {
	t.Helper()
	msgs := mustState(t, m, "peer")
	for _, o := range msgs.Inbound {
		raw, err := os.ReadFile(mailPath(m, "peer", "inbox", o.MessageID))
		if err != nil {
			continue
		}
		_ = raw
		var req Message
		err = m.Store.withView(context.Background(), mailRuntime("peer"), func(ws *os.Root, _ Snapshot) error {
			box, err := openOwnBox(ws, "peer", "inbox")
			if err != nil {
				return err
			}
			defer func(closeFn func() error) { _ = closeFn() }(box.Close)
			req, err = readMessage(box, o.MessageID+".md")
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return req
	}
	t.Fatal("no unread approval request")
	return Message{}
}

func requestCommand(t *testing.T, req Message) string {
	t.Helper()
	var payload ApprovalRequest
	if err := json.Unmarshal([]byte(req.Body), &payload); err != nil || payload.Version != 1 || payload.Command == "" {
		t.Fatal(payload, err)
	}
	return payload.Command
}

func TestExecutorVerifiedAbsenceRunsRealBashAndRecordsExit(t *testing.T) {
	m := mailFixture(t)
	var stdout, stderr bytes.Buffer
	e := Executor{Mail: m, Resolve: approvalResolver(""), Stdout: &stdout, Stderr: &stderr}
	result, err := e.Execute(context.Background(), mailRuntime("worker"), "printf 'protocol-output'; printf 'diagnostic' >&2; exit 7")
	if err != nil || result.Status != "exited" || result.ExitStatus == nil || *result.ExitStatus != 7 ||
		stdout.String() != "protocol-output" || stderr.String() != "diagnostic" || result.RequestID != "" {
		t.Fatal(result, err, stdout.String(), stderr.String())
	}
	raw, err := os.ReadFile(filepath.Join(m.Store.StateRoot, "repo", "agents", "worker", "executions", result.OperationID+".json"))
	var saved ExecutionResult
	if err != nil || json.Unmarshal(raw, &saved) != nil || saved.Status != "exited" || saved.ExitStatus == nil || *saved.ExitStatus != 7 {
		t.Fatal(string(raw), err)
	}
}

func TestExecutorLegacyLeaseSpansApprovalWaitAndRun(t *testing.T) {
	m := mailFixture(t)
	path := legacyLockPath(m.Store)
	e := Executor{Mail: m, Resolve: approvalResolver("peer")}
	e.wait = func(context.Context) error {
		if legacyTryExclusive(t, path) {
			t.Fatal("legacy daemon entered during approval wait")
		}
		req := pendingApproval(t, m)
		approvalReply(t, m, req, requestCommand(t, req), "approved")
		return nil
	}
	runs := 0
	e.run = func(context.Context, string, io.Writer, io.Writer) (int, error) {
		runs++
		if legacyTryExclusive(t, path) {
			t.Fatal("legacy daemon entered during Bash dispatch")
		}
		return 0, nil
	}
	result, err := e.Execute(context.Background(), mailRuntime("worker"), "probe")
	if err != nil || result.Status != "executed" || runs != 1 {
		t.Fatalf("execution result: %+v %v runs=%d", result, err, runs)
	}
	if !legacyTryExclusive(t, path) {
		t.Fatal("legacy daemon cannot enter after terminal audit and lease release")
	}
}

func TestExecutorRejectsLegacyExclusiveBeforeSideEffects(t *testing.T) {
	m := mailFixture(t)
	path := legacyLockPath(m.Store)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	release := holdLegacyExclusive(t, path)
	runs := 0
	e := Executor{Mail: m, Resolve: approvalResolver("")}
	e.run = func(context.Context, string, io.Writer, io.Writer) (int, error) { runs++; return 0, nil }
	result, err := e.Execute(context.Background(), mailRuntime("worker"), "probe")
	if !errors.Is(err, ErrBusy) || result.Status != "unavailable" || runs != 0 {
		t.Fatalf("legacy exclusive admitted execute: %+v %v runs=%d", result, err, runs)
	}
	if _, err := os.Stat(filepath.Join(m.Store.StateRoot, "repo")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy exclusion created workspace: %v", err)
	}
	release()
}

func TestExecutorApprovalRequestNoticeFailurePreservesExactWait(t *testing.T) {
	m := mailFixture(t)
	var notifiedID string
	rt := noticeRuntime{storedRuntime: mailRuntime("worker")}
	rt.notify = func(_ context.Context, to Binding, id string) error {
		if to.Role != "peer" {
			t.Fatalf("approval request notified wrong role: %+v", to)
		}
		notifiedID = id
		return errors.New("injected reviewer pane failure")
	}
	runs := 0
	e := Executor{Mail: m, Resolve: approvalResolver("peer")}
	e.wait = func(context.Context) error {
		req := pendingApproval(t, m)
		if req.ID != notifiedID {
			t.Fatalf("notice did not target committed request: %q, %q", notifiedID, req.ID)
		}
		approvalReply(t, m, req, requestCommand(t, req), "rejected")
		return nil
	}
	e.run = func(context.Context, string, io.Writer, io.Writer) (int, error) { runs++; return 0, nil }
	result, err := e.Execute(context.Background(), rt, "never run")
	if err == nil || result.Status != "rejected" || runs != 0 || result.Notice == nil ||
		result.Notice.Status != "unknown" || result.RequestID != notifiedID {
		t.Fatalf("notification failure changed exact approval outcome: %+v %v runs=%d", result, err, runs)
	}
	if _, err := os.Stat(mailPath(m, "peer", "read", notifiedID)); err != nil {
		t.Fatalf("reviewer request was lost after hint failure: %v", err)
	}
}

func TestExecutorExactSelectiveReplyAndUnlockedDispatch(t *testing.T) {
	m := mailFixture(t)
	calls := 0
	unrelated := sendStateMessage(t, m, "peer", "worker", "ordinary mail", "", "", false)
	var wrong Message
	e := Executor{Mail: m, Resolve: approvalResolver("peer")}
	e.wait = func(context.Context) error {
		req := pendingApproval(t, m)
		hash := requestCommand(t, req)
		claimStateMessage(t, m, "peer", req.ID)
		bad, _ := json.Marshal(ApprovalDecision{1, "different command", "approved"})
		wrong = sendStateMessage(t, m, "peer", "worker", string(bad), req.ID, req.InputRequestID, false)
		good, _ := json.Marshal(ApprovalDecision{1, hash, "approved"})
		sendStateMessage(t, m, "peer", "worker", string(good), req.ID, req.InputRequestID, false)
		return nil
	}
	e.run = func(ctx context.Context, command string, stdout, stderr io.Writer) (int, error) {
		calls++
		// This synchronous send would fail ErrBusy if Execute held its lock.
		_, err := m.Send(ctx, mailRuntime("worker"), "peer", "during bash", "", false)
		return 0, err
	}
	result, err := e.Execute(context.Background(), mailRuntime("worker"), "printf approved")
	if err != nil || result.Status != "executed" || calls != 1 || result.ReplyID == "" {
		t.Fatal(result, err, calls)
	}
	for _, id := range []string{unrelated.ID, wrong.ID} {
		if _, err := os.Stat(mailPath(m, "worker", "inbox", id)); err != nil {
			t.Fatal("drained unrelated/wrong mail", err)
		}
	}
}

func TestExecutorUnknownOrChangingReviewerNeverRuns(t *testing.T) {
	for _, scenario := range []string{"unknown", "unbound", "self", "lost"} {
		t.Run(scenario, func(t *testing.T) {
			m := mailFixture(t)
			calls := 0
			e := Executor{Mail: m, Resolve: approvalResolver("peer")}
			switch scenario {
			case "unknown":
				e.Resolve = func(context.Context, Snapshot) (string, error) { return "", ErrUnknown }
			case "unbound":
				e.Resolve = approvalResolver("missing")
			case "self":
				e.Resolve = approvalResolver("worker")
			case "lost":
				e.wait = func(context.Context) error { e.Resolve = approvalResolver(""); return nil }
				// The pinned resolver must observe a changing configuration.
				current := "peer"
				e.Resolve = func(context.Context, Snapshot) (string, error) { return current, nil }
				e.wait = func(context.Context) error { current = ""; return nil }
			}
			e.run = func(context.Context, string, io.Writer, io.Writer) (int, error) { calls++; return 0, nil }
			result, err := e.Execute(context.Background(), mailRuntime("worker"), "never")
			if err == nil || result.Status != "unavailable" || calls != 0 {
				t.Fatal(result, err, calls)
			}
		})
	}
}

func TestExecutorCancellationAndLateReplyCannotAuthorizeNewCall(t *testing.T) {
	m := mailFixture(t)
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	e := Executor{Mail: m, Resolve: approvalResolver("peer")}
	var old Message
	e.wait = func(context.Context) error { old = pendingApproval(t, m); cancel(); return context.Canceled }
	e.run = func(context.Context, string, io.Writer, io.Writer) (int, error) { calls++; return 0, nil }
	first, err := e.Execute(ctx, mailRuntime("worker"), "same command")
	if !errors.Is(err, context.Canceled) || first.Status != "cancelled" || calls != 0 {
		t.Fatal(first, err)
	}
	late := approvalReply(t, m, old, requestCommand(t, old), "approved")
	e.wait = func(context.Context) error {
		req := pendingApproval(t, m)
		if req.ID == old.ID || req.InputRequestID == old.InputRequestID {
			t.Fatal("reused cancelled request")
		}
		approvalReply(t, m, req, requestCommand(t, req), "rejected")
		return nil
	}
	second, err := e.Execute(context.Background(), mailRuntime("worker"), "same command")
	if err == nil || second.Status != "rejected" || calls != 0 || second.OperationID == first.OperationID {
		t.Fatal(second, err)
	}
	if _, err := os.Stat(mailPath(m, "worker", "inbox", late.ID)); err != nil {
		t.Fatal("late reply consumed by another call", err)
	}
}

func TestExecutorFixed300SecondDeadlineNoFallback(t *testing.T) {
	m := mailFixture(t)
	now := time.Now()
	calls := 0
	e := Executor{Mail: m, Resolve: approvalResolver("peer"), now: func() time.Time { return now }}
	e.wait = func(context.Context) error { now = now.Add(300 * time.Second); return nil }
	e.run = func(context.Context, string, io.Writer, io.Writer) (int, error) { calls++; return 0, nil }
	result, err := e.Execute(context.Background(), mailRuntime("worker"), "no fallback")
	if !errors.Is(err, context.DeadlineExceeded) || result.Status != "wait_timeout" || calls != 0 {
		t.Fatal(result, err, calls)
	}
}

func TestExecutorEqualConcurrentInvocationsHaveIndependentApprovals(t *testing.T) {
	// Nested execution keeps the first invocation live while an equal second
	// invocation obtains its own request/decision; equal commands share no approval.
	m := mailFixture(t)
	calls := 0
	outer := Executor{Mail: m, Resolve: approvalResolver("peer")}
	run := func(context.Context, string, io.Writer, io.Writer) (int, error) { calls++; return 0, nil }
	outer.run = run
	var second ExecutionResult
	outer.wait = func(context.Context) error {
		firstReq := pendingApproval(t, m)
		// Claim first request without filling it; the outer caller still waits.
		claimStateMessage(t, m, "peer", firstReq.ID)
		inner := Executor{Mail: m, Resolve: approvalResolver("peer"), run: run}
		inner.wait = func(context.Context) error {
			req := pendingApproval(t, m)
			if req.ID == firstReq.ID || req.InputRequestID == firstReq.InputRequestID {
				t.Fatal("shared request")
			}
			approvalReply(t, m, req, requestCommand(t, req), "approved")
			return nil
		}
		var err error
		second, err = inner.Execute(context.Background(), mailRuntime("worker"), "same")
		if err != nil || second.Status != "executed" {
			t.Fatal(second, err)
		}
		body, _ := json.Marshal(ApprovalDecision{1, requestCommand(t, firstReq), "approved"})
		sendStateMessage(t, m, "peer", "worker", string(body), firstReq.ID, firstReq.InputRequestID, false)
		return nil
	}
	first, err := outer.Execute(context.Background(), mailRuntime("worker"), "same")
	if err != nil || first.Status != "executed" || calls != 2 || first.RequestID == second.RequestID ||
		first.OperationID == second.OperationID || first.Command != second.Command {
		t.Fatal(first, second, err, calls)
	}
	// A third identical invocation must also obtain another approval.
	outer.wait = func(context.Context) error {
		req := pendingApproval(t, m)
		approvalReply(t, m, req, requestCommand(t, req), "approved")
		return nil
	}
	third, err := outer.Execute(context.Background(), mailRuntime("worker"), "same")
	if err != nil || third.Status != "executed" || calls != 3 || third.RequestID == first.RequestID || third.RequestID == second.RequestID {
		t.Fatal(third, err, calls)
	}
}

func TestExecutorAmbiguousRepliesFailClosedAndStayUnread(t *testing.T) {
	m := mailFixture(t)
	calls := 0
	e := Executor{Mail: m, Resolve: approvalResolver("peer")}
	var replies []Message
	e.wait = func(context.Context) error {
		req := pendingApproval(t, m)
		hash := requestCommand(t, req)
		claimStateMessage(t, m, "peer", req.ID)
		for _, decision := range []string{"approved", "rejected"} {
			body, _ := json.Marshal(ApprovalDecision{1, hash, decision})
			replies = append(replies, sendStateMessage(t, m, "peer", "worker", string(body), req.ID, req.InputRequestID, false))
		}
		return nil
	}
	e.run = func(context.Context, string, io.Writer, io.Writer) (int, error) { calls++; return 0, nil }
	result, err := e.Execute(context.Background(), mailRuntime("worker"), "ambiguous")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || calls != 0 || result.Status != "unavailable" {
		t.Fatal(result, err, calls)
	}
	for _, reply := range replies {
		if _, err := os.Stat(mailPath(m, "worker", "inbox", reply.ID)); err != nil {
			t.Fatal("consumed ambiguous decision", err)
		}
	}
}

func TestExecutorDispatchAuditFailureNeverRunsAndCompletionErrorNeverRetries(t *testing.T) {
	m := mailFixture(t)
	calls := 0
	e := Executor{Mail: m, Resolve: approvalResolver("")}
	// Initialize own paths, then make the audit location unsafe.
	_, err := m.Claim(context.Background(), mailRuntime("worker"), "")
	if err != nil {
		t.Fatal(err)
	}
	records := filepath.Join(m.Store.StateRoot, "repo", "agents", "worker", "executions")
	if err := os.WriteFile(records, []byte("unsafe"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.run = func(context.Context, string, io.Writer, io.Writer) (int, error) { calls++; return 0, nil }
	result, err := e.Execute(context.Background(), mailRuntime("worker"), "never")
	if err == nil || calls != 0 || result.Status != "unavailable" {
		t.Fatal(result, err, calls)
	}
	if err := os.Remove(records); err != nil {
		t.Fatal(err)
	}
	e.run = func(context.Context, string, io.Writer, io.Writer) (int, error) {
		calls++
		if err := os.Chmod(records, 0o755); err != nil {
			t.Fatal(err)
		}
		return 0, nil
	}
	result, err = e.Execute(context.Background(), mailRuntime("worker"), "once")
	if err != nil || calls != 1 || result.Status != "executed" || !strings.Contains(result.Reason, "audit completion unavailable") {
		t.Fatal(result, err, calls)
	}
}

func holdExecutionMutationLock(t *testing.T, m Mail) func() {
	t.Helper()
	held := make(chan struct{})
	released := make(chan struct{})
	done := make(chan struct{})
	var lockErr error
	go func() {
		lockErr = m.Store.WithVerified(context.Background(), mailRuntime("worker"), func(*os.Root, Snapshot) error {
			close(held)
			<-released
			return nil
		})
		close(done)
	}()
	select {
	case <-held:
	case <-done:
		t.Fatalf("could not hold workspace mutation lock: %v", lockErr)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out acquiring workspace mutation lock")
	}
	var once sync.Once
	release := func() {
		once.Do(func() { close(released) })
		<-done
		if lockErr != nil {
			t.Fatalf("release workspace mutation lock: %v", lockErr)
		}
	}
	t.Cleanup(release)
	return release
}

func TestExecutorPersistentAuditContentionExpiresWithoutExecution(t *testing.T) {
	m := mailFixture(t)
	release := holdExecutionMutationLock(t, m)
	runs := 0
	e := Executor{Mail: m, Resolve: approvalResolver("")}
	e.run = func(context.Context, string, io.Writer, io.Writer) (int, error) { runs++; return 0, nil }
	result, err := e.Execute(context.Background(), mailRuntime("worker"), "never")
	if !errors.Is(err, context.DeadlineExceeded) || result.Status != "unavailable" || runs != 0 {
		t.Fatalf("audit contention dispatched Bash: %+v, %v, runs=%d", result, err, runs)
	}
	release()
	if _, err := os.Stat(filepath.Join(m.Store.StateRoot, "repo", "agents", "worker", "executions", result.OperationID+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("contention wrote a dispatch audit: %v", err)
	}
}

func TestExecutorReleasedAuditContentionDispatchesOnce(t *testing.T) {
	m := mailFixture(t)
	release := holdExecutionMutationLock(t, m)
	runs, waits := 0, 0
	e := Executor{Mail: m, Resolve: approvalResolver("")}
	e.wait = func(context.Context) error {
		waits++
		release()
		return nil
	}
	e.run = func(context.Context, string, io.Writer, io.Writer) (int, error) { runs++; return 0, nil }
	result, err := e.Execute(context.Background(), mailRuntime("worker"), "once")
	if err != nil || result.Status != "executed" || runs != 1 || waits != 1 {
		t.Fatalf("released audit contention did not dispatch once: %+v, %v, runs=%d, waits=%d", result, err, runs, waits)
	}
	if saved := savedExecution(t, m, result); saved.Status != "executed" {
		t.Fatalf("incorrect final audit after one dispatch: %+v", saved)
	}
}

func savedExecution(t *testing.T, m Mail, result ExecutionResult) ExecutionResult {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(m.Store.StateRoot, "repo", "agents", "worker", "executions", result.OperationID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved ExecutionResult
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("command_hash")) {
		t.Fatal("digest machinery remains in audit", string(raw))
	}
	return saved
}

func TestExecutorTerminalRefusalsPersistWithoutDispatch(t *testing.T) {
	for _, scenario := range []string{"rejected", "wait_timeout", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			m := mailFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			now := time.Now()
			calls := 0
			e := Executor{Mail: m, Resolve: approvalResolver("peer"), now: func() time.Time { return now }}
			e.run = func(context.Context, string, io.Writer, io.Writer) (int, error) { calls++; return 0, nil }
			e.wait = func(context.Context) error {
				req := pendingApproval(t, m)
				switch scenario {
				case "rejected":
					approvalReply(t, m, req, requestCommand(t, req), "rejected")
				case "wait_timeout":
					now = now.Add(300 * time.Second)
				case "cancelled":
					cancel()
					return context.Canceled
				}
				return nil
			}
			result, err := e.Execute(ctx, mailRuntime("worker"), "immutable\ncommand")
			if err == nil || result.Status != scenario || calls != 0 {
				t.Fatal(result, err, calls)
			}
			saved := savedExecution(t, m, result)
			if saved.Status != scenario || saved.RequestID != result.RequestID || saved.InputRequestID != result.InputRequestID ||
				saved.Command != "immutable\ncommand" || saved.ExitStatus != nil || saved.Reason == "" {
				t.Fatal(saved, result)
			}
			// Persistence is an audit fact. Timeout/cancel does not fabricate an exact
			// reviewer reply or erase the file-derived unanswered mail obligation.
			state := mustState(t, m, "worker")
			expected := 1
			if scenario == "rejected" {
				expected = 0
			}
			if len(state.Outbound) != expected {
				t.Fatal(state)
			}
		})
	}
}

func TestExecutorDecisionRequiresLiteralCommandAndRejectsLegacyDigest(t *testing.T) {
	m := mailFixture(t)
	command := "printf 'exact\\n'; : \"日本語\""
	e := Executor{Mail: m, Resolve: approvalResolver("peer")}
	var legacy Message
	calls := 0
	e.wait = func(context.Context) error {
		req := pendingApproval(t, m)
		var fields map[string]any
		if err := json.Unmarshal([]byte(req.Body), &fields); err != nil || len(fields) != 2 || fields["command"] != command {
			t.Fatal("request has legacy digest or modified command", fields, err)
		}
		claimStateMessage(t, m, "peer", req.ID)
		legacy = sendStateMessage(t, m, "peer", "worker", `{"version":1,"command_hash":"sha256:legacy","decision":"approved"}`, req.ID, req.InputRequestID, false)
		body, _ := json.Marshal(ApprovalDecision{1, command, "approved"})
		sendStateMessage(t, m, "peer", "worker", string(body), req.ID, req.InputRequestID, false)
		return nil
	}
	e.run = func(_ context.Context, actual string, _, _ io.Writer) (int, error) {
		calls++
		if actual != command {
			t.Fatal(actual)
		}
		return 0, nil
	}
	result, err := e.Execute(context.Background(), mailRuntime("worker"), command)
	if err != nil || calls != 1 || result.Status != "executed" {
		t.Fatal(result, err, calls)
	}
	if _, err := os.Stat(mailPath(m, "worker", "inbox", legacy.ID)); err != nil {
		t.Fatal(err)
	}
	savedExecution(t, m, result)
}

// Only this writer observes the process output; readiness is synchronized rather
// than inferred from a fixed sleep. The PID is used solely for fixture cleanup.
type executionChildWriter struct {
	ready   chan int
	pending string
}

func (w *executionChildWriter) Write(p []byte) (int, error) {
	w.pending += string(p)
	if i := strings.IndexByte(w.pending, '\n'); i >= 0 {
		pid, err := strconv.Atoi(w.pending[:i])
		if err != nil {
			return 0, err
		}
		select {
		case w.ready <- pid:
		default:
		}
		w.pending = w.pending[i+1:]
	}
	return len(p), nil
}

func TestExecutorCancellationKillsOwnedProcessGroupAndRecordsTerminal(t *testing.T) {
	m := mailFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan int, 1)
	output := &executionChildWriter{ready: ready}
	e := Executor{Mail: m, Resolve: approvalResolver(""), Stdout: output}
	type outcome struct {
		result ExecutionResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := e.Execute(ctx, mailRuntime("worker"), "trap '' TERM; sleep 30 & printf '%s\\n' \"$!\"; wait")
		done <- outcome{result, err}
	}()
	var child int
	select {
	case child = <-ready:
	case out := <-done:
		t.Fatal("command ended before child readiness", out)
	case <-time.After(5 * time.Second):
		t.Fatal("no child readiness")
	}
	cancel()
	select {
	case out := <-done:
		if !errors.Is(out.err, context.Canceled) || out.result.Status != "cancelled" || out.result.ExitStatus == nil {
			t.Fatal(out)
		}
		saved := savedExecution(t, m, out.result)
		if saved.Status != "cancelled" || saved.ExitStatus == nil || saved.Reason == "" {
			t.Fatal(saved)
		}
	case <-time.After(3 * time.Second):
		// Old parent-only cancellation leaves sleep alive holding the stdout pipe.
		_ = syscall.Kill(child, syscall.SIGKILL)
		<-done
		t.Fatal("cancellation left an owned child holding output")
	}
}
