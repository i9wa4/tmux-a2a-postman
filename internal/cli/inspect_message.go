package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

type inspectMessageOutput struct {
	Status      string                `json:"status"`
	Reason      string                `json:"reason,omitempty"`
	ID          string                `json:"id"`
	MatchCount  int                   `json:"match_count"`
	Message     *inspectMessageMatch  `json:"message,omitempty"`
	Matches     []inspectMessageMatch `json:"matches,omitempty"`
	ContextID   string                `json:"context_id,omitempty"`
	SessionName string                `json:"session_name,omitempty"`
}

type inspectMessageMatch struct {
	MessageID           string         `json:"message_id"`
	MarkdownPath        string         `json:"markdown_path"`
	StorageState        string         `json:"storage_state"`
	Node                string         `json:"node,omitempty"`
	Frontmatter         map[string]any `json:"frontmatter,omitempty"`
	From                string         `json:"from,omitempty"`
	To                  string         `json:"to,omitempty"`
	ReplyPolicy         string         `json:"reply_policy,omitempty"`
	ReplyTo             string         `json:"reply_to,omitempty"`
	InputRequestID      string         `json:"input_request_id,omitempty"`
	FillsInputRequestID string         `json:"fills_input_request_id,omitempty"`
	InputRequestSetID   string         `json:"input_request_set_id,omitempty"`
	BranchID            string         `json:"branch_id,omitempty"`
	CompletionRule      string         `json:"completion_rule,omitempty"`
	Timestamp           string         `json:"timestamp,omitempty"`
}

func RunInspectMessage(args []string) error {
	fs := flag.NewFlagSet("inspect-message", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from tmux session)")
	configPath := fs.String("config", "", "Config file path")
	sessionName := fs.String("session", "", "tmux session name (optional, defaults to current tmux session)")
	id := fs.String("id", "", "message_id to inspect")
	jsonOutput := fs.Bool("json", false, "print structured JSON output (default)")
	pathOnly := fs.Bool("path", false, "print only the matched Markdown path")
	bodyOnly := fs.Bool("body", false, "print only the matched Markdown body")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}
	if (*pathOnly || *bodyOnly) && *jsonOutput {
		return fmt.Errorf("--json cannot be combined with --path or --body")
	}
	if *pathOnly && *bodyOnly {
		return fmt.Errorf("--path and --body are mutually exclusive")
	}

	sessionDir, resolvedContextID, resolvedSessionName, err := resolveInspectMessageSessionDir(*contextID, *sessionName, *configPath)
	if err != nil {
		return err
	}
	matches, unproven, err := findInspectMessageMatches(sessionDir, *id)
	if err != nil {
		return err
	}

	output := inspectMessageOutput{
		Status:      "not_found",
		ID:          *id,
		MatchCount:  len(matches),
		ContextID:   resolvedContextID,
		SessionName: resolvedSessionName,
	}
	switch len(matches) {
	case 0:
		unpopped, err := inspectMessageHasUnpoppedCopy(sessionDir, *id)
		if err != nil {
			return err
		}
		switch {
		case unproven > 0:
			output.Status = "not_claimed"
			output.Reason = "no_journaled_read"
		case unpopped:
			output.Status = "not_claimed"
			output.Reason = "unread_inbox"
		}
	case 1:
		output.Status = "found"
		output.Message = &matches[0]
	default:
		output.Status = "ambiguous"
		output.Matches = matches
	}

	if *pathOnly || *bodyOnly {
		return writeInspectMessagePlainOutput(output, *pathOnly, *bodyOnly)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func resolveInspectMessageSessionDir(contextID, sessionName, configPath string) (string, string, string, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return "", "", "", fmt.Errorf("loading config: %w", err)
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)
	if sessionName == "" {
		sessionName = config.GetTmuxSessionName()
		if sessionName == "" {
			return "", "", "", fmt.Errorf("tmux session name required (run inside tmux or pass --session)")
		}
	}
	sessionName, err = config.ValidateSessionName(sessionName)
	if err != nil {
		return "", "", "", err
	}

	if contextID != "" {
		contextID, err = config.ResolveContextID(contextID)
	} else {
		contextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
	}
	if err != nil {
		return "", "", "", err
	}

	return filepath.Join(baseDir, contextID, sessionName), contextID, sessionName, nil
}

// inspectMessageLegacyArchiveCutoff separates archives that predate journaled
// read events from current ones. It is compared with the archive FILE's
// modification time, which pop's rename preserves from the delivery time, never
// with the timestamp in the file name: a message that is named long ago but
// delivered and popped (or orphaned) now has a recent file time. The date is a
// heuristic (pop receipts, PR #591, merged 2026-06-28, are the nearest marker;
// two days of slack are added).
var inspectMessageLegacyArchiveCutoff = time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)

// inspectMessageClaimEvidence is what the journal says about popped messages in
// the current session generation.
type inspectMessageClaimEvidence struct {
	// reads maps "read/<file name>" to the message id recorded by the journaled
	// mailbox_projection_read event for that path. It is derived from
	// projection.ProjectMailboxProjection, the same source that decides which
	// files the projection sync keeps under read/.
	reads map[string]string
}

// loadInspectMessageClaimEvidence reads the claim evidence. A session without a
// usable journal yields empty evidence (only the legacy rule can then apply); a
// journal that cannot be replayed is an error, so the command fails closed.
func loadInspectMessageClaimEvidence(sessionDir string) (inspectMessageClaimEvidence, error) {
	evidence := inspectMessageClaimEvidence{reads: map[string]string{}}
	projected, ok, err := projection.ProjectMailboxProjection(sessionDir)
	if err != nil {
		return evidence, fmt.Errorf("reading journaled read events: %w", err)
	}
	if !ok {
		return evidence, nil
	}
	for _, file := range projected.Read {
		path := filepath.ToSlash(file.Path)
		evidence.reads[path] = parseMessageContent(file.Content, filepath.Base(path)).MessageID
	}
	return evidence, nil
}

// findInspectMessageMatches returns only claimed messages: those archived under
// read/ AND proven to have been popped (see inspectMessageClaimProven). Unread
// inbox mail is consumed through pop, so its content is never returned here (see
// inspectMessageHasUnpoppedCopy). The second result counts read/ files that carry
// the id but lack the proof; only their existence is reported, never their
// content.
func findInspectMessageMatches(sessionDir, id string) ([]inspectMessageMatch, int, error) {
	evidence, err := loadInspectMessageClaimEvidence(sessionDir)
	if err != nil {
		return nil, 0, err
	}
	var matches []inspectMessageMatch
	unproven := 0
	if err := inspectMessageReadDir(filepath.Join(sessionDir, "read"), id, evidence, &matches, &unproven); err != nil {
		return nil, 0, err
	}
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].MarkdownPath < matches[j].MarkdownPath
	})
	return matches, unproven, nil
}

// inspectMessageClaimProven decides whether a read/ archive is evidence of a
// successful pop. A read/ file alone is not: a pop that fails after the archive
// rename (the #802 rollback race) can leave an orphan there.
//
// Proof is the journaled read event for exactly this path, recorded for this
// message id, in the current session generation. It is the fact the projection
// sync itself uses to keep the archive under read/, so it survives sync (a pop
// receipt file does not: sync deletes unjournaled files under read/).
//
// If the journal has a read for this path, the journal alone decides; the
// legacy rule never overrides it. Only when the journal has nothing for the path
// is an archive whose file time predates inspectMessageLegacyArchiveCutoff
// accepted, because archives from before journaled reads have no event.
//
// Known residual (#802, not closable from this command): a journaled read can
// also come from the daemon read watcher replaying an orphan archive, and a
// journaled read whose stdout delivery to the caller failed still counts as
// claimed because the system consumed the message.
func inspectMessageClaimProven(path, filename, messageID string, evidence inspectMessageClaimEvidence) bool {
	if recorded, ok := evidence.reads["read/"+filename]; ok {
		return recorded == messageID
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.ModTime().Before(inspectMessageLegacyArchiveCutoff)
}

// inspectMessageHasUnpoppedCopy reports whether an unread inbox message carries
// the id. It only answers existence so the caller can say "not_claimed"; the
// matched content is discarded and never reaches the output.
func inspectMessageHasUnpoppedCopy(sessionDir, id string) (bool, error) {
	var unread []inspectMessageMatch
	if err := inspectMessageInboxDir(filepath.Join(sessionDir, "inbox"), id, &unread); err != nil {
		return false, err
	}
	return len(unread) > 0, nil
}

func inspectMessageReadDir(readDir, id string, evidence inspectMessageClaimEvidence, matches *[]inspectMessageMatch, unproven *int) error {
	entries, err := os.ReadDir(readDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading read directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		path := filepath.Join(readDir, entry.Name())
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading message: %w", err)
		}
		if !inspectMessageMatchesID(string(content), entry.Name(), id) {
			continue
		}
		if !inspectMessageClaimProven(path, entry.Name(), parseMessageContent(string(content), entry.Name()).MessageID, evidence) {
			*unproven++
			continue
		}
		if err := appendInspectMessageMatch(path, entry.Name(), "read", "", id, matches); err != nil {
			return err
		}
	}
	return nil
}

func inspectMessageInboxDir(inboxDir, id string, matches *[]inspectMessageMatch) error {
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading inbox directory: %w", err)
	}
	for _, nodeEntry := range entries {
		if !nodeEntry.IsDir() {
			continue
		}
		nodeName := nodeEntry.Name()
		nodeInboxDir := filepath.Join(inboxDir, nodeName)
		if err := filepath.WalkDir(nodeInboxDir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				return nil
			}
			return appendInspectMessageMatch(path, entry.Name(), "unread", nodeName, id, matches)
		}); err != nil {
			return fmt.Errorf("reading inbox directory: %w", err)
		}
	}
	return nil
}

func appendInspectMessageMatch(path, filename, storageState, nodeName, id string, matches *[]inspectMessageMatch) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading message: %w", err)
	}
	if !inspectMessageMatchesID(string(content), filename, id) {
		return nil
	}
	payload := parseMessageContent(string(content), filename)
	*matches = append(*matches, inspectMessageMatch{
		MessageID:           payload.MessageID,
		MarkdownPath:        path,
		StorageState:        storageState,
		Node:                nodeName,
		Frontmatter:         payload.Frontmatter,
		From:                payload.From,
		To:                  payload.To,
		ReplyPolicy:         payload.ReplyPolicy,
		ReplyTo:             payload.ReplyTo,
		InputRequestID:      payload.InputRequestID,
		FillsInputRequestID: payload.FillsInputRequestID,
		InputRequestSetID:   payload.InputRequestSetID,
		BranchID:            payload.BranchID,
		CompletionRule:      payload.CompletionRule,
		Timestamp:           payload.Timestamp,
	})
	return nil
}

func inspectMessageMatchesID(content, filename, id string) bool {
	if filename == id {
		return true
	}
	metadata, err := envelope.ParseMetadata(content)
	if err != nil {
		return false
	}
	return metadata.MessageID == id
}

func writeInspectMessagePlainOutput(output inspectMessageOutput, pathOnly, bodyOnly bool) error {
	if output.Status == "not_claimed" {
		return fmt.Errorf("not_claimed (%s): message id %q has no proof of a successful pop; claim it with pop first", output.Reason, output.ID)
	}
	if output.Status != "found" || output.Message == nil {
		return fmt.Errorf("%s: message id %q matched %d files", output.Status, output.ID, output.MatchCount)
	}
	if pathOnly {
		if _, err := fmt.Fprintln(os.Stdout, output.Message.MarkdownPath); err != nil {
			return err
		}
		return nil
	}
	if bodyOnly {
		content, err := os.ReadFile(output.Message.MarkdownPath)
		if err != nil {
			return fmt.Errorf("reading message body: %w", err)
		}
		body, exact := envelope.SenderBodyFromContent(string(content))
		if exact {
			if _, err := fmt.Fprint(os.Stdout, body); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintln(os.Stdout, body); err != nil {
				return err
			}
		}
		return nil
	}
	return nil
}
