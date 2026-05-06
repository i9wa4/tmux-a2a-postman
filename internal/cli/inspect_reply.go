package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

type inspectReplyOutput struct {
	Status      string              `json:"status"`
	ID          string              `json:"id"`
	MatchCount  int                 `json:"match_count"`
	Matches     []inspectReplyMatch `json:"matches,omitempty"`
	ContextID   string              `json:"context_id,omitempty"`
	SessionName string              `json:"session_name,omitempty"`
}

type inspectReplyMatch struct {
	ContextID   string                 `json:"context_id"`
	SessionName string                 `json:"session_name"`
	Node        string                 `json:"node"`
	MatchedBy   string                 `json:"matched_by"`
	ReplySlot   status.ReplySlotDetail `json:"reply_slot"`
}

func RunInspectReply(args []string) error {
	fs := flag.NewFlagSet("inspect-reply", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from tmux session)")
	configPath := fs.String("config", "", "Config file path")
	sessionName := fs.String("session", "", "tmux session name (optional, defaults to current tmux session)")
	id := fs.String("id", "", "message_id or reply_slot_id to inspect")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return fmt.Errorf("--id is required")
	}

	health, ok, err := collectResolvedSessionHealth(*contextID, *sessionName, *configPath)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("tmux session name required (run inside tmux or pass --session)")
	}

	output := inspectReplyOutput{
		Status:      "not_found",
		ID:          *id,
		ContextID:   health.ContextID,
		SessionName: health.SessionName,
	}
	for _, node := range health.Nodes {
		if node.Flow == nil {
			continue
		}
		output.Matches = append(output.Matches, inspectReplyMatches(*id, health, node, node.Flow.ReplySlots.ActionRequired)...)
		output.Matches = append(output.Matches, inspectReplyMatches(*id, health, node, node.Flow.ReplySlots.WaitingOnReply)...)
	}
	output.MatchCount = len(output.Matches)
	if output.MatchCount > 0 {
		output.Status = "found"
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func inspectReplyMatches(id string, health status.SessionHealth, node status.NodeHealth, replySlots []status.ReplySlotDetail) []inspectReplyMatch {
	if len(replySlots) == 0 {
		return nil
	}
	var matches []inspectReplyMatch
	for _, replySlot := range replySlots {
		matchedBy := replySlotMatchField(id, replySlot)
		if matchedBy == "" {
			continue
		}
		matches = append(matches, inspectReplyMatch{
			ContextID:   health.ContextID,
			SessionName: health.SessionName,
			Node:        node.Name,
			MatchedBy:   matchedBy,
			ReplySlot:   replySlot,
		})
	}
	return matches
}

func replySlotMatchField(id string, replySlot status.ReplySlotDetail) string {
	if replySlot.ReplySlotID == id {
		return "reply_slot_id"
	}
	if replySlot.MessageID == id {
		return "message_id"
	}
	return ""
}
