package daemon

import (
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
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

// M2-B1 T5: the foreign-sender guard in message.DeliverMessage is exercised
// with a post file written by the REAL daemon-submit producer
// (processDaemonSubmitRequest -> handleDaemonSubmitSend), not with literal text.
// The producer only validates the filename and writes the file into the
// requesting session's post/; the guard decides afterwards.
//
// What this test pins (negative controls run while writing it): the forged
// subtest FAILS when the planner's foreign-sender check
// (planDeliveryPolicy -> senderClaimsForeignSession) is disabled, because the
// explicit early guard in DeliverMessage takes its dead-letter suffix from that
// same planner decision (the file then lands in dead-letter/ WITHOUT the
// -dl-forged-sender suffix). Removing ONLY the early guard does not change the
// observable outcome, because the planner classifies the same message at its
// next call. So this test proves the end-to-end protection, not the early
// guard's ordering in isolation.
func TestDaemonSubmitSendProducerFeedsForeignSenderGuard(t *testing.T) {
	baseDir := t.TempDir()
	sessionA := filepath.Join(baseDir, "sess-a")
	sessionB := filepath.Join(baseDir, "sess-b")
	for _, dir := range []string{sessionA, sessionB} {
		if err := config.CreateSessionDirs(dir); err != nil {
			t.Fatalf("CreateSessionDirs(%s): %v", dir, err)
		}
	}
	manager := journal.NewManager("test-ctx", 31353)
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)

	nodes := map[string]discovery.NodeInfo{
		"sess-a:worker": {PaneID: "%1", SessionName: "sess-a", SessionDir: sessionA},
		"sess-a:node":   {PaneID: "%2", SessionName: "sess-a", SessionDir: sessionA},
		"sess-b:node":   {PaneID: "%3", SessionName: "sess-b", SessionDir: sessionB},
	}
	adjacency := map[string][]string{
		"sess-a:node":   {"sess-a:worker"},
		"sess-b:node":   {"sess-a:worker"},
		"sess-a:worker": {"sess-a:node", "sess-b:node"},
	}

	// submit runs the real producer for a send request and returns the post
	// path it wrote.
	//
	// boundSender is the sender the request is bound to (the node that really
	// called send in session A). It is the same for the valid and the forged
	// request: the forgery is only in the file name and envelope, which the
	// producer does not compare with the bound sender.
	const boundSender = "sess-a:node"
	submit := func(t *testing.T, requestID, filename, content string) string {
		t.Helper()
		requestPath, err := projection.WriteDaemonSubmitRequest(sessionA, projection.DaemonSubmitRequest{
			RequestID: requestID,
			Command:   projection.DaemonSubmitSend,
			CreatedAt: time.Now().UTC().Format(time.RFC3339),
			Sender:    boundSender,
			Filename:  filename,
			Content:   content,
		})
		if err != nil {
			t.Fatalf("WriteDaemonSubmitRequest: %v", err)
		}
		result, err := processDaemonSubmitRequest(requestPath)
		if err != nil {
			t.Fatalf("processDaemonSubmitRequest: %v", err)
		}
		wantPostPath := filepath.Join(sessionA, "post", filename)
		if !result.hasPostDispatch() || result.PostPath != wantPostPath {
			t.Fatalf("producer result = %#v, want a post dispatch at %s", result, wantPostPath)
		}
		got, err := os.ReadFile(wantPostPath)
		if err != nil || string(got) != content {
			t.Fatalf("producer wrote post = %q (err %v), want the submitted content %q", got, err, content)
		}
		return result.PostPath
	}
	deliver := func(t *testing.T, postPath string) {
		t.Helper()
		if err := message.DeliverMessage(postPath, "test-ctx", nodes, adjacency, &config.Config{EnterDelay: 0.1, TmuxTimeout: 1.0}, func(string) bool { return true }, nil, idle.NewIdleTracker(), ""); err != nil {
			t.Fatalf("DeliverMessage: %v", err)
		}
	}

	t.Run("sender in the source session passes the guard and lands in the inbox", func(t *testing.T) {
		filename := "20260709-130000-r0001-from-sess-a:node-to-sess-a:worker.md"
		content := "---\nparams:\n  contextId: test-ctx\n  from: sess-a:node\n  to: sess-a:worker\n  timestamp: 2026-07-09T13:00:00Z\n---\n\nvalid via daemon-submit\n"
		deliver(t, submit(t, "req-t5-valid", filename, content))

		if _, err := os.Stat(filepath.Join(sessionA, "inbox", "worker", filename)); err != nil {
			t.Fatalf("valid daemon-submit message did not reach the recipient inbox: %v", err)
		}
		if matches, _ := filepath.Glob(filepath.Join(sessionA, "dead-letter", "*")); len(matches) != 0 {
			t.Fatalf("valid daemon-submit message left dead letters: %v", matches)
		}
	})

	t.Run("forged foreign-qualified sender is dead-lettered after the producer accepted it", func(t *testing.T) {
		filename := "20260709-130001-r0002-from-sess-b:node-to-sess-a:worker.md"
		content := "---\nparams:\n  contextId: test-ctx\n  from: sess-b:node\n  to: sess-a:worker\n  timestamp: 2026-07-09T13:00:01Z\n---\n\nforged via daemon-submit\n"
		postPath := submit(t, "req-t5-forged", filename, content)
		deliver(t, postPath)

		if _, err := os.Stat(filepath.Join(sessionA, "inbox", "worker", filename)); !os.IsNotExist(err) {
			t.Fatalf("forged daemon-submit message reached the recipient inbox: %v", err)
		}
		if _, err := os.Stat(postPath); !os.IsNotExist(err) {
			t.Fatalf("forged file still in post/: %v", err)
		}
		// The suffix is asserted as a literal on purpose: it is the contract
		// with operators reading dead-letter/, not derived from the code under test.
		wantDeadLetter := filepath.Join(sessionA, "dead-letter", strings.TrimSuffix(filename, ".md")+"-dl-forged-sender.md")
		got, err := os.ReadFile(wantDeadLetter)
		if err != nil {
			t.Fatalf("forged-sender dead letter %s missing: %v", wantDeadLetter, err)
		}
		if string(got) != content {
			t.Fatalf("dead-letter audit copy = %q, want the forged bytes %q", got, content)
		}
		// Nothing was written into the claimed session B.
		if entries, _ := os.ReadDir(filepath.Join(sessionB, "inbox")); len(entries) != 0 {
			t.Fatalf("claimed session B inbox changed: %v", entries)
		}
	})
}
