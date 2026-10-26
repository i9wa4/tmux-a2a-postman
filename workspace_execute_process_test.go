package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	postmanconfig "github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

// Run the real public main in a child process so its ExitCode forwarding and
// stdout/stderr contract are observed across an OS process boundary.
func TestIssue937PublicCLIChild(t *testing.T) {
	raw := os.Getenv("POSTMAN_TEST_PUBLIC_ARGS")
	if raw == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"postman"}, args...)
	main()
}

func TestWorkspaceExecuteBashProcessPreservesExitAndStreams(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "pm937-exec-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	home := t.TempDir()
	stateHome := t.TempDir()
	root := filepath.Join(home, "work")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	runTmux := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("owned tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	pane := runTmux("-f", "/dev/null", "new-session", "-d", "-s", "work", "-c", root, "-P", "-F", "#{pane_id}", "exec sleep 120")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "tmux", "-S", socket, "kill-server").Run()
	})
	runTmux("select-pane", "-t", pane, "-T", "worker")
	config := filepath.Join(t.TempDir(), "postman.md")
	if err := os.WriteFile(config, []byte("## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A matching historical approval-looking file must have no authority over
	// a fresh file-first invocation. Its bytes must remain untouched.
	legacyDir := filepath.Join(stateHome, "tmux-a2a-postman", "legacy-context", "work", "journal")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(legacyDir, "approved.json")
	legacyBody := []byte(`{"thread_id":"command-approval-old","decision":"approved","command":"printf protocol-output; printf diagnostic >&2; exit 7"}`)
	if err := os.WriteFile(legacy, legacyBody, 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := "printf protocol-output; printf diagnostic >&2; exit 7"
	args, err := json.Marshal([]string{"execute-bash", "--postman-md", config, "--command", command})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, self, "-test.run=^TestIssue937PublicCLIChild$")
	child.Env = append(os.Environ(), "HOME="+home, "XDG_STATE_HOME="+stateHome, "TMUX="+socket+",0,0", "TMUX_PANE="+pane, "POSTMAN_TEST_PUBLIC_ARGS="+string(args))
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	err = child.Run()
	var exit *exec.ExitError
	if ctx.Err() != nil || !strings.Contains(stderr.String(), "diagnostic") || stdout.String() != "protocol-output" ||
		!errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("public process: err=%v, context=%v, stdout=%q, stderr=%q", err, ctx.Err(), stdout.String(), stderr.String())
	}
	jsonStart := bytes.IndexByte(stderr.Bytes(), '{')
	if jsonStart < 0 {
		t.Fatalf("missing JSON result on stderr: %q", stderr.String())
	}
	var result struct {
		Status    string                    `json:"status"`
		Execution workspace.ExecutionResult `json:"execution"`
	}
	if err := json.NewDecoder(bytes.NewReader(stderr.Bytes()[jsonStart:])).Decode(&result); err != nil ||
		result.Status != "exited" || result.Execution.ExitStatus == nil || *result.Execution.ExitStatus != 7 || result.Execution.RequestID != "" {
		t.Fatalf("public JSON: %+v, err=%v, stderr=%q", result, err, stderr.String())
	}
	if got, err := os.ReadFile(legacy); err != nil || !bytes.Equal(got, legacyBody) {
		t.Fatalf("historical approval changed: %q, %v", got, err)
	}
}

func TestWorkspaceExecuteBashProcessIgnoresLegacyDecisionsWithLiveReviewer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "pm937-old-approval-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "s")
	home, stateHome := t.TempDir(), t.TempDir()
	root := filepath.Join(home, "work")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	runTmux := func(args ...string) string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "tmux", append([]string{"-S", socket}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("owned tmux %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	pane := runTmux("-f", "/dev/null", "new-session", "-d", "-s", "work", "-c", root, "-P", "-F", "#{pane_id}", "exec sleep 120")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "tmux", "-S", socket, "kill-server").Run()
	})
	runTmux("select-pane", "-t", pane, "-T", "worker")
	reviewer := runTmux("split-window", "-d", "-t", pane, "-P", "-F", "#{pane_id}", "exec sleep 120")
	runTmux("select-pane", "-t", reviewer, "-T", "peer")
	config := filepath.Join(t.TempDir(), "postman.md")
	if err := os.WriteFile(config, []byte("## `edges`\n```mermaid\ngraph LR\nworker --- peer:::command_approver_node\n```\n## `worker`\nWorker\n## `peer`\nPeer\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "executed")
	command := "printf legacy-authorized > " + marker
	baseDir := filepath.Join(stateHome, "tmux-a2a-postman")
	legacySession := filepath.Join(baseDir, "legacy-context", "work")
	now := time.Now().UTC()
	writer, err := journal.OpenShadowWriter(legacySession, "legacy-context", "work", os.Getpid(), now)
	if err != nil {
		t.Fatal(err)
	}
	// The retired CLI resolved its journal through the live context owner.
	// Make this private fixture discoverable by that same resolver.
	if err := os.WriteFile(filepath.Join(legacySession, "postman.pid"), []byte(fmt.Sprint(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if contextID, err := postmanconfig.ResolveContextIDFromSession(baseDir, "work"); err != nil || contextID != "legacy-context" {
		t.Fatalf("retired context resolution: context=%q err=%v", contextID, err)
	}
	hash := sha256.Sum256([]byte(command))
	commandHash := "sha256:" + hex.EncodeToString(hash[:])
	// Retired --label legacy --reviewer peer, with default blocking mode and
	// empty category, selected this thread from the exact policy tuple.
	threadHash := sha256.Sum256([]byte(strings.Join([]string{"worker", "peer", "legacy", "", commandHash}, "\x00")))
	approvedThreadID := "command-approval-" + hex.EncodeToString(threadHash[:8])
	requesterAddress := nodeaddr.Full("worker", "work")
	approverAddress := nodeaddr.Full("peer", "work")
	for i, decision := range []journal.ApprovalDecision{journal.ApprovalDecisionApproved, journal.ApprovalDecisionRejected} {
		thread := "command-approval-legacy-" + string(rune('a'+i))
		if i == 0 {
			thread = approvedThreadID
		}
		input := "ireq_legacy_" + string(rune('a'+i))
		request := journal.CommandApprovalRequestPayload{
			Requester: "worker", RequesterAddress: requesterAddress,
			Reviewer: "peer", CommandApproverNode: "peer", CommandApproverAddress: approverAddress,
			Mode: "blocking", Label: "legacy", CommandHash: commandHash,
			InputRequestID: input, CommandText: command, ExpiresAt: now.Add(time.Hour).Format(time.RFC3339Nano),
		}
		if _, err := writer.AppendEventWithOptions(journal.CommandApprovalRequestedEventType, journal.VisibilityOperatorVisible, request, journal.AppendOptions{ThreadID: thread}, now.Add(-time.Duration(4-2*i)*time.Second)); err != nil {
			t.Fatal(err)
		}
		oldDecision := journal.CommandApprovalDecisionPayload{
			Reviewer: "peer", ReviewerAddress: approverAddress, RequesterAddress: requesterAddress,
			Decision: decision, InputRequestID: input, CommandHash: commandHash,
		}
		if _, err := writer.AppendEventWithOptions(journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, oldDecision, journal.AppendOptions{ThreadID: thread}, now.Add(-time.Duration(3-2*i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.SyncCommandApprovalDecisionHistory(legacySession); err != nil {
		t.Fatal(err)
	}
	history, err := journal.ListCommandApprovalDecisionHistory(legacySession)
	if err != nil || len(history) != 2 {
		t.Fatalf("parser-valid historical decisions: %d, %v", len(history), err)
	}
	state, found, err := projection.ProjectCommandApprovalState(legacySession, time.Now().UTC())
	approved := state.Threads[approvedThreadID]
	// This is the retired evaluator's Allowed branch: exact thread and policy
	// identity, unexpired address-qualified approval, and HistoricalOnly=false.
	oldAllowed := found && err == nil && approved.Status == projection.CommandApprovalStatusApproved && !approved.HistoricalOnly &&
		approved.ThreadID == approvedThreadID && approved.CommandHash == commandHash &&
		approved.Requester == "worker" && approved.RequesterAddress == requesterAddress &&
		approved.Reviewer == "peer" && approved.CommandApproverNode == "peer" && approved.CommandApproverAddress == approverAddress &&
		approved.Label == "legacy" && approved.Category == "" && approved.Mode == "blocking" &&
		approved.InputRequestID == "ireq_legacy_a" && approved.ExpiresAt == now.Add(time.Hour).Format(time.RFC3339Nano)
	if !oldAllowed {
		t.Fatalf("retired selector did not have an eligible approval: found=%v thread=%+v err=%v", found, approved, err)
	}
	before, err := snapshotRegularFiles(legacySession)
	if err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal([]string{"execute-bash", "--postman-md", config, "--command", command})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, self, "-test.run=^TestIssue937PublicCLIChild$")
	child.Env = append(os.Environ(), "HOME="+home, "XDG_STATE_HOME="+stateHome, "TMUX="+socket+",0,0", "TMUX_PANE="+pane, "POSTMAN_TEST_PUBLIC_ARGS="+string(args))
	var stdout, stderr bytes.Buffer
	child.Stdout, child.Stderr = &stdout, &stderr
	_ = child.Run()
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("configured fresh request did not wait for its own decision: context=%v stdout=%q stderr=%q", ctx.Err(), stdout.String(), stderr.String())
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old approval authorized fresh command: marker stat=%v", err)
	}
	workspaceRoot, err := workspace.DefaultStateRoot(home, stateHome)
	if err != nil {
		t.Fatal(err)
	}
	var freshRequest workspace.Message
	var approvalRequest workspace.ApprovalRequest
	freshRequestCount := 0
	var visited []string
	err = filepath.WalkDir(workspaceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		visited = append(visited, path)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(path, filepath.Join(workspaceRoot, "work", "agents", "peer", "inbox")+string(os.PathSeparator)) {
			prefix := []byte("<!-- postman ")
			separator := []byte(" -->\n\n")
			split := bytes.Index(data, separator)
			if !bytes.HasPrefix(data, prefix) || split < len(prefix) {
				return fmt.Errorf("fresh reviewer request has invalid envelope: %s", path)
			}
			if err := json.Unmarshal(data[len(prefix):split], &freshRequest); err != nil {
				return err
			}
			if err := json.Unmarshal(data[split+len(separator):], &approvalRequest); err != nil {
				return err
			}
			freshRequestCount++
		}
		return nil
	})
	if err != nil || freshRequestCount != 1 || freshRequest.From != "worker" || freshRequest.To != "peer" ||
		!freshRequest.NeedsReply || freshRequest.InputRequestID == "" || !strings.HasPrefix(freshRequest.ID, "m-") ||
		freshRequest.InputRequestID != "ireq_"+strings.TrimPrefix(freshRequest.ID, "m-") ||
		freshRequest.InputRequestID == "ireq_legacy_a" || freshRequest.InputRequestID == "ireq_legacy_b" ||
		freshRequest.ReplyTo != "" || approvalRequest.Version != 1 || approvalRequest.Command != command {
		t.Fatalf("fresh approval correlation: count=%d envelope=%+v body=%+v err=%v files=%v stderr=%q", freshRequestCount, freshRequest, approvalRequest, err, visited, stderr.String())
	}
	after, err := snapshotRegularFiles(legacySession)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("historical records changed: before=%v after=%v err=%v", before, after, err)
	}
}

type legacyPathSnapshot struct {
	mode os.FileMode
	hash [32]byte
}

func snapshotRegularFiles(root string) (map[string]legacyPathSnapshot, error) {
	result := make(map[string]legacyPathSnapshot)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snapshot := legacyPathSnapshot{mode: info.Mode()}
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			snapshot.hash = sha256.Sum256(data)
		}
		result[rel] = snapshot
		return nil
	})
	return result, err
}
