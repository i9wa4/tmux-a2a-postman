package notification

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/i9wa4/tmux-a2a-postman/internal/agentruntime"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/paneutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/tmuxrunner"
)

// ErrPaneUnresponsive indicates that post-Enter verification could not prove
// the intended input was submitted on the same pane, including exhausted
// retries or an ambiguous capture (#816).
var ErrPaneUnresponsive = errors.New("pane unresponsive: notification not submitted after verify retries")

var defaultPaneNotifier = NewPaneNotifier(10 * time.Minute)

// PaneNotifier sends notifications to tmux panes with instance-scoped cooldown state.
type PaneNotifier struct {
	mu           sync.Mutex
	lastNotified map[string]time.Time
	cooldown     time.Duration

	now      func() time.Time
	sleep    func(time.Duration)
	runTmux  func(args ...string) error
	capture  func(paneID string) (string, error)
	identify func(paneID string) (string, error)
	stderr   io.Writer
}

type PaneDelivery struct {
	PaneID         string
	Message        string
	Filename       string
	EnterDelay     time.Duration
	TmuxTimeout    time.Duration
	EnterCount     int
	BypassCooldown bool
	VerifyDelay    time.Duration
	MaxRetries     int
}

type PaneSender interface {
	DeliverPane(delivery PaneDelivery) error
}

type PaneSenderFunc func(delivery PaneDelivery) error

func (f PaneSenderFunc) DeliverPane(delivery PaneDelivery) error {
	return f(delivery)
}

type TmuxPaneSender struct {
	Notifier *PaneNotifier
}

func (s TmuxPaneSender) DeliverPane(delivery PaneDelivery) error {
	notifier := s.Notifier
	if notifier == nil {
		notifier = defaultPaneNotifier
	}
	return notifier.SendToPane(
		delivery.PaneID,
		delivery.Message,
		delivery.EnterDelay,
		delivery.TmuxTimeout,
		delivery.EnterCount,
		delivery.BypassCooldown,
		delivery.VerifyDelay,
		delivery.MaxRetries,
		delivery.Filename,
	)
}

// NewPaneNotifier creates a pane notifier with the given per-pane cooldown.
func NewPaneNotifier(cooldown time.Duration) *PaneNotifier {
	return &PaneNotifier{
		lastNotified: make(map[string]time.Time),
		cooldown:     cooldown,
	}
}

// InitPaneCooldown sets the per-pane notification cooldown duration.
// Must be called once at startup before any SendToPane calls.
func InitPaneCooldown(d time.Duration) {
	defaultPaneNotifier.InitCooldown(d)
}

// InitCooldown sets this notifier's per-pane cooldown duration.
func (n *PaneNotifier) InitCooldown(d time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.cooldown = d
}

// BuildNotification builds a notification message using notification_template.
// Variables available: from_node, node, timestamp, filename, inbox_path,
// talks_to_line, template, reply_command, context_id.
// recipient and sender are simple node names (not session-prefixed).
// sourceSessionName is the session name where the message originated.
func BuildNotification(cfg *config.Config, adjacency map[string][]string, nodes map[string]discovery.NodeInfo, contextID, recipient, sender, sourceSessionName, filename string, livenessMap map[string]bool) string {
	return envelope.BuildNotificationEnvelope(cfg, cfg.NotificationTemplate, recipient, sender, contextID, filename, nil, adjacency, nodes, sourceSessionName, livenessMap)
}

// SendToPane sends a message to a tmux pane using set-buffer + paste-buffer.
// Security: Sanitizes message before passing to tmux set-buffer.
// Error handling: Logs errors but does not fail (graceful degradation).
// enterCount controls how many C-m keystrokes to send; 0 or 1 sends one, N>=2 sends N total.
// bypassCooldown skips the per-pane rate limit; direct message delivery passes true.
// verifyDelay > 0 enables post-Enter verification: it checks the same pane's
// visible input prompts for the intended notification after a short settled
// observation, and retries C-m up to maxRetries only while it remains pending.
func SendToPane(paneID string, message string, enterDelay time.Duration, tmuxTimeout time.Duration, enterCount int, bypassCooldown bool, verifyDelay time.Duration, maxRetries int) error {
	return SendToPaneWithFilename(paneID, message, "", enterDelay, tmuxTimeout, enterCount, bypassCooldown, verifyDelay, maxRetries)
}

// SendToPaneWithFilename uses the trusted delivery filename independently of
// the rendered notification template when checking the pane after Enter.
func SendToPaneWithFilename(paneID, message, filename string, enterDelay, tmuxTimeout time.Duration, enterCount int, bypassCooldown bool, verifyDelay time.Duration, maxRetries int) error {
	return TmuxPaneSender{}.DeliverPane(PaneDelivery{
		PaneID:         paneID,
		Message:        message,
		Filename:       filename,
		EnterDelay:     enterDelay,
		TmuxTimeout:    tmuxTimeout,
		EnterCount:     enterCount,
		BypassCooldown: bypassCooldown,
		VerifyDelay:    verifyDelay,
		MaxRetries:     maxRetries,
	})
}

// SendToPane sends a message to a tmux pane using this notifier's dependencies and cooldown state.
func (n *PaneNotifier) SendToPane(paneID string, message string, enterDelay time.Duration, tmuxTimeout time.Duration, enterCount int, bypassCooldown bool, verifyDelay time.Duration, maxRetries int, trustedFilename ...string) error {
	if n == nil {
		n = defaultPaneNotifier
	}
	if strings.TrimSpace(message) == "" {
		err := fmt.Errorf("empty notification body for pane %s", paneID)
		n.warnf("postman: notification: %v\n", err)
		return err
	}
	// Rate limit: skip if pane was notified within cooldown window (#273).
	// paneNotifyCooldown <= 0 disables the limiter (used in tests).
	now := n.nowTime()
	n.mu.Lock()
	if n.lastNotified == nil {
		n.lastNotified = make(map[string]time.Time)
	}
	cooldown := n.cooldown
	if !bypassCooldown && cooldown > 0 {
		if last, ok := n.lastNotified[paneID]; ok && now.Sub(last) < cooldown {
			n.mu.Unlock()
			n.warnf("postman: notification: cooldown active for pane %s (last=%s, cooldown=%s)\n", paneID, last.Format(time.RFC3339), cooldown)
			return nil
		}
	}
	n.lastNotified[paneID] = now
	n.mu.Unlock()
	verify := verifyDelay > 0 && maxRetries > 0
	filename := notificationFilename(message)
	if len(trustedFilename) > 0 && trustedFilename[0] != "" {
		filename = trustedFilename[0]
	}
	// A valid custom template may omit {filename}. Include a useful message
	// reference in the submitted notice so verification still has a cue tied
	// to this delivery, without rejecting or changing the configured template.
	if verify && filename != "" && !containsFilenameToken(message, filename) {
		message += "\nMessage: " + filename
	}
	sanitized, err := PrepareInteractivePaneMessage(message)
	if err != nil {
		n.warnf("⚠️  postman: WARNING: sanitizeForTmux: %v\n", err)
		return err
	}
	var incarnation string
	if verify {
		if filename == "" {
			return fmt.Errorf("%w: notification has no message filename", ErrPaneUnresponsive)
		}
		incarnation, err = n.paneIdentity(paneID)
		if err != nil || incarnation == "" {
			return fmt.Errorf("%w: pane %s identity unavailable: %v", ErrPaneUnresponsive, paneID, err)
		}
		baseline, captureErr := n.captureVerified(paneID, incarnation)
		if captureErr != nil || strings.Contains(baseline, filename) {
			return fmt.Errorf("%w: pane %s baseline unavailable or contains stale notification: %v", ErrPaneUnresponsive, paneID, captureErr)
		}
	}

	// 1-4. Set buffer, paste it into the pane, and submit with C-m as one
	// chained tmux invocation (#800). tmux executes a semicolon-separated
	// command list for a single client atomically, so there is no longer a
	// Go-level gap between these steps (previously separate exec.Command
	// calls with an intervening time.Sleep) in which a daemon restart could
	// leave text pasted with no Enter ever sent: this single process
	// invocation either runs to completion or never starts. The enter delay
	// (if any) runs inside the same invocation via tmux's own run-shell, so
	// the whole sequence -- including the wait -- is one atomic call from
	// the daemon's perspective. C-m (carriage return) submits reliably in
	// both Codex CLI and claude-chill; the "Enter" key name adds a newline
	// in Codex CLI multi-line readline instead of submitting (#126).
	chainTimeout := atomicChainTimeout(tmuxTimeout, enterDelay)
	if err := n.run(chainTimeout, atomicFirstEnterArgs(sanitized, paneID, enterDelay)...); err != nil {
		n.warnf("⚠️  postman: WARNING: failed to paste and submit to pane %s: %v\n", paneID, err)
		// The chain may have pasted (and even submitted) before the deadline
		// hit, so the delivery state is ambiguous: report it as
		// ErrPaneUnresponsive so callers do not treat it as a plain failure
		// and re-paste a second copy of the notification (#871).
		if ambiguous := ambiguousDeadlineError(paneID, "chained paste/submit", chainTimeout, err); ambiguous != nil {
			return ambiguous
		}
		return err
	}

	// 5. Send additional C-m keystrokes up to enterCount total. The paste has
	// already happened, so a deadline here is ambiguous too and must not lead
	// the caller to re-paste (#871).
	for i := 1; i < enterCount; i++ {
		n.sleepFor(enterDelay)
		if err := n.run(tmuxTimeout, "send-keys", "-t", paneID, "C-m"); err != nil {
			if ambiguous := ambiguousDeadlineError(paneID, fmt.Sprintf("C-m %d", i+1), tmuxTimeout, err); ambiguous != nil {
				return ambiguous
			}
			return fmt.Errorf("failed to send C-m %d to pane %s: %w", i+1, paneID, err)
		}
	}

	// 6. Post-Enter verify: observe the same pane after the UI settles. A spinner
	// or footer can change while the notification remains unsent in the composer.
	// Check after the final retry, rather than reporting before its Enter.
	if verify {
		for retry := 0; retry <= maxRetries; retry++ {
			n.sleepFor(verifyDelay)
			_, errA := n.captureVerified(paneID, incarnation)
			if errA != nil {
				return fmt.Errorf("%w: pane %s immediate capture: %v", ErrPaneUnresponsive, paneID, errA)
			}
			n.sleepFor(verifyDelay)
			snapB, errB := n.captureVerified(paneID, incarnation)
			if errB != nil {
				return fmt.Errorf("%w: pane %s settled capture: %v", ErrPaneUnresponsive, paneID, errB)
			}
			if submittedNotification(snapB, filename) {
				return nil
			}
			pending, known := composerHasNotification(snapB, filename)
			if !known || !pending {
				return fmt.Errorf("%w: pane %s has no correlated input transition", ErrPaneUnresponsive, paneID)
			}
			if retry == maxRetries {
				return fmt.Errorf("%w: pane %s notification still in composer after %d verify retries", ErrPaneUnresponsive, paneID, maxRetries)
			}
			if err := n.run(tmuxTimeout, "send-keys", "-t", paneID, "C-m"); err != nil {
				if ambiguous := ambiguousDeadlineError(paneID, "verify-retry C-m", tmuxTimeout, err); ambiguous != nil {
					return ambiguous
				}
				return fmt.Errorf("failed to retry C-m for pane %s: %w", paneID, err)
			}
		}
	}

	return nil
}

// ambiguousDeadlineError returns a non-retryable ErrPaneUnresponsive error
// (keeping the deadline cause) when err is a tmux deadline at a stage that
// follows, or is, the paste, and nil for any other error. Once the notification
// text may be in the pane, retrying the whole delivery would paste it again.
func ambiguousDeadlineError(paneID, stage string, timeout time.Duration, err error) error {
	if !errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return fmt.Errorf("%w: %s to pane %s timed out after %s, delivery state ambiguous, not retried: %w", ErrPaneUnresponsive, stage, paneID, timeout, err)
}

func notificationFilename(message string) string {
	const prefix = "You've got mail: "
	start := strings.Index(message, prefix)
	if start < 0 {
		return ""
	}
	fields := strings.Fields(message[start+len(prefix):])
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], "`.,;: ")
}

// composerHasNotification examines only the last Claude or Codex input prompt
// on the visible screen. An older submitted prompt may still appear in the
// transcript while the current composer at the bottom is empty.
func composerHasNotification(snapshot, filename string) (pending, known bool) {
	if filename == "" {
		return false, false
	}
	lines := strings.Split(snapshot, "\n")
	lastPrompt := -1
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "❯") || strings.HasPrefix(trimmed, "›") {
			lastPrompt = i
		}
	}
	if lastPrompt < 0 {
		return false, false
	}
	return promptHasNotification(lines, lastPrompt, len(lines), filename), true
}

// promptHasNotification attributes a filename only to the prompt's own text.
// A framed multiline paste may continue below the prompt; ordinary output may
// also appear there, so only lines inside the explicit message frame count.
func promptHasNotification(lines []string, start, end int, filename string) bool {
	if start < 0 || start >= end {
		return false
	}
	prompt := strings.TrimLeft(lines[start], " \t")
	switch {
	case strings.HasPrefix(prompt, "❯"):
		prompt = strings.TrimSpace(strings.TrimPrefix(prompt, "❯"))
	case strings.HasPrefix(prompt, "›"):
		prompt = strings.TrimSpace(strings.TrimPrefix(prompt, "›"))
	default:
		return false
	}
	if prompt != "<!-- message start -->" {
		return containsFilenameToken(prompt, filename)
	}
	found := false
	for _, line := range lines[start+1 : end] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "<!-- end of message -->" {
			return found
		}
		if strings.HasPrefix(trimmed, "─") {
			return false
		}
		found = found || containsFilenameToken(line, filename)
	}
	return false
}

func containsFilenameToken(text, filename string) bool {
	if filename == "" {
		return false
	}
	for offset := 0; offset < len(text); {
		i := strings.Index(text[offset:], filename)
		if i < 0 {
			return false
		}
		i += offset
		before := i == 0 || !filenameTokenByte(text[i-1])
		after := i+len(filename) == len(text) || !filenameTokenByte(text[i+len(filename)])
		// A sentence period may follow the filename; a .bak suffix may not.
		if !after && text[i+len(filename)] == '.' {
			end := i + len(filename) + 1
			after = end == len(text) || !filenameTokenByte(text[end])
		}
		if before && after {
			return true
		}
		offset = i + 1
	}
	return false
}

func filenameTokenByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-' || b == '.'
}

// submittedNotification requires the intended message in a previous input
// prompt and a newer prompt without it. Output, footers, and an empty prompt
// alone cannot establish that Enter submitted this notification.
func submittedNotification(snapshot, filename string) bool {
	if filename == "" {
		return false
	}
	lines := strings.Split(snapshot, "\n")
	last, submitted := -1, false
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "❯") || strings.HasPrefix(trimmed, "›") {
			if last >= 0 {
				submitted = submitted || promptHasNotification(lines, last, i, filename)
			}
			last = i
		}
	}
	pending, known := composerHasNotification(snapshot, filename)
	return submitted && known && !pending
}

// bufferSeq generates unique tmux buffer names for atomicFirstEnterArgs.
var bufferSeq atomic.Uint64

// nextBufferName returns a name for this process guaranteed not to collide
// with another concurrent SendToPane call's buffer, another daemon process,
// or a human/tool using tmux's unnamed default buffer (#800 F-030): tmux's
// paste buffer is server-global and nothing in tmux's contract guarantees
// one client's set-buffer+paste-buffer pair is indivisible against another
// client's concurrent set-buffer -- a named buffer per call makes
// non-collision structural rather than relying on the server's (unspecified)
// internal scheduling.
func nextBufferName() string {
	return fmt.Sprintf("postman-notify-%d-%d", os.Getpid(), bufferSeq.Add(1))
}

// atomicFirstEnterArgs builds one chained tmux command line that sets a
// uniquely-named paste buffer, pastes it into paneID (deleting the buffer
// immediately after), and submits it with a real C-m keystroke (#800). The
// enter delay, when positive, runs between paste and submit via tmux's own
// run-shell so it stays inside this single invocation rather than a
// Go-level time.Sleep between separate commands.
func atomicFirstEnterArgs(sanitized, paneID string, enterDelay time.Duration) []string {
	bufName := nextBufferName()
	args := []string{"set-buffer", "-b", bufName, sanitized, ";", "paste-buffer", "-b", bufName, "-d", "-t", paneID}
	if enterDelay > 0 {
		args = append(args, ";", "run-shell", fmt.Sprintf("sleep %.3f", enterDelay.Seconds()))
	}
	return append(args, ";", "send-keys", "-t", paneID, "C-m")
}

func (n *PaneNotifier) nowTime() time.Time {
	if n.now != nil {
		return n.now()
	}
	return time.Now()
}

func (n *PaneNotifier) sleepFor(d time.Duration) {
	if n.sleep != nil {
		n.sleep(d)
		return
	}
	time.Sleep(d)
}

// atomicChainMargin is the headroom added on top of the tmux timeout and the
// in-chain enter delay for process start-up and scheduling.
const atomicChainMargin = time.Second

// atomicChainTimeout returns the deadline for the chained
// set-buffer/paste-buffer/run-shell sleep/send-keys C-m invocation (#800). The
// enter delay runs inside that single tmux client, so a deadline that does not
// cover it could kill the client after the paste but before C-m, leaving an
// unsubmitted notification in the composer. A timeout still returns an error
// (never delivery success), and the post-Enter verify loop is unchanged (#871).
func atomicChainTimeout(tmuxTimeout, enterDelay time.Duration) time.Duration {
	if tmuxTimeout <= 0 {
		tmuxTimeout = tmuxrunner.DefaultTimeout
	}
	if enterDelay < 0 {
		enterDelay = 0
	}
	return tmuxTimeout + enterDelay + atomicChainMargin
}

// run executes one tmux invocation under a real deadline (#871). A
// non-positive timeout falls back to tmuxrunner.DefaultTimeout, so a hung tmux
// server can never block the caller indefinitely.
func (n *PaneNotifier) run(timeout time.Duration, args ...string) error {
	if n.runTmux != nil {
		return n.runTmux(args...)
	}
	return tmuxrunner.Command{Timeout: timeout}.Run(args...)
}

func (n *PaneNotifier) capturePane(paneID string) (string, error) {
	if n.capture != nil {
		return n.capture(paneID)
	}
	return paneutil.CaptureContent(paneID)
}

func (n *PaneNotifier) paneIdentity(paneID string) (string, error) {
	if n.identify != nil {
		return n.identify(paneID)
	}
	out, err := tmuxrunner.Output("display-message", "-p", "-t", paneID,
		"#{socket_path}\t#{pid}\t#{start_time}\t#{session_id}\t#{pane_id}\t#{pane_pid}")
	if err != nil {
		return "", err
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(fields) != 6 || fields[4] != paneID {
		return "", fmt.Errorf("incomplete pane identity")
	}
	for _, field := range fields {
		if field == "" {
			return "", fmt.Errorf("incomplete pane identity")
		}
	}
	return strings.Join(fields, "\t"), nil
}

func (n *PaneNotifier) captureVerified(paneID, incarnation string) (string, error) {
	before, err := n.paneIdentity(paneID)
	if err != nil || before != incarnation {
		return "", fmt.Errorf("pane incarnation changed before capture: %v", err)
	}
	text, err := n.capturePane(paneID)
	if err != nil {
		return "", err
	}
	after, err := n.paneIdentity(paneID)
	if err != nil || after != incarnation {
		return "", fmt.Errorf("pane incarnation changed after capture: %v", err)
	}
	return text, nil
}

func (n *PaneNotifier) stderrWriter() io.Writer {
	if n.stderr != nil {
		return n.stderr
	}
	return os.Stderr
}

func (n *PaneNotifier) warnf(format string, args ...any) {
	_, _ = fmt.Fprintf(n.stderrWriter(), format, args...)
}

// ResolveEnterCount returns the effective enter count for pane delivery.
// When configured == 0, probes runtime automatically via probeRuntime.
// probeRuntime returns the running command name for the pane.
func ResolveEnterCount(configured int, probeRuntime func() (string, error)) int {
	if configured == 0 {
		runtime, err := probeRuntime()
		if err == nil && agentruntime.Normalize(runtime) == agentruntime.Codex {
			return 2
		}
		return 1
	} else if configured > 1 {
		runtime, err := probeRuntime()
		if err != nil || agentruntime.Normalize(runtime) != agentruntime.Codex {
			return 1
		}
	}
	return configured
}

// StripVT strips VT/ANSI control sequences and C0/C1 control characters from s
// using a state-machine parser (ECMA-48). LF (U+000A) is preserved.
// Returns an error if s contains invalid UTF-8.
func StripVT(s string) (string, error) {
	// Pre-pass: normalize CRLF and bare CR to LF (#225).
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	const (
		stateNormal = iota
		stateEsc
		stateCSI
		stateString
		stateStrEsc
	)

	state := stateNormal
	var buf []byte
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return "", fmt.Errorf("invalid UTF-8 at byte offset %d", i)
		}
		i += size

		switch state {
		case stateNormal:
			switch {
			case r == '\n': // U+000A LF: preserve
				buf = utf8.AppendRune(buf, r)
			case r == 0x1B: // ESC
				state = stateEsc
			case r >= 0x20 && r <= 0x7E: // printable ASCII
				buf = utf8.AppendRune(buf, r)
			case r >= 0x00A0: // above C1 range: valid Unicode, non-control
				buf = utf8.AppendRune(buf, r)
			default: // NUL, C0 excl LF, TAB, DEL, C1 U+0080-U+009F: drop
			}

		case stateEsc:
			state = stateNormal // default: drop ESC + this byte, return to Normal
			switch r {
			case 'P', 'X', ']', '^', '_': // string sequence introducers
				state = stateString
			case '[': // CSI introducer
				state = stateCSI
			}

		case stateCSI:
			if r >= 0x40 && r <= 0x7E { // final byte
				state = stateNormal
			}
			// else: drop parameter/intermediate bytes

		case stateString:
			switch r {
			case 0x07: // BEL: string terminator
				state = stateNormal
			case 0x1B: // ESC: possible ST
				state = stateStrEsc
			}
			// else: drop string body

		case stateStrEsc:
			switch r {
			case 0x07: // BEL inside stateStrEsc: string terminated
				state = stateNormal
			case '\\': // ST confirmed (ESC + backslash)
				state = stateNormal
			default: // ESC was not ST: continue consuming string
				state = stateString
			}
		}
	}

	return string(buf), nil
}

// PrepareInteractivePaneMessage wraps pane delivery with protocol sentinels and
// strips VT/ANSI sequences before the target backend receives text.
func PrepareInteractivePaneMessage(message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("empty notification body")
	}
	message = "<!-- message start -->\n" + message + "\n<!-- end of message -->"
	return sanitizeForPaneText(message)
}

// sanitizeForPaneText sanitizes a string for safe interactive pane text by
// stripping VT/ANSI sequences and invalid UTF-8 (#301).
func sanitizeForPaneText(s string) (string, error) {
	return StripVT(s)
}
