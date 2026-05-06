package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

type inspectInputOutput struct {
	Status      string              `json:"status"`
	ID          string              `json:"id"`
	MatchCount  int                 `json:"match_count"`
	Matches     []inspectInputMatch `json:"matches,omitempty"`
	ContextID   string              `json:"context_id,omitempty"`
	SessionName string              `json:"session_name,omitempty"`
}

type inspectInputMatch struct {
	ContextID    string                    `json:"context_id"`
	SessionName  string                    `json:"session_name"`
	Node         string                    `json:"node"`
	MatchedBy    string                    `json:"matched_by"`
	InputRequest status.InputRequestDetail `json:"input_request"`
}

func RunInspectInput(args []string) error {
	fs := flag.NewFlagSet("inspect-input", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from tmux session)")
	configPath := fs.String("config", "", "Config file path")
	sessionName := fs.String("session", "", "tmux session name (optional, defaults to current tmux session)")
	id := fs.String("id", "", "message_id or input_request_id to inspect")
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

	output := inspectInputOutput{
		Status:      "not_found",
		ID:          *id,
		ContextID:   health.ContextID,
		SessionName: health.SessionName,
	}
	for _, node := range health.Nodes {
		if node.Flow == nil {
			continue
		}
		output.Matches = append(output.Matches, inspectInputMatches(*id, health, node, node.Flow.InputRequests.InputRequired)...)
		output.Matches = append(output.Matches, inspectInputMatches(*id, health, node, node.Flow.InputRequests.WaitingOnInput)...)
	}
	output.MatchCount = len(output.Matches)
	if output.MatchCount > 0 {
		output.Status = "found"
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func inspectInputMatches(id string, health status.SessionHealth, node status.NodeHealth, inputRequests []status.InputRequestDetail) []inspectInputMatch {
	if len(inputRequests) == 0 {
		return nil
	}
	var matches []inspectInputMatch
	for _, inputRequest := range inputRequests {
		matchedBy := inputRequestMatchField(id, inputRequest)
		if matchedBy == "" {
			continue
		}
		matches = append(matches, inspectInputMatch{
			ContextID:    health.ContextID,
			SessionName:  health.SessionName,
			Node:         node.Name,
			MatchedBy:    matchedBy,
			InputRequest: inputRequest,
		})
	}
	return matches
}

func inputRequestMatchField(id string, inputRequest status.InputRequestDetail) string {
	if inputRequest.InputRequestID == id {
		return "input_request_id"
	}
	if inputRequest.MessageID == id {
		return "message_id"
	}
	return ""
}
