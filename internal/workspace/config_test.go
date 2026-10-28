package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func resolverTopology(t *testing.T, reviewer bool) string {
	t.Helper()
	content := "## `worker`\nWorker\n"
	if reviewer {
		content = "## `edges`\n```mermaid\ngraph LR\nworker --- peer:::command_approver_node\n```\n" + content
	}
	path := filepath.Join(t.TempDir(), "postman.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfiguredApprovalResolverDistinguishesVerifiedAbsence(t *testing.T) {
	ctx := context.Background()
	s, _ := mailRuntime("worker").Snapshot(ctx)
	for _, reviewer := range []bool{false, true} {
		path := resolverTopology(t, reviewer)
		resolve := ConfiguredApprovalResolver(path)
		got, err := resolve(ctx, s)
		expected := ""
		if reviewer {
			expected = "peer"
		}
		if err != nil || got != expected {
			t.Fatal(got, err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if _, err := resolve(ctx, s); err == nil {
			t.Fatal("missing config became advisory")
		}
	}
	path := resolverTopology(t, true)
	resolve := ConfiguredApprovalResolver(path)
	peer, _ := mailRuntime("peer").Snapshot(ctx)
	if _, err := resolve(ctx, peer); err == nil {
		t.Fatal("reviewer can self approve")
	}
	missing := s
	missing.Bindings = []Binding{missing.Caller}
	if _, err := resolve(ctx, missing); err == nil {
		t.Fatal("unbound reviewer became advisory")
	}
	verifiedAbsent := missing
	verifiedAbsent.AbsentRoles = []string{"peer"}
	if got, err := resolve(ctx, verifiedAbsent); err != nil || got != "" {
		t.Fatal("verified native absence did not become advisory", got, err)
	}
	if _, err := ConfiguredApprovalResolver(resolverTopology(t, false))(ctx, peer); err == nil {
		t.Fatal("unconfigured caller")
	}
}

func TestConfiguredApprovalResolverChangeDuringWaitFailsClosed(t *testing.T) {
	m := mailFixture(t)
	path := resolverTopology(t, true)
	calls := 0
	e := Executor{Mail: m, Resolve: ConfiguredApprovalResolver(path)}
	e.wait = func(context.Context) error {
		if err := os.WriteFile(path, []byte("## `worker`\nWorker\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return nil
	}
	e.run = func(context.Context, string, io.Writer, io.Writer) (int, error) { calls++; return 0, nil }
	got, err := e.Execute(context.Background(), mailRuntime("worker"), "never")
	if !errors.Is(err, ErrUnknown) || calls != 0 || got.Status != "unavailable" {
		t.Fatal(got, err, calls)
	}
}

func TestConfiguredApprovalMalformedTopologyCannotBecomeAdvisory(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
	}{
		{"unclosed_mermaid", "## `edges`\n```mermaid\ngraph LR\nworker --- peer:::command_approver_node\n"},
		{"duplicate_node_section", "## `worker`\nWorker\n## `worker`\nAgain\n"},
		{"legacy_reviewer_key", "---\ncommand_approver_node: peer\n---\n## `worker`\nWorker\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "postman.md")
			if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
				t.Fatal(err)
			}
			calls := 0
			e := Executor{Mail: mailFixture(t), Resolve: ConfiguredApprovalResolver(path)}
			e.run = func(context.Context, string, io.Writer, io.Writer) (int, error) {
				calls++
				return 0, nil
			}
			got, err := e.Execute(context.Background(), mailRuntime("worker"), "never")
			if err == nil || got.Status != "unavailable" || calls != 0 || got.RequestID != "" {
				t.Fatal(got, err, calls)
			}
		})
	}
}

func TestConfiguredApprovalNativeWorkspaceAndRealBash(t *testing.T) {
	f := newTmuxFixture(t)
	pane := f.run(t, "split-window", "-d", "-t", f.pane, "-P", "-F", "#{pane_id}", "exec sleep 120")
	f.run(t, "select-pane", "-t", pane, "-T", "approver")
	requester := f.adapter()
	reviewer := requester
	reviewer.Pane = pane
	path := filepath.Join(t.TempDir(), "postman.md")
	content := "## `edges`\n```mermaid\ngraph LR\nworker --- approver:::command_approver_node\n```\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	m := mailFixture(t)
	var stdout, stderr bytes.Buffer
	e := Executor{Mail: m, Resolve: ConfiguredApprovalResolver(path), Stdout: &stdout, Stderr: &stderr}
	e.wait = func(ctx context.Context) error {
		state := m.State(ctx, reviewer, nil)
		if !state.Known || len(state.Inbound) != 1 {
			t.Fatal(state)
		}
		claim, err := m.Claim(ctx, reviewer, state.Inbound[0].MessageID)
		if err != nil || claim == nil {
			t.Fatal(claim, err)
		}
		var request Message
		err = m.Store.withView(ctx, reviewer, func(ws *os.Root, _ Snapshot) error {
			box, err := openOwnBox(ws, "approver", "read")
			if err != nil {
				return err
			}
			defer func(closeFn func() error) { _ = closeFn() }(box.Close)
			request, err = readMessage(box, state.Inbound[0].MessageID+".md")
			return err
		})
		if err != nil || claim.Path == "" {
			t.Fatal(claim, err)
		}
		var payload ApprovalRequest
		if err := json.Unmarshal([]byte(request.Body), &payload); err != nil {
			t.Fatal(err)
		}
		body, _ := json.Marshal(ApprovalDecision{1, payload.Command, "approved"})
		_, err = m.SendWithReply(ctx, reviewer, "worker", string(body), request.ID, request.InputRequestID, false)
		return err
	}
	got, err := e.Execute(context.Background(), requester, "printf 'native-approved'; printf 'native-diagnostic' >&2; exit 3")
	if err != nil || got.Status != "exited" || got.ExitStatus == nil || *got.ExitStatus != 3 ||
		stdout.String() != "native-approved" || stderr.String() != "native-diagnostic" {
		t.Fatal(got, err)
	}
	if got.RequestID == "" || got.InputRequestID == "" || got.ReplyID == "" {
		t.Fatal(got)
	}
	state := m.State(context.Background(), requester, nil)
	if !state.Known || state.Unread == nil || *state.Unread != 0 || len(state.Outbound) != 0 {
		t.Fatal(state)
	}
	f.run(t, "kill-pane", "-t", pane)
	verified, err := requester.Snapshot(context.Background())
	if err != nil || len(verified.AbsentRoles) != 1 || verified.AbsentRoles[0] != "approver" {
		t.Fatal("removed reviewer was not positively absent", verified, err)
	}
	stdout.Reset()
	stderr.Reset()
	advisory, err := e.Execute(context.Background(), requester, "printf 'native-absent'")
	if err != nil || advisory.Status != "executed" || advisory.Reviewer != "" ||
		advisory.RequestID != "" || stdout.String() != "native-absent" || stderr.Len() != 0 {
		t.Fatal("verified absent reviewer did not run advisory Bash", advisory, err, stdout.String(), stderr.String())
	}
	requester.Socket = filepath.Join(t.TempDir(), "missing-socket")
	stdout.Reset()
	unknown, err := e.Execute(context.Background(), requester, "printf 'must-not-run'")
	if err == nil || unknown.Status != "unavailable" || stdout.Len() != 0 {
		t.Fatal("failed native probe became advisory", unknown, err, stdout.String())
	}
}
