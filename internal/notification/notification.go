package notification

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/i9wa4/tmux-a2a-postman/internal/agentruntime"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/paneutil"
)

var (
	defaultPaneNotifier = NewPaneNotifier(10 * time.Minute)

	// bufferMu serializes tmux set-buffer + paste-buffer pairs.
	// tmux uses a single global paste buffer; concurrent SendToPane calls
	// would race without this lock. The lock is held only for the two fast
	// tmux commands (~1ms each); the expensive sleep + send-keys runs outside.
	bufferMu sync.Mutex
)

// PaneNotifier sends notifications to tmux panes with instance-scoped cooldown state.
type PaneNotifier struct {
	mu           sync.Mutex
	lastNotified map[string]time.Time
	cooldown     time.Duration

	now     func() time.Time
	sleep   func(time.Duration)
	runTmux func(args ...string) error
	capture func(paneID string) (string, error)
	stderr  io.Writer
}

type PaneDelivery struct {
	PaneID         string
	Message        string
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
// verifyDelay > 0 enables post-Enter capture comparison: after C-m, waits verifyDelay,
// captures pane, waits again, captures again; if identical, retries C-m up to maxRetries.
func SendToPane(paneID string, message string, enterDelay time.Duration, tmuxTimeout time.Duration, enterCount int, bypassCooldown bool, verifyDelay time.Duration, maxRetries int) error {
	return TmuxPaneSender{}.DeliverPane(PaneDelivery{
		PaneID:         paneID,
		Message:        message,
		EnterDelay:     enterDelay,
		TmuxTimeout:    tmuxTimeout,
		EnterCount:     enterCount,
		BypassCooldown: bypassCooldown,
		VerifyDelay:    verifyDelay,
		MaxRetries:     maxRetries,
	})
}

// SendToPane sends a message to a tmux pane using this notifier's dependencies and cooldown state.
func (n *PaneNotifier) SendToPane(paneID string, message string, enterDelay time.Duration, tmuxTimeout time.Duration, enterCount int, bypassCooldown bool, verifyDelay time.Duration, maxRetries int) error {
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
	// Wrap with protocol sentinels so all pane output is clearly delimited.
	message = "<!-- message start -->\n" + message + "\n<!-- end of message -->"
	// Security: Sanitize message for tmux set-buffer (#301)
	sanitized, err := sanitizeForTmux(message)
	if err != nil {
		n.warnf("⚠️  postman: WARNING: sanitizeForTmux: %v\n", err)
		return err
	}

	// 1-2. Set buffer + paste buffer (serialized via bufferMu to prevent
	// global tmux paste-buffer race when deliveries run concurrently).
	bufferMu.Lock()
	if err := n.run("set-buffer", sanitized); err != nil {
		bufferMu.Unlock()
		n.warnf("⚠️  postman: WARNING: failed to set buffer for pane %s: %v\n", paneID, err)
		return err
	}
	if err := n.run("paste-buffer", "-t", paneID); err != nil {
		bufferMu.Unlock()
		n.warnf("⚠️  postman: WARNING: failed to paste buffer to pane %s: %v\n", paneID, err)
		return err
	}
	bufferMu.Unlock()

	// 3. Wait enter_delay (runs outside bufferMu — parallel across panes)
	n.sleepFor(enterDelay)

	// 4. Send C-m to submit. C-m (carriage return) submits reliably in both Codex CLI and claude-chill.
	// "Enter" key name adds a newline in Codex CLI multi-line readline instead of submitting (#126).
	if err := n.run("send-keys", "-t", paneID, "C-m"); err != nil {
		n.warnf("⚠️  postman: WARNING: failed to send C-m to pane %s: %v\n", paneID, err)
		return err
	}

	// 5. Send additional C-m keystrokes up to enterCount total
	for i := 1; i < enterCount; i++ {
		n.sleepFor(enterDelay)
		if err := n.run("send-keys", "-t", paneID, "C-m"); err != nil {
			return fmt.Errorf("failed to send C-m %d to pane %s: %w", i+1, paneID, err)
		}
	}

	// 6. Post-Enter verify: capture-compare-retry to detect swallowed Enter
	if verifyDelay > 0 && maxRetries > 0 {
		for retry := 0; retry < maxRetries; retry++ {
			n.sleepFor(verifyDelay)
			snapA, errA := n.capturePane(paneID)
			if errA != nil {
				break // cannot verify; skip
			}
			n.sleepFor(verifyDelay)
			snapB, errB := n.capturePane(paneID)
			if errB != nil {
				break
			}
			if snapA != snapB {
				break // pane content changed; Enter was accepted
			}
			// Pane unchanged — retry C-m silently so alt-screen TUI panes stay clean.
			_ = n.run("send-keys", "-t", paneID, "C-m")
		}
	}

	return nil
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

func (n *PaneNotifier) run(args ...string) error {
	if n.runTmux != nil {
		return n.runTmux(args...)
	}
	return exec.Command("tmux", args...).Run()
}

func (n *PaneNotifier) capturePane(paneID string) (string, error) {
	if n.capture != nil {
		return n.capture(paneID)
	}
	return paneutil.CaptureContent(paneID)
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

// sanitizeForTmux sanitizes a string for safe use with tmux set-buffer
// by stripping VT/ANSI sequences and invalid UTF-8 (#301).
func sanitizeForTmux(s string) (string, error) {
	return StripVT(s)
}
