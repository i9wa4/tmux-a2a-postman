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
// (session-aware, same-session priority, PONG-filtered), and reply_command building
// (--context-id injection).
//
// tmpl is caller-provided: pass cfg.MessageTemplate for ping/A2A envelopes, or
// cfg.NotificationTemplate for pane notification hints.
//
// NOTE: resolveNodeName is inlined here as accepted debt from #141.
// Follow-up: consolidate into internal/discovery — see issue #148.
func BuildEnvelope(
	cfg *config.Config,
	tmpl string,
	recipient string,
	sender string,
	contextID string,
	taskID string,
	filename string,
	activeNodes []string,
	adjacency map[string][]string,
	nodes map[string]discovery.NodeInfo,
	sourceSessionName string,
	pongActiveNodes map[string]bool,
) string {
	// Role template resolution: MaterializedPaths → Nodes → CommonTemplate prepend.
	templatePath := ""
	recipientTemplate := ""
	if matPath, ok := cfg.MaterializedPaths[recipient]; ok {
		// Issue #134: Template materialized as file; reference by path. Label added so agents
		// can identify the file purpose without @-prefix (which triggers autocomplete).
		templatePath = matPath
		recipientTemplate = "Role template: " + matPath + "\n"
	} else {
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
	}

	// Sentinel obfuscation: prevent user-configured template content from terminating the
	// protocol prematurely. @path references are unaffected (no sentinel in file paths).
	recipientTemplate = strings.ReplaceAll(recipientTemplate, "<!-- end of message -->", "<!-- end of msg -->")

	// talks_to_line: session-aware PONG-filtered neighbor list.
	// Uses notification-path semantics: same-session priority, exact key lookup.
	talksTo := config.GetTalksTo(adjacency, recipient)
	activeTalksTo := []string{}
	for _, node := range talksTo {
		nodeFullName := resolveNodeName(node, sourceSessionName, nodes)
		if nodeFullName != "" && pongActiveNodes[nodeFullName] {
			activeTalksTo = append(activeTalksTo, node)
		}
	}
	talksToLine := ""
	if len(activeTalksTo) > 0 {
		talksToLine = fmt.Sprintf("Can talk to: %s", strings.Join(activeTalksTo, ", "))
	}

	// reply_command: inject --context-id if missing from create-draft commands,
	// then expand {context_id} literal. Uses notification-path logic.
	replyCmd := cfg.ReplyCommand
	if strings.Contains(replyCmd, "create-draft") && !strings.Contains(replyCmd, "--context-id") {
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
	recipientFullName := resolveNodeName(recipient, sourceSessionName, nodes)
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
		"context_id":    contextID,
		"node":          recipient,
		"from_node":     sender,
		"task_id":       taskID,
		"timestamp":     ts,
		"iso_timestamp": now.Format(time.RFC3339),
		"filename":      filepath.Base(filename),
		"inbox_path":    inboxPath,
		"talks_to_line": talksToLine,
		"template":      recipientTemplate,
		"reply_command": replyCmd,
		"session_dir":   sessionDir,
		"active_nodes":  strings.Join(activeNodes, ", "),
		"template_path": templatePath,
	}

	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	return template.ExpandTemplate(tmpl, vars, timeout)
}

// resolveNodeName resolves a simple node name to a session-prefixed node name.
// Priority: same-session first, then any session.
//
// NOTE: This function is duplicated in internal/message and internal/notification.
// Consolidation into internal/discovery is tracked in issue #148.
func resolveNodeName(nodeName, sourceSessionName string, knownNodes map[string]discovery.NodeInfo) string {
	if strings.Contains(nodeName, ":") {
		if _, found := knownNodes[nodeName]; found {
			return nodeName
		}
		return ""
	}

	sameSessionKey := sourceSessionName + ":" + nodeName
	if _, found := knownNodes[sameSessionKey]; found {
		return sameSessionKey
	}

	for fullName := range knownNodes {
		parts := strings.SplitN(fullName, ":", 2)
		if len(parts) == 2 && parts[1] == nodeName {
			return fullName
		}
	}

	return ""
}
