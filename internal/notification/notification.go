package notification

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
)

var (
	paneNotifyMu       sync.Mutex
	paneLastNotified   = map[string]time.Time{}
	paneNotifyCooldown = 10 * time.Minute
)

// InitPaneCooldown sets the per-pane notification cooldown duration.
// Must be called once at startup before any SendToPane calls.
func InitPaneCooldown(d time.Duration) {
	paneNotifyMu.Lock()
	paneNotifyCooldown = d
	paneNotifyMu.Unlock()
}

// BuildNotification builds a notification message using notification_template.
// Variables available: from_node, node, timestamp, filename, inbox_path,
// talks_to_line, template, reply_command, context_id.
// recipient and sender are simple node names (not session-prefixed).
// sourceSessionName is the session name where the message originated.
func BuildNotification(cfg *config.Config, adjacency map[string][]string, nodes map[string]discovery.NodeInfo, contextID, recipient, sender, sourceSessionName, filename string, livenessMap map[string]bool) string {
	return envelope.BuildEnvelope(cfg, cfg.NotificationTemplate, recipient, sender, contextID, "", filename, nil, adjacency, nodes, sourceSessionName, livenessMap)
}

// SendToPane sends a message to a tmux pane using set-buffer + paste-buffer.
// Security: Sanitizes message before passing to tmux set-buffer.
// Error handling: Logs errors but does not fail (graceful degradation).
// enterCount controls how many C-m keystrokes to send; 0 or 1 sends one, N>=2 sends N total.
// bypassCooldown skips the per-pane rate limit; pass true for direct message delivery,
// false for periodic reminders/alerts where the cooldown should apply.
func SendToPane(paneID string, message string, enterDelay time.Duration, tmuxTimeout time.Duration, enterCount int, bypassCooldown bool) error {
	// Rate limit: skip if pane was notified within cooldown window (#273).
	// paneNotifyCooldown <= 0 disables the limiter (used in tests).
	paneNotifyMu.Lock()
	if !bypassCooldown && paneNotifyCooldown > 0 {
		if last, ok := paneLastNotified[paneID]; ok && time.Since(last) < paneNotifyCooldown {
			paneNotifyMu.Unlock()
			return nil
		}
	}
	paneLastNotified[paneID] = time.Now()
	paneNotifyMu.Unlock()
	// Wrap with protocol sentinels so all pane output is clearly delimited.
	message = "<!-- message start -->\n" + message + "\n<!-- end of message -->"
	// Security: Sanitize message for tmux set-buffer (#301)
	sanitized, err := sanitizeForTmux(message)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  postman: WARNING: sanitizeForTmux: %v\n", err)
		return err
	}

	// 1. Set buffer
	cmd := exec.Command("tmux", "set-buffer", sanitized)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  postman: WARNING: failed to set buffer for pane %s: %v\n", paneID, err)
		return err
	}

	// 2. Paste buffer to target pane
	cmd = exec.Command("tmux", "paste-buffer", "-t", paneID)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  postman: WARNING: failed to paste buffer to pane %s: %v\n", paneID, err)
		return err
	}

	// 3. Wait enter_delay
	time.Sleep(enterDelay)

	// 4. Send C-m to submit. C-m (carriage return) submits reliably in both Codex CLI and claude-chill.
	// "Enter" key name adds a newline in Codex CLI multi-line readline instead of submitting (#126).
	cmd = exec.Command("tmux", "send-keys", "-t", paneID, "C-m")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  postman: WARNING: failed to send C-m to pane %s: %v\n", paneID, err)
		return err
	}

	// 5. Send additional C-m keystrokes up to enterCount total
	for i := 1; i < enterCount; i++ {
		time.Sleep(enterDelay)
		cmd = exec.Command("tmux", "send-keys", "-t", paneID, "C-m")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to send C-m %d to pane %s: %w", i+1, paneID, err)
		}
	}

	return nil
}

// ResolveEnterCount returns the effective enter count for pane delivery.
// When configured == 0, probes runtime automatically via probeRuntime.
// probeRuntime returns the running command name for the pane.
func ResolveEnterCount(configured int, probeRuntime func() (string, error)) int {
	if configured == 0 {
		runtime, err := probeRuntime()
		if err == nil && runtime == "codex" {
			return 2
		}
		return 1
	} else if configured > 1 {
		runtime, err := probeRuntime()
		if err != nil || runtime != "codex" {
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
