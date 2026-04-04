package envelope

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

// BuildEnvelope builds a message string by expanding tmpl with a shared set of variables.
// Shared logic: role-template resolution, sentinel obfuscation, talks_to_line construction
// (session-aware, same-session priority, liveness-filtered), and reply_command building
// (--context-id injection).
//
// tmpl is caller-provided: pass cfg.DaemonMessageTemplate for ping/alert/heartbeat,
// or cfg.NotificationTemplate for pane notification hints.
//
// NOTE: resolveNodeName is inlined here as accepted debt from #141.
// Follow-up: consolidate into internal/discovery — see issue #148.
func BuildEnvelope(
	cfg *config.Config,
	tmpl string,
	recipient string,
	sender string,
	contextID string,
	filename string,
	activeNodes []string,
	adjacency map[string][]string,
	nodes map[string]discovery.NodeInfo,
	sourceSessionName string,
	livenessMap map[string]bool,
) string {
	allowShell := false
	if cfg != nil {
		allowShell = cfg.AllowShellTemplates
	}
	return buildEnvelope(cfg, tmpl, recipient, sender, contextID, filename, activeNodes, adjacency, nodes, sourceSessionName, livenessMap, allowShell)
}

func BuildNotificationEnvelope(
	cfg *config.Config,
	tmpl string,
	recipient string,
	sender string,
	contextID string,
	filename string,
	activeNodes []string,
	adjacency map[string][]string,
	nodes map[string]discovery.NodeInfo,
	sourceSessionName string,
	livenessMap map[string]bool,
) string {
	return buildEnvelope(cfg, tmpl, recipient, sender, contextID, filename, activeNodes, adjacency, nodes, sourceSessionName, livenessMap, cfg.AllowShellForNotificationTemplate())
}

func BuildDaemonEnvelope(
	cfg *config.Config,
	tmpl string,
	recipient string,
	sender string,
	contextID string,
	filename string,
	activeNodes []string,
	adjacency map[string][]string,
	nodes map[string]discovery.NodeInfo,
	sourceSessionName string,
	livenessMap map[string]bool,
) string {
	return buildEnvelope(cfg, tmpl, recipient, sender, contextID, filename, activeNodes, adjacency, nodes, sourceSessionName, livenessMap, cfg.AllowShellForDaemonMessageTemplate())
}

func buildEnvelope(
	cfg *config.Config,
	tmpl string,
	recipient string,
	sender string,
	contextID string,
	filename string,
	activeNodes []string,
	adjacency map[string][]string,
	nodes map[string]discovery.NodeInfo,
	sourceSessionName string,
	livenessMap map[string]bool,
	allowShell bool,
) string {
	recipientSimple := nodeaddr.Simple(recipient)
	senderSimple := nodeaddr.Simple(sender)

	// Role template resolution: Nodes → CommonTemplate prepend.
	recipientTemplate := ""
	if nodeConfig, ok := cfg.Nodes[recipientSimple]; ok {
		recipientTemplate = nodeConfig.Template
	}
	// Issue #49: Prepend common_template if present.
	if cfg.CommonTemplate != "" {
		if recipientTemplate != "" {
			recipientTemplate = cfg.CommonTemplate + "\n\n" + recipientTemplate
		} else {
			recipientTemplate = cfg.CommonTemplate
		}
	}

	// Sentinel obfuscation: prevent user-configured template content from terminating the
	// protocol prematurely. @path references are unaffected (no sentinel in file paths).
	recipientTemplate = strings.ReplaceAll(recipientTemplate, "<!-- end of message -->", "<!-- end of msg -->")

	// talks_to_line: session-aware liveness-filtered neighbor list.
	// Uses notification-path semantics: same-session priority, exact key lookup.
	talksTo := config.GetTalksTo(adjacency, recipient)
	if len(talksTo) == 0 && recipientSimple != recipient {
		talksTo = config.GetTalksTo(adjacency, recipientSimple)
	}
	activeTalksTo := []string{}
	for _, node := range talksTo {
		nodeFullName := discovery.ResolveNodeName(node, sourceSessionName, nodes)
		if nodeFullName != "" && (livenessMap == nil || livenessMap[nodeFullName]) {
			activeTalksTo = append(activeTalksTo, node)
		}
	}
	talksToLine := ""
	if len(activeTalksTo) > 0 {
		talksToLine = fmt.Sprintf("Can talk to: %s", strings.Join(activeTalksTo, ", "))
	}

	// contacts_section: adjacent nodes with role descriptions (liveness-filtered, or all-adjacent when nil).
	contactLines := []string{}
	for _, node := range activeTalksTo {
		nodeConfig := cfg.GetNodeConfig(nodeaddr.Simple(node))
		if nodeConfig.Role != "" {
			contactLines = append(contactLines, fmt.Sprintf("- %s: %s", node, nodeConfig.Role))
		} else {
			contactLines = append(contactLines, fmt.Sprintf("- %s", node))
		}
	}
	contactsSection := strings.Join(contactLines, "\n")
	if contactsSection == "" {
		contactsSection = "- none"
	}

	replyCmd := RenderReplyCommand(cfg.ReplyCommand, contextID, recipientSimple)

	// Resolve recipient session directory for inbox_path and session_dir.
	sessionDir := ""
	recipientFullName := discovery.ResolveNodeName(recipient, sourceSessionName, nodes)
	if recipientFullName != "" {
		if recipientInfo, found := nodes[recipientFullName]; found {
			sessionDir = recipientInfo.SessionDir
		}
	}
	if sessionDir == "" {
		// Fallback: derive from filename path (post/ → session dir).
		sessionDir = filepath.Dir(filepath.Dir(filename))
	}
	inboxPath := filepath.Join(sessionDir, "inbox", recipientSimple)

	now := time.Now()
	ts := now.Format("20060102-150405")

	// Extract sent_timestamp from filename prefix (YYYYMMDD-HHMMSS).
	// Falls back to "" when filename is absent or has unexpected format.
	// Digit check prevents non-numeric chars from passing through as timestamps.
	isAllDigits := func(s string) bool {
		for _, r := range s {
			if r < '0' || r > '9' {
				return false
			}
		}
		return len(s) > 0
	}
	sentTimestamp := ""
	base := filepath.Base(filename)
	if parts := strings.SplitN(base, "-", 3); len(parts) >= 2 && len(parts[0]) == 8 && len(parts[1]) == 6 && isAllDigits(parts[0]) && isAllDigits(parts[1]) {
		sentTimestamp = parts[0] + "-" + parts[1]
	}

	vars := map[string]string{
		"context_id":       contextID,
		"node":             recipientSimple,
		"from_node":        senderSimple,
		"timestamp":        ts,
		"iso_timestamp":    now.Format(time.RFC3339),
		"sent_timestamp":   sentTimestamp,
		"filename":         base,
		"inbox_path":       inboxPath,
		"talks_to_line":    talksToLine,
		"contacts_section": contactsSection,
		"template":         recipientTemplate,
		"reply_command":    replyCmd,
		"session_dir":      sessionDir,
		"active_nodes":     strings.Join(activeNodes, ", "),
		"session_name":     sourceSessionName,
	}

	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	return template.ExpandTemplate(tmpl, vars, timeout, allowShell)
}

// RenderReplyCommand normalizes the configured reply command and expands the
// placeholders used by envelope, daemon alerts, and draft templates.
func RenderReplyCommand(replyCmd, contextID, recipient string) string {
	replyCmd = legacySendMessageRe.ReplaceAllString(replyCmd, "send")
	fields := strings.Fields(replyCmd)
	for i, field := range fields {
		if field == "send-message" {
			fields[i] = "send"
		}
	}
	if containsToken(fields, "send") && !strings.Contains(replyCmd, "--context-id") {
		if strings.Contains(replyCmd, "--to") {
			replyCmd = strings.Replace(replyCmd, "--to", fmt.Sprintf("--context-id %s --to", contextID), 1)
		} else {
			replyCmd = fmt.Sprintf("%s --context-id %s", replyCmd, contextID)
		}
	}
	replyCmd = strings.ReplaceAll(replyCmd, "{context_id}", contextID)
	replyCmd = strings.ReplaceAll(replyCmd, "{node}", recipient)
	return replyCmd
}

var legacySendMessageRe = regexp.MustCompile(`\bsend-message\b`)

func containsToken(fields []string, want string) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}

// BuildRoleContent returns canonical role content for a node with sentinel obfuscation.
// Resolution: config template with CommonTemplate prepend.
// Used by sendAlertToUINode, SendPingToNode, heartbeat, idle.
func BuildRoleContent(cfg *config.Config, nodeName string) string {
	nc := cfg.GetNodeConfig(nodeName)
	nodeTemplate := nc.Template
	roleContent := ""
	if cfg.CommonTemplate != "" && nodeTemplate != "" {
		roleContent = cfg.CommonTemplate + "\n\n" + nodeTemplate
	} else if cfg.CommonTemplate != "" {
		roleContent = cfg.CommonTemplate
	} else {
		roleContent = nodeTemplate
	}
	return strings.ReplaceAll(roleContent, "<!-- end of message -->", "<!-- end of msg -->")
}
