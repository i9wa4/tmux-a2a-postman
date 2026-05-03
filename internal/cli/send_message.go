package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

type sendStatus string

const (
	sendStatusProcessed         sendStatus = "processed"
	sendStatusQueued            sendStatus = "queued"
	sendOutcomeObservationDelay            = 250 * time.Millisecond
)

type cliNotifyStatus string

const (
	cliNotifyOK      cliNotifyStatus = "OK"
	cliNotifyFailed  cliNotifyStatus = "FAILED"
	cliNotifySkipped cliNotifyStatus = "SKIPPED"
	cliNotifyNone    cliNotifyStatus = ""
)

type sendOutput struct {
	Sent        string                `json:"sent"`
	Status      string                `json:"status"`
	ContextID   string                `json:"context_id,omitempty"`
	Session     string                `json:"session,omitempty"`
	From        string                `json:"from,omitempty"`
	To          string                `json:"to,omitempty"`
	ReplyPolicy string                `json:"reply_policy,omitempty"`
	ReplyTo     string                `json:"reply_to,omitempty"`
	SubmitPath  projection.SubmitPath `json:"submit_path,omitempty"`
	Notify      string                `json:"notify,omitempty"`
}

type sendToPaneFunc func(paneID, message string, enterDelay, tmuxTimeout time.Duration, enterCount int, bypassCooldown bool, verifyDelay time.Duration, maxRetries int) error

// performCLINotification sends a synchronous pane notification from the CLI.
// Returns cliNotifySkipped when paneID is empty, cliNotifyOK on success, cliNotifyFailed on error.
func performCLINotification(paneID, notificationMsg string, enterDelay, tmuxTimeout time.Duration, enterCount int, bypassCooldown bool, verifyDelay time.Duration, maxRetries int, fn sendToPaneFunc) cliNotifyStatus {
	if paneID == "" {
		return cliNotifySkipped
	}
	if err := fn(paneID, notificationMsg, enterDelay, tmuxTimeout, enterCount, bypassCooldown, verifyDelay, maxRetries); err != nil {
		return cliNotifyFailed
	}
	return cliNotifyOK
}

func RunSendMessage(args []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	to := fs.String("to", "", "recipient node name (required)")
	body := fs.String("body", "", "message body (required)")
	noReply := fs.Bool("no-reply", false, "mark message as not requiring a reply")
	replyRequired := fs.Bool("reply-required", false, "mark message as requiring a reply")
	replyTo := fs.String("reply-to", "", "message id this message replies to")
	contextID := fs.String("context-id", "", "context ID (optional, auto-detected)")
	configPath := fs.String("config", "", "config file path (optional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" {
		return fmt.Errorf("--to is required")
	}
	if err := cliutil.ValidateNodeAddress("--to", *to); err != nil {
		return err
	}
	// NOTE: runCreateDraft issues only a warning (not an error) for --send
	// without --body (see runCreateDraft:966-968). Enforce here before
	// delegating so send never sends a placeholder-body message.
	if *body == "" {
		return fmt.Errorf("--body is required")
	}
	if *noReply && *replyRequired {
		return fmt.Errorf("--no-reply and --reply-required are mutually exclusive")
	}
	if err := validateReplyToMessageID(*replyTo); err != nil {
		return err
	}
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	sender := config.GetTmuxPaneName()
	if sender == "" {
		return fmt.Errorf("sender auto-detection failed: set tmux pane title")
	}
	if err := cliutil.ValidateOutboundNodeName("auto-detected pane title", sender); err != nil {
		return err
	}

	sessionName := config.GetTmuxSessionName()
	if sessionName == "" {
		return fmt.Errorf("tmux session name required (run inside tmux)")
	}
	sessionName, err = config.ValidateSessionName(sessionName)
	if err != nil {
		return fmt.Errorf("invalid session name: %w", err)
	}

	var resolvedContextID string
	if *contextID != "" {
		resolvedContextID, err = config.ResolveContextID(*contextID)
		if err != nil {
			return err
		}
	} else {
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
		if err != nil {
			return err
		}
	}

	adjacency, err := config.ParseEdges(cfg.Edges)
	if err != nil {
		return fmt.Errorf("parsing edges: %w", err)
	}
	recipientSessionName, recipientSimpleName, recipientHasSession := nodeaddr.Split(*to)
	senderCandidates := []string{sender}
	senderFullName := nodeaddr.Full(sender, sessionName)
	if senderFullName != sender {
		senderCandidates = append(senderCandidates, senderFullName)
	}
	senderPresent := false
	seenNeighbors := make(map[string]bool)
	talksToList := []string{}
	for _, candidate := range senderCandidates {
		neighbors, ok := adjacency[candidate]
		if !ok {
			continue
		}
		senderPresent = true
		for _, neighbor := range neighbors {
			if seenNeighbors[neighbor] {
				continue
			}
			seenNeighbors[neighbor] = true
			talksToList = append(talksToList, neighbor)
		}
	}
	recipientCandidates := []string{*to}
	if !recipientHasSession {
		recipientFullName := nodeaddr.Full(recipientSimpleName, sessionName)
		if recipientFullName != *to {
			recipientCandidates = append(recipientCandidates, recipientFullName)
		}
	} else if recipientSessionName == sessionName {
		recipientCandidates = append(recipientCandidates, recipientSimpleName)
	}
	canTalkTo := strings.Join(talksToList, ", ")
	if !senderPresent {
		return fmt.Errorf("missing sender: %q is not present in configured edges", sender)
	}
	recipientPresent := false
	for _, candidate := range recipientCandidates {
		if _, ok := adjacency[candidate]; ok {
			recipientPresent = true
			break
		}
	}
	if !recipientPresent {
		return fmt.Errorf("missing receiver: %q is not present in configured edges", *to)
	}
	recipientAllowed := false
	for _, n := range talksToList {
		for _, candidate := range recipientCandidates {
			if n == candidate {
				recipientAllowed = true
				break
			}
		}
		if recipientAllowed {
			break
		}
	}
	if !recipientAllowed {
		return fmt.Errorf("edge violation: %q cannot send to %q — not allowed; allowed recipients: %s",
			sender, *to, canTalkTo)
	}
	sessionDir := filepath.Join(baseDir, resolvedContextID, sessionName)
	draftDir := filepath.Join(sessionDir, "draft")
	if err := os.MkdirAll(draftDir, 0o700); err != nil {
		return fmt.Errorf("creating draft directory: %w", err)
	}

	now := time.Now()
	ts := now.Format("20060102-150405")
	filename, err := message.GenerateFilename(ts, sender, *to, sessionName)
	if err != nil {
		return fmt.Errorf("generating filename: %w", err)
	}
	replyPolicy := message.ResolveReplyPolicyForSend(*body, *noReply, *replyRequired)
	draftPath := filepath.Join(draftDir, filename)

	content := cfg.DraftTemplate
	if content == "" {
		content = "---\nparams:\n  contextId: {context_id}\n  from: {sender}\n  to: {recipient}\n  timestamp: {timestamp}\n---\n\nYou can only talk to: {can_talk_to}\n\n# Content\n\n"
	}

	vars := map[string]string{
		"context_id":     resolvedContextID,
		"sender":         sender,
		"recipient":      *to,
		"timestamp":      now.Format(time.RFC3339),
		"can_talk_to":    canTalkTo,
		"session_dir":    filepath.Join(baseDir, resolvedContextID, sessionName),
		"reply_command":  strings.ReplaceAll(envelope.RenderReplyCommand(cfg.ReplyCommand, resolvedContextID, *to), "<recipient>", *to),
		"message_id":     filename,
		"reply_policy":   replyPolicy,
		"reply_to":       *replyTo,
		"template":       getNodeTemplate(cfg, *to),
		"session_name":   sessionName,
		"sender_pane_id": config.GetTmuxPaneID(),
	}

	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	content = template.ExpandTemplate(content, vars, timeout, cfg.AllowShellForDraftTemplate())

	stripped, err := notification.StripVT(*body)
	if err != nil {
		return fmt.Errorf("--body contains invalid UTF-8: %w", err)
	}
	content = strings.ReplaceAll(content, "<!-- write here -->", stripped)
	content = message.EnsureEnvelopeParams(content, map[string]string{
		"messageId":   filename,
		"replyPolicy": replyPolicy,
		"replyTo":     *replyTo,
	})

	if cfg.MessageFooter != "" {
		footerVars := make(map[string]string, len(vars))
		for k, v := range vars {
			footerVars[k] = v
		}
		footerTalksToList := config.GetTalksTo(adjacency, *to)
		if len(footerTalksToList) == 0 {
			recipientSimpleName := nodeaddr.Simple(*to)
			if recipientSimpleName != *to {
				footerTalksToList = config.GetTalksTo(adjacency, recipientSimpleName)
			}
		}
		footerVars["can_talk_to"] = strings.Join(footerTalksToList, ", ")
		footerVars["reply_command"] = strings.ReplaceAll(
			envelope.RenderReplyCommand(cfg.ReplyCommand, resolvedContextID, sender),
			"<recipient>",
			sender,
		)
		footerVars["message_id"] = filename
		footerVars["reply_policy"] = replyPolicy
		footerVars["reply_to"] = *replyTo
		footer := template.ExpandTemplate(cfg.MessageFooter, footerVars, timeout, cfg.AllowShellForMessageFooter())
		content = strings.TrimRight(content, "\n") + "\n\n---\n\n" + footer + "\n"
	}

	if config.ContextOwnsSession(baseDir, resolvedContextID, sessionName) {
		response, err := roundTripDaemonSubmit(sessionDir, projection.DaemonSubmitRequest{
			Command:  projection.DaemonSubmitSend,
			Filename: filename,
			Content:  content,
		}, daemonSubmitTimeout(cfg.TmuxTimeout))
		if err != nil {
			return fmt.Errorf("daemon submit send: %w", err)
		}
		deliveredFilename := filename
		if response.Filename != "" {
			deliveredFilename = response.Filename
		}
		status, err := observeSendOutcome(baseDir, resolvedContextID, sessionDir, deliveredFilename)
		if err != nil {
			return fmt.Errorf("send outcome: %w", err)
		}
		return writeSendOutput(sendOutput{
			Sent:        deliveredFilename,
			Status:      string(status),
			ContextID:   resolvedContextID,
			Session:     sessionName,
			From:        sender,
			To:          *to,
			ReplyPolicy: replyPolicy,
			ReplyTo:     *replyTo,
			SubmitPath:  projection.SubmitPathDaemon,
		})
	}

	if err := os.WriteFile(draftPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("writing draft: %w", err)
	}

	postDir := filepath.Clean(filepath.Join(draftDir, "..", "post"))
	if err := os.MkdirAll(postDir, 0o700); err != nil {
		return fmt.Errorf("creating post/ directory: %w", err)
	}
	dst := filepath.Join(postDir, filename)
	if err := os.Rename(draftPath, dst); err != nil {
		return fmt.Errorf("sending draft: %w", err)
	}
	status, err := observeSendOutcome(baseDir, resolvedContextID, sessionDir, filename)
	if err != nil {
		return fmt.Errorf("send outcome: %w", err)
	}
	var notifyStatus cliNotifyStatus
	if status == sendStatusProcessed {
		freshNodes, _ := discovery.DiscoverNodes(baseDir, resolvedContextID, sessionName)
		var paneID string
		if freshNodes != nil {
			fullKey := discovery.ResolveNodeName(*to, sessionName, freshNodes)
			if nodeInfo, ok := freshNodes[fullKey]; ok {
				paneID = nodeInfo.PaneID
			}
		}
		notificationMsg := notification.BuildNotification(cfg, adjacency, freshNodes, resolvedContextID, *to, sender, sessionName, filename, nil)
		recipientSimpleName := nodeaddr.Simple(*to)
		enterDelay := time.Duration(cfg.EnterDelay * float64(time.Second))
		if nd := cfg.GetNodeConfig(recipientSimpleName).EnterDelay; nd != 0 {
			enterDelay = time.Duration(nd * float64(time.Second))
		}
		tmuxTimeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
		enterCount := cfg.GetNodeConfig(recipientSimpleName).EnterCount
		if enterCount == 0 {
			enterCount = 1
		}
		verifyDelay := time.Duration(cfg.EnterVerifyDelay * float64(time.Second))
		notifyStatus = performCLINotification(paneID, notificationMsg, enterDelay, tmuxTimeout, enterCount, true, verifyDelay, cfg.EnterRetryMax, notification.SendToPane)
	}
	return writeSendOutput(sendOutput{
		Sent:        filename,
		Status:      string(status),
		ContextID:   resolvedContextID,
		Session:     sessionName,
		From:        sender,
		To:          *to,
		ReplyPolicy: replyPolicy,
		ReplyTo:     *replyTo,
		SubmitPath:  projection.SubmitPathPost,
		Notify:      notifyOutputValue(notifyStatus),
	})
}

func validateReplyToMessageID(replyTo string) error {
	if replyTo == "" {
		return nil
	}
	if strings.ContainsAny(replyTo, "/\\") {
		return fmt.Errorf("--reply-to must be a message id, not a path")
	}
	if strings.ContainsAny(replyTo, " \t\r\n") {
		return fmt.Errorf("--reply-to must be a single message id token")
	}
	if _, err := message.ParseMessageFilename(replyTo); err != nil {
		return fmt.Errorf("--reply-to must be a valid message id: %w", err)
	}
	return nil
}

// getNodeTemplate retrieves the template for a given node from config,
// prepending common_template if present (mirrors BuildEnvelope/BuildRoleContent).
// Returns empty string if node or template is not found (nil-safe).
func getNodeTemplate(cfg *config.Config, nodeName string) string {
	if cfg == nil || cfg.Nodes == nil {
		return ""
	}
	nodeConfig, exists := cfg.Nodes[nodeName]
	if !exists {
		nodeConfig, exists = cfg.Nodes[strings.SplitN(nodeName, ":", 2)[len(strings.SplitN(nodeName, ":", 2))-1]]
	}
	if !exists {
		return ""
	}
	tmpl := nodeConfig.Template
	if cfg.CommonTemplate != "" && tmpl != "" {
		return cfg.CommonTemplate + "\n\n" + tmpl
	}
	if cfg.CommonTemplate != "" {
		return cfg.CommonTemplate
	}
	return tmpl
}

func writeSendResult(filename string, status sendStatus, notifyStatus cliNotifyStatus) error {
	return writeSendOutput(sendOutput{
		Sent:   filename,
		Status: string(status),
		Notify: notifyOutputValue(notifyStatus),
	})
}

func notifyOutputValue(notifyStatus cliNotifyStatus) string {
	switch notifyStatus {
	case cliNotifyOK:
		return "OK"
	case cliNotifyFailed:
		return "FAILED"
	case cliNotifySkipped:
		return "SKIPPED"
	default:
		return ""
	}
}

func writeSendOutput(output sendOutput) error {
	return json.NewEncoder(os.Stdout).Encode(output)
}

func observeSendOutcome(baseDir, contextID, sessionDir, filename string) (sendStatus, error) {
	if deadLetterBasename, ok, err := findMatchingDeadLetter(sessionDir, filename); err != nil {
		return "", err
	} else if ok {
		return "", fmt.Errorf("message dead-lettered: %s", deadLetterBasename)
	}
	if !config.ContextHasLiveDaemon(baseDir, contextID) {
		return sendStatusQueued, nil
	}

	postPath := filepath.Join(sessionDir, "post", filename)
	deadline := time.Now().Add(sendOutcomeObservationDelay)
	for {
		if deadLetterBasename, ok, err := findMatchingDeadLetter(sessionDir, filename); err != nil {
			return "", err
		} else if ok {
			return "", fmt.Errorf("message dead-lettered: %s", deadLetterBasename)
		}

		if _, err := os.Stat(postPath); err != nil {
			if os.IsNotExist(err) {
				return sendStatusProcessed, nil
			}
			return "", fmt.Errorf("checking post queue state: %w", err)
		}
		if time.Now().After(deadline) {
			return sendStatusQueued, nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func findMatchingDeadLetter(sessionDir, filename string) (string, bool, error) {
	deadLetterDir := filepath.Join(sessionDir, "dead-letter")
	entries, err := os.ReadDir(deadLetterDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("reading dead-letter directory: %w", err)
	}

	prefix := strings.TrimSuffix(filename, ".md") + "-dl-"
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, ".md") {
			return name, true, nil
		}
	}
	return "", false, nil
}
