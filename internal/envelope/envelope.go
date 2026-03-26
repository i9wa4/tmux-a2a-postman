package envelope

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
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
	// Role template resolution: Nodes → CommonTemplate prepend.
	recipientTemplate := ""
	if nodeConfig, ok := cfg.Nodes[recipient]; ok {
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
		nodeConfig := cfg.GetNodeConfig(node)
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

	// reply_command: inject --context-id if missing from send-message commands,
	// then expand {context_id} literal. Uses notification-path logic.
	replyCmd := cfg.ReplyCommand
	if strings.Contains(replyCmd, "send-message") && !strings.Contains(replyCmd, "--context-id") {
		if strings.Contains(replyCmd, "--to") {
			replyCmd = strings.Replace(replyCmd, "--to", fmt.Sprintf("--context-id %s --to", contextID), 1)
		} else {
			replyCmd = fmt.Sprintf("%s --context-id %s", replyCmd, contextID)
		}
	}
	replyCmd = strings.ReplaceAll(replyCmd, "{context_id}", contextID)
	// ping.go also replaced {node} in reply_command — preserve that behavior.
	replyCmd = strings.ReplaceAll(replyCmd, "{node}", recipient)

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
	inboxPath := filepath.Join(sessionDir, "inbox", recipient)

	now := time.Now()
	ts := now.Format("20060102-150405")

	vars := map[string]string{
		"context_id":       contextID,
		"node":             recipient,
		"from_node":        sender,
		"timestamp":        ts,
		"iso_timestamp":    now.Format(time.RFC3339),
		"filename":         filepath.Base(filename),
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
	return template.ExpandTemplate(tmpl, vars, timeout, cfg.AllowShellTemplates)
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
