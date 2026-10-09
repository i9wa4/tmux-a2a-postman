package daemon

import (
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/msgtrace"
	"github.com/i9wa4/tmux-a2a-postman/internal/tmuxrunner"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

// Post-path offload helpers for #871. The daemon's single select loop used to
// run the pre-delivery mailbox projection sync and a full tmux topology
// discovery inline for every post, delaying daemon-submit pop dispatch
// (queue_ms >> handler_ms). The sync now runs on one dedicated worker and the
// topology is reused for a short, identity-checked window.

// preDeliverySyncFn performs the pre-delivery mailbox projection sync. It is a
// variable so tests can inject a slow or recording step.
var preDeliverySyncFn = syncMailboxProjectionWithTrace

// preDeliverySyncWaitTimeout bounds how long a delivery goroutine waits for the
// dedicated sync worker. It FAILS CLOSED: if the sync did not complete in time,
// the delivery goroutine does not call DeliverMessage. The post file stays in
// post/ and the periodic pending-post reconciler retries it, so delivery never
// runs concurrently with (or ahead of) its own pre-delivery sync, and a wedged
// worker cannot widen the sync-vs-pop interleaving window (#802, #871).
// It is a variable only so tests can shrink it.
var preDeliverySyncWaitTimeout = 30 * time.Second

const preDeliverySyncQueueDepth = 64

// preDeliverySyncRequest is shared by pointer between the delivery goroutine
// and the worker. ok is written by the worker before it closes done and read by
// the waiter only after done is closed, so the close orders the accesses.
type preDeliverySyncRequest struct {
	sessionDir string
	fields     msgtrace.Fields
	done       chan struct{}
	ok         bool
}

// runPreDeliverySync hands the sync to the single dedicated worker and waits
// for it (bounded). Serializing here restores the pre-#871 concurrency profile
// (one pre-delivery sync at a time, previously guaranteed by running on the
// select loop) without adding sync-vs-sync overlap, and no lock is held while
// the sync runs. Ordering per message is preserved: the delivery goroutine does
// not call DeliverMessage until its own sync function has returned normally.
//
// It returns true only when the sync function returned normally. An ordinary
// sync problem is logged inside the sync function itself (it has no error
// result) and counts as completed, exactly as when the sync ran inline on the
// loop. A timeout, or a panic in the sync function, returns false: the worker
// survives the panic and keeps serving later requests, but the post is not
// delivered on the strength of a sync that did not finish. On false the caller
// must not deliver; a visible error event and log line are emitted (fail
// closed; the pending-post reconciler retries the post). Queue
// and retry are bounded: the queue holds at most preDeliverySyncQueueDepth
// requests, each delivery goroutine waits at most preDeliverySyncWaitTimeout
// and occupies one non-daemon delivery budget slot while waiting, and retries
// run at the reconciler cadence. A permanently wedged sync therefore shows as
// repeated error events and posts staying in post/, never as unbounded
// goroutines or a delivery that overlaps the sync.
func (rt *daemonRuntime) runPreDeliverySync(sessionDir string, fields msgtrace.Fields) bool {
	rt.preSyncOnce.Do(func() {
		queue := make(chan *preDeliverySyncRequest, preDeliverySyncQueueDepth)
		rt.preSyncQueue = queue
		go func() {
			for req := range queue {
				func() {
					defer close(req.done)
					defer func() {
						if r := recover(); r != nil {
							// req.ok stays false: this sync did not complete.
							log.Printf("🚨 PANIC in pre-delivery sync worker for %s: %v\n", req.sessionDir, r)
						}
					}()
					preDeliverySyncFn(req.sessionDir, req.fields)
					req.ok = true
				}()
			}
		}()
	})

	req := &preDeliverySyncRequest{sessionDir: sessionDir, fields: fields, done: make(chan struct{})}
	timer := time.NewTimer(preDeliverySyncWaitTimeout)
	defer timer.Stop()
	select {
	case rt.preSyncQueue <- req:
	case <-timer.C:
		log.Printf("postman: WARNING: component=post_path event=pre_delivery_sync_enqueue_timeout session=%s message_id=%s action=defer_delivery\n", filepath.Base(sessionDir), fields.MessageID)
		rt.reportPreDeliverySyncStall("enqueue", sessionDir, fields)
		return false
	}
	select {
	case <-req.done:
		if !req.ok {
			log.Printf("postman: WARNING: component=post_path event=pre_delivery_sync_panicked session=%s message_id=%s action=defer_delivery\n", filepath.Base(sessionDir), fields.MessageID)
			rt.reportPreDeliverySyncStall("panic", sessionDir, fields)
		}
		return req.ok
	case <-timer.C:
		log.Printf("postman: WARNING: component=post_path event=pre_delivery_sync_wait_timeout session=%s message_id=%s action=defer_delivery\n", filepath.Base(sessionDir), fields.MessageID)
		rt.reportPreDeliverySyncStall("wait", sessionDir, fields)
		return false
	}
}

// reportPreDeliverySyncStall surfaces a fail-closed deferral in the TUI event
// stream (non-blocking, so it can never stall the caller).
func (rt *daemonRuntime) reportPreDeliverySyncStall(phase, sessionDir string, fields msgtrace.Fields) {
	if rt.events == nil {
		return
	}
	tui.SendEventNonBlocking(rt.events, tui.DaemonEvent{
		Type: "error",
		Message: fmt.Sprintf("pre-delivery sync %s failure for %s in %s: delivery deferred, post kept for retry",
			phase, fields.MessageID, filepath.Base(sessionDir)),
	})
}

// postPaneIdentityTimeout is the real deadline for one cached-pane identity
// check on the post path.
const postPaneIdentityTimeout = 2 * time.Second

// postPaneIdentityLookup returns the live session name and pane title of a
// tmux pane. It is a variable so tests can inject topology changes.
var postPaneIdentityLookup = func(paneID string) (sessionName, title string, err error) {
	out, err := tmuxrunner.Command{Timeout: postPaneIdentityTimeout}.Output(
		"display-message", "-p", "-t", paneID, "#{session_name}\t#{pane_title}")
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(strings.TrimRight(string(out), "\r\n"), "\t", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("unexpected display-message output %q", string(out))
	}
	return parts[0], parts[1], nil
}

// postEndpointsIdentityValid reports whether the cached topology may be reused
// for this post: sender and recipient must be present AND still be the same
// live pane (same session and title as the cached node key). A same-key node
// whose pane was replaced, renamed, or moved fails the check, so the post path
// falls back to a full discovery. Non-tmux backends are never reused because
// their identity cannot be verified with one bounded tmux call. Any lookup
// error fails closed (no reuse).
func (rt *daemonRuntime) postEndpointsIdentityValid(sourceSessionDir, filename string) bool {
	if !rt.postEndpointsKnown(sourceSessionDir, filename) {
		return false
	}
	info, err := message.ParseMessageFilename(filename)
	if err != nil {
		return false
	}
	sourceSessionName := filepath.Base(sourceSessionDir)
	for _, endpoint := range []string{info.From, info.To} {
		if endpoint == "postman" || endpoint == "daemon" {
			continue
		}
		nodeKey := discovery.ResolveNodeName(endpoint, sourceSessionName, rt.nodes)
		node := rt.nodes[nodeKey]
		if node.Backend != "" && !strings.EqualFold(node.Backend, "tmux") {
			return false
		}
		sessionName, title, err := postPaneIdentityLookup(node.PaneID)
		if err != nil {
			return false
		}
		if sessionName != node.SessionName || nodeKey != sessionName+":"+title {
			return false
		}
	}
	return true
}
