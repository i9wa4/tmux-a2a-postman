package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

type recipientEvidence struct {
	Message workspace.Message       `json:"message"`
	Archive string                  `json:"archive"`
	SHA256  string                  `json:"sha256"`
	Reply   *workspace.Message      `json:"reply,omitempty"`
	Notice  *workspace.NoticeResult `json:"notice,omitempty"`
}

// This process is the recipient's handler, launched only by the installed
// Bash Readline binding. The controller never calls POP or constructs replies.
func TestNotificationRecipientChild(t *testing.T) {
	if os.Getenv("POSTMAN_RECIPIENT_MODE") == "" {
		return
	}
	var pop struct {
		Status  string            `json:"status"`
		Message workspace.Message `json:"message"`
		Path    string            `json:"markdown_absolute_path"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&pop); err != nil {
		t.Fatal(err)
	}
	if pop.Status == "empty" {
		return
	}
	if pop.Status != "message" {
		t.Fatal(pop.Status)
	}
	raw, err := os.ReadFile(pop.Path)
	if err != nil {
		t.Fatal(err)
	} // full file through EOF
	split := bytes.Index(raw, []byte(" -->\n\n"))
	if split < 0 {
		t.Fatal("invalid archive")
	}
	body := raw[split+len(" -->\n\n"):]
	sum := sha256.Sum256(body)
	proof := recipientEvidence{Message: pop.Message, Archive: pop.Path, SHA256: hex.EncodeToString(sum[:])}
	if pop.Message.NeedsReply {
		replyBody := []byte("Read complete body SHA256=" + proof.SHA256)
		if os.Getenv("POSTMAN_RECIPIENT_MODE") == "reviewer" {
			var request workspace.ApprovalRequest
			if err := json.Unmarshal(body, &request); err != nil {
				t.Fatal(err)
			}
			decision := os.Getenv("POSTMAN_TEST_DECISION")
			if decision == "" {
				decision = "approved"
			}
			replyBody, _ = json.Marshal(workspace.ApprovalDecision{Version: 1, Command: request.Command, Decision: decision})
		}
		to := pop.Message.From
		if pop.Message.FromSession != "" {
			to = pop.Message.FromSession + ":" + to
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Getenv("POSTMAN_BIN"), "send-heredoc", "--postman-md", os.Getenv("POSTMAN_CONFIG"), "--to", to, "--reply-to", pop.Message.ID, "--fills-input-request-id", pop.Message.InputRequestID, "--no-reply")
		cmd.Stdin = bytes.NewReader(replyBody)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("recipient reply: %v: %s", err, stderr.String())
		}
		var sent struct {
			Message *workspace.Message      `json:"message"`
			Notice  *workspace.NoticeResult `json:"notice"`
		}
		if err := json.Unmarshal(out, &sent); err != nil {
			t.Fatal(err)
		}
		proof.Reply, proof.Notice = sent.Message, sent.Notice
	}
	data, _ := json.Marshal(proof)
	if err := os.WriteFile(os.Getenv("POSTMAN_RECIPIENT_EVIDENCE"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func quote952(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func wait952(t *testing.T, description string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", description)
}

func (f *rebuiltCLIFixture) bashRecipient(t *testing.T, pane, mode, config string) (string, string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(f.dir, "recipient-"+strings.TrimPrefix(pane, "%"))
	evidence, ready := base+".json", base+".ready"
	handler := base + ".handler"
	script := "#!/bin/sh\nexec " + quote952(self) + " -test.run='^TestNotificationRecipientChild$'\n"
	if mode == "receipt" {
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		python, err := exec.LookPath("python3")
		if err != nil {
			t.Fatal(err)
		}
		script = "#!/bin/sh\nexec " + quote952(python) + " " + quote952(filepath.Join(cwd, "integrations/bash/receipt-handler.py")) + " > " + quote952(evidence) + "\n"
	}

	if err := os.WriteFile(handler, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// Use the emitted setup as a regular file: /dev/fd process substitution is
	// not available in every Nix build sandbox.
	setup := f.run(pane, "", "notifier-script")
	if setup.exit != 0 {
		t.Fatalf("emit recipient setup: %s", setup.stderr)
	}
	setupPath := base + ".setup.bash"
	if err := os.WriteFile(setupPath, setup.stdout, 0o600); err != nil {
		t.Fatal(err)
	}
	rc := base + ".bashrc"
	var env strings.Builder
	for _, pair := range [][2]string{{"HOME", f.home}, {"XDG_STATE_HOME", f.stateHome}, {"POSTMAN_BIN", f.bin}, {"POSTMAN_CONFIG", config}, {"POSTMAN_HANDLER", handler}, {"POSTMAN_RECIPIENT_MODE", mode}, {"POSTMAN_RECIPIENT_EVIDENCE", evidence}, {"POSTMAN_TEST_DECISION", os.Getenv("POSTMAN_TEST_DECISION")}} {
		fmt.Fprintf(&env, "export %s=%s\n", pair[0], quote952(pair[1]))
	}
	text := env.String() + "source " + quote952(setupPath) + " || exit\nPS1='postman> '\nprintf ready > " + quote952(ready) + "\n"
	if err := os.WriteFile(rc, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	bash := os.Getenv("POSTMAN_TEST_BASH")
	if bash == "" {
		bash, err = exec.LookPath("bash")
		if err != nil {
			t.Fatal(err)
		}
	}
	// Preserve startup errors even when the recipient shell exits immediately.
	f.tmux("set-option", "-p", "-t", pane, "remain-on-exit", "on")
	f.tmux("respawn-pane", "-k", "-t", pane, "exec "+quote952(bash)+" --noprofile --rcfile "+quote952(rc)+" -i")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		dead := f.tmux("display-message", "-p", "-t", pane, "#{pane_dead}")
		if dead == "1" || time.Now().After(deadline) {
			t.Fatalf("recipient setup failed (bash=%s, pane=%s, dead=%s):\n%s", bash, pane, dead,
				f.tmux("capture-pane", "-p", "-S", "-", "-t", pane))
		}
		time.Sleep(15 * time.Millisecond)
	}
	// Do not send until Bash is actually reading input after registration.
	wait952(t, "Readline prompt", func() bool { return strings.Contains(f.tmux("capture-pane", "-p", "-t", pane), "postman>") })
	return evidence, rc
}

func read952(t *testing.T, path string) recipientEvidence {
	t.Helper()
	wait952(t, "notification-driven handler evidence", func() bool { _, err := os.Stat(path); return err == nil })
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var proof recipientEvidence
	if err := json.Unmarshal(data, &proof); err != nil {
		t.Fatal(err)
	}
	return proof
}

func TestNotificationDrivenRoundTrip(t *testing.T) {
	f := newRebuiltCLIFixture(t)
	f.tmux("resize-window", "-t", f.peer, "-x", "240")
	workerProof, _ := f.bashRecipient(t, f.worker, "sender", f.config)
	peerProof, _ := f.bashRecipient(t, f.peer, "receiver", f.config)
	// A pending command must remain unsubmitted and be preserved across the hook.
	marker := filepath.Join(f.dir, "must-not-run")
	pending := "touch " + quote952(marker) + "; :"
	f.tmux("send-keys", "-t", f.peer, "-l", pending)
	body := strings.Repeat("Markdown body with `literal $HOME`\n", 300) + "END-OF-BODY\n"
	started := time.Now()
	send := f.run(f.worker, body, "send-heredoc", "--postman-md", f.config, "--to", "peer", "--reply-required")
	sent := f.jsonResult(t, send)
	if send.exit != 0 || sent.Message == nil || sent.Notice == nil || sent.Notice.Status != "accepted" {
		t.Fatalf("send: %+v stderr=%s", sent, send.stderr)
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("send waited for receiver")
	}
	receiver := read952(t, peerProof)
	sender := read952(t, workerProof)
	sum := sha256.Sum256([]byte(body))
	if receiver.SHA256 != hex.EncodeToString(sum[:]) || receiver.Message.ID != sent.Message.ID {
		t.Fatal("receiver did not read complete exact message", receiver)
	}
	if receiver.Reply == nil || sender.Message.ID != receiver.Reply.ID || sender.Message.ReplyTo != sent.Message.ID || sender.Message.FillsInputRequestID != sent.Message.InputRequestID {
		t.Fatal("reply lost exact correlation", receiver, sender)
	}
	if receiver.Notice == nil || receiver.Notice.Status != "accepted" {
		t.Fatal("return notification unavailable", receiver.Notice)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("notification submitted pending input", err)
	}
	screen := f.tmux("capture-pane", "-p", "-t", f.peer)
	if !strings.Contains(screen, pending) {
		t.Fatalf("Readline composer lost: %q", screen)
	}
	t.Logf("Bash/Readline smoke: notice=%s request=%s full-body-sha256=%s reply=%s sender-claim=%s", sent.Notice.Status, sent.Message.ID, receiver.SHA256, sender.Message.ID, sender.Archive)
}

func TestNotificationDisabledCannotDriveRoundTrip(t *testing.T) {
	f := newRebuiltCLIFixture(t)
	proof, _ := f.bashRecipient(t, f.peer, "receiver", f.config)
	// Delete only the recipient registration; the live recipient remains waiting.
	name, _ := workspace.Name("~/work")
	path := filepath.Join(f.stateRoot, name, "runtime", "notifiers", "peer.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	send := f.run(f.worker, "no trigger", "send-heredoc", "--postman-md", f.config, "--to", "peer", "--reply-required")
	sent := f.jsonResult(t, send)
	if send.exit != 0 || sent.Notice == nil || sent.Notice.Status != "unavailable" {
		t.Fatalf("delivery/notice conflated: %+v %s", sent, send.stderr)
	}
	// A bounded negative observation, never a POP from the controller.
	timer := time.NewTimer(150 * time.Millisecond)
	defer timer.Stop()
	<-timer.C
	if _, err := os.Stat(proof); !os.IsNotExist(err) {
		t.Fatal("receiver ran without notification", err)
	}
	inbox := filepath.Join(f.stateRoot, name, "agents", "peer", "inbox", sent.Message.ID+".md")
	if _, err := os.Stat(inbox); err != nil {
		t.Fatal("notification failure lost delivery", err)
	}
}

func TestNotificationDrivenApproval(t *testing.T) {
	for _, decision := range []string{"approved", "rejected"} {
		t.Run(decision, func(t *testing.T) {
			t.Setenv("POSTMAN_TEST_DECISION", decision)
			f := newRebuiltCLIFixture(t)
			data, err := os.ReadFile(f.config)
			if err != nil {
				t.Fatal(err)
			}
			data = bytes.Replace(data, []byte("worker --- peer"), []byte("worker --- peer\nclass peer command_approver_node"), 1)
			if err := os.WriteFile(f.config, data, 0o600); err != nil {
				t.Fatal(err)
			}
			proof, _ := f.bashRecipient(t, f.peer, "reviewer", f.config)
			marker := filepath.Join(f.dir, "executed")
			got := f.run(f.worker, "", "execute-bash", "--postman-md", f.config, "--command", "printf approved > "+quote952(marker))
			var out struct {
				Execution workspace.ExecutionResult `json:"execution"`
			}
			if err := json.NewDecoder(bytes.NewReader(got.stderr)).Decode(&out); err != nil {
				t.Fatalf("%v: %s", err, got.stderr)
			}
			reviewer := read952(t, proof)
			if reviewer.Reply == nil || out.Execution.ReplyID != reviewer.Reply.ID || out.Execution.RequestID != reviewer.Message.ID || out.Execution.Notice == nil || out.Execution.Notice.Status != "accepted" {
				t.Fatal("approval bypassed notification", out, reviewer)
			}
			_, statErr := os.Stat(marker)
			if decision == "approved" {
				if got.exit != 0 || statErr != nil || out.Execution.Status != "executed" {
					t.Fatal(got.exit, out, statErr)
				}
			} else {
				if got.exit == 0 || !os.IsNotExist(statErr) || out.Execution.Status != "rejected" {
					t.Fatal(got.exit, out, statErr)
				}
			}
		})
	}
}

func TestNotificationDrivenParentChildRoundTrip(t *testing.T) {
	for _, direction := range []string{"parent-to-child", "child-to-parent"} {
		t.Run(direction, func(t *testing.T) {
			f := newRebuiltCLIFixture(t)
			childRoot := filepath.Join(f.home, "child")
			if err := os.Mkdir(childRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			child := f.tmux("new-session", "-d", "-s", "child", "-c", childRoot, "-P", "-F", "#{pane_id}")
			f.tmux("select-pane", "-t", child, "-T", "peer")
			tree := "workspace_tree:\n  - session_name: work\n    interface_node: worker\n  - session_name: child\n    parent_session_name: work\n    interface_node: peer\n"
			topology := "## `edges`\n```mermaid\ngraph LR\nworker --- peer\n```\n"
			parentConfig := f.config
			childConfig := filepath.Join(childRoot, "postman.md")
			for path, role := range map[string]string{parentConfig: "worker", childConfig: "peer"} {
				if err := os.WriteFile(path, []byte("---\ninterface_node: "+role+"\n"+tree+"---\n"+topology), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			parentProof, _ := f.bashRecipient(t, f.worker, "parent", parentConfig)
			childProof, _ := f.bashRecipient(t, child, "child", childConfig)
			pane, config, to, receiverPath, senderPath := f.worker, parentConfig, "child:peer", childProof, parentProof
			if direction == "child-to-parent" {
				pane, config, to, receiverPath, senderPath = child, childConfig, "work:worker", parentProof, childProof
			}
			body := "parent/child complete body\nlast line\n"
			got := f.run(pane, body, "send-heredoc", "--postman-md", config, "--to", to, "--reply-required")
			sent := f.jsonResult(t, got)
			if got.exit != 0 || sent.Notice == nil || sent.Notice.Status != "accepted" {
				t.Fatalf("cross notice: %+v %s", sent, got.stderr)
			}
			receiver, sender := read952(t, receiverPath), read952(t, senderPath)
			if receiver.Message.ID != sent.Message.ID || receiver.Reply == nil || sender.Message.ID != receiver.Reply.ID || sender.Message.ReplyTo != sent.Message.ID || sender.Message.FillsInputRequestID != sent.Message.InputRequestID {
				t.Fatal("cross exact reply failed", receiver, sender)
			}
			if receiver.Notice == nil || receiver.Notice.Status != "accepted" {
				t.Fatal("cross return notice failed", receiver)
			}
			// The same-named local peer must remain untouched by cross-session notices.
			if strings.Contains(f.tmux("capture-pane", "-p", "-t", f.peer), "POSTMAN") {
				t.Fatal("local same-named pane received cross notice")
			}
		})
	}
}

// Smoke the shipped deterministic recipient integration, rather than only the
// scripted test recipient. The notification adapter is exactly the normal route.
func TestNotificationShippedReceiptHandlerSmoke(t *testing.T) {
	f := newRebuiltCLIFixture(t)
	workerPath, _ := f.bashRecipient(t, f.worker, "receipt", f.config)
	peerPath, _ := f.bashRecipient(t, f.peer, "receipt", f.config)
	got := f.run(f.worker, "Shipped receipt handler smoke\nEOF marker\n", "send-heredoc", "--postman-md", f.config, "--to", "peer", "--reply-required")
	sent := f.jsonResult(t, got)
	if got.exit != 0 || sent.Notice == nil || sent.Notice.Status != "accepted" {
		t.Fatal(sent, string(got.stderr))
	}
	readReceipt := func(path string) (string, string) {
		t.Helper()
		var claim, hash string
		wait952(t, "shipped receipt handler full-read evidence", func() bool {
			data, err := os.ReadFile(path)
			if err != nil {
				return false
			}
			dec := json.NewDecoder(bytes.NewReader(data))
			for {
				var line struct {
					Claimed string `json:"claimed"`
					Archive string `json:"archive"`
					SHA256  string `json:"full_archive_sha256"`
				}
				if dec.Decode(&line) != nil {
					break
				}
				if line.Claimed != "" {
					raw, err := os.ReadFile(line.Archive)
					if err != nil {
						return false
					}
					sum := sha256.Sum256(raw)
					if line.SHA256 != hex.EncodeToString(sum[:]) {
						t.Fatal("shipped handler did not fully read archive")
					}
					claim, hash = line.Claimed, line.SHA256
					return true
				}
			}
			return false
		})
		return claim, hash
	}
	request, hash := readReceipt(peerPath)
	reply, _ := readReceipt(workerPath)
	if request != sent.Message.ID || reply == request {
		t.Fatal("shipped round trip missing", request, reply)
	}
	name, _ := workspace.Name("~/work")
	archived, err := os.ReadFile(filepath.Join(f.stateRoot, name, "agents", "worker", "read", reply+".md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(archived, []byte(`"reply_to":"`+request+`"`)) || !bytes.Contains(archived, []byte(`"fills_input_request_id":"`+sent.Message.InputRequestID+`"`)) {
		t.Fatal("shipped reply lacks correlation")
	}
	t.Logf("shipped receipt smoke: notice=accepted receiver-claim=%s full-archive-sha256=%s exact-reply-and-sender-claim=%s", request, hash, reply)
}
