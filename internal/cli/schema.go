package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

// RunSchema prints JSON Schema for the curated public postman config surface or a specific command's options.
//
//	tmux-a2a-postman schema      # postman.toml public config surface
//	tmux-a2a-postman schema send # send options schema
func RunSchema(args []string) error {
	return runSchema(os.Stdout, args)
}

func runSchema(stdout io.Writer, args []string) error {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")

	// Parse --nodes-dir flag before the switch (opt-in path, not inline in no-arg output).
	nodesDir := false
	filteredArgs := args[:0:0]
	for _, a := range args {
		if a == "--nodes-dir" {
			nodesDir = true
		} else {
			filteredArgs = append(filteredArgs, a)
		}
	}
	if nodesDir {
		xdgPath := config.ResolveConfigPath()
		xdgNodesDir := config.ResolveNodesDir(xdgPath)
		localConfigPath := ""
		if cwd, err := os.Getwd(); err == nil {
			localConfigPath, _ = config.ResolveLocalConfigPath(cwd, xdgPath)
		}
		localNodesDir := config.ResolveNodesDir(localConfigPath)
		return json.NewEncoder(stdout).Encode(struct {
			XDG          string `json:"xdg"`
			ProjectLocal string `json:"project_local"`
		}{XDG: xdgNodesDir, ProjectLocal: localNodesDir})
	}

	target := ""
	if len(filteredArgs) > 0 {
		target = filteredArgs[0]
	}

	type schemaProperty struct {
		Type        string `json:"type"`
		Description string `json:"description"`
	}
	type jsonSchema struct {
		Schema     string                    `json:"$schema"`
		Title      string                    `json:"title"`
		Type       string                    `json:"type"`
		Properties map[string]schemaProperty `json:"properties"`
		Required   []string                  `json:"required,omitempty"`
	}

	switch target {
	case "":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "postman.toml public config surface",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"base_dir":                            {Type: "string", Description: "Override state base directory (also: POSTMAN_HOME)"},
				"edges":                               {Type: "array", Description: "Bidirectional routing edges between named nodes"},
				"ui_node":                             {Type: "string", Description: "Human-facing node that receives daemon alerts and user_input waits"},
				"reminder_interval_messages":          {Type: "integer", Description: "Reminder cadence after archived reads (0 = disabled)"},
				"inbox_unread_threshold":              {Type: "integer", Description: "Unread-summary threshold for ui_node alerts (0 = disabled)"},
				"[node].idle_timeout_seconds":         {Type: "integer", Description: "Per-node inactivity alert threshold (0 = disabled)"},
				"[node].dropped_ball_timeout_seconds": {Type: "integer", Description: "Per-node late-reply and dropped-ball threshold (0 = disabled)"},
				"node_spinning_seconds":               {Type: "integer", Description: "Reply-tracked composing-to-spinning threshold (0 = disabled)"},
				"[heartbeat].enabled":                 {Type: "boolean", Description: "Enable periodic heartbeat messages"},
				"[heartbeat].interval_seconds":        {Type: "number", Description: "Heartbeat interval in seconds"},
				"[heartbeat].llm_node":                {Type: "string", Description: "Heartbeat recipient node"},
				"[heartbeat].prompt":                  {Type: "string", Description: "Heartbeat prompt inserted into the shared daemon envelope"},
			},
		})
	// Properties = --params scope only (excluded flags omitted; see alwaysExcludedParams)
	// SYNC: options struct fields; alwaysExcludedParams + perCommandExcludedParams maps
	case "send":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "send options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"to":              {Type: "string", Description: "Recipient node name (required)"},
				"body":            {Type: "string", Description: "Message body (required)"},
				"idempotency-key": {Type: "string", Description: "Idempotency token for deduplication"},
				"json":            {Type: "boolean", Description: "Output JSON: {\"sent\": \"filename.md\"}"},
			},
			Required: []string{"to", "body"},
		})
	case "pop":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "pop options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"peek": {Type: "boolean", Description: "Read without archiving (non-destructive)"},
				"json": {Type: "boolean", Description: "Output JSON: {} (empty inbox) or {\"id\",\"from\",\"to\",\"body\",\"timestamp\"} (message present); test id field to distinguish"},
			},
		})
	case "read":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "read options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"json":          {Type: "boolean", Description: "Output JSON: {\"files\":[...]} or {\"messages\":[...]}"},
				"archived":      {Type: "boolean", Description: "List archived messages in read/ (self-filter: calling node only)"},
				"dead-letters":  {Type: "boolean", Description: "List dead-letter messages (metadata only, filenames hidden)"},
				"resend-oldest": {Type: "boolean", Description: "Resend the oldest dead-letter (requires --dead-letters)"},
			},
		})
	case "get-context-id":
		// session, config are excluded from --params
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "get-context-id options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"json": {Type: "boolean", Description: "Output JSON: {\"context_id\": \"...\"}"},
			},
		})
	case "get-health-oneline":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "get-health-oneline options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"json": {Type: "boolean", Description: "Output JSON: {\"status\": \"[0]🟣 [1]🟢\"}"},
			},
		})
	case "get-health":
		// Always-JSON command; no --json flag. Schema has no json property.
		type arrayItems struct {
			Type       string                    `json:"type"`
			Properties map[string]schemaProperty `json:"properties"`
		}
		type schemaPropertyWithItems struct {
			Type        string      `json:"type"`
			Description string      `json:"description"`
			Items       *arrayItems `json:"items,omitempty"`
		}
		type healthSchema struct {
			Schema     string                             `json:"$schema"`
			Title      string                             `json:"title"`
			Type       string                             `json:"type"`
			Properties map[string]schemaPropertyWithItems `json:"properties"`
		}
		return enc.Encode(healthSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "get-health output",
			Type:   "object",
			Properties: map[string]schemaPropertyWithItems{
				"context_id":    {Type: "string", Description: "Active context ID for the current tmux session"},
				"session_name":  {Type: "string", Description: "tmux session name used for the health snapshot"},
				"node_count":    {Type: "integer", Description: "Number of known nodes"},
				"visible_state": {Type: "string", Description: "Worst visible state across the session"},
				"compact":       {Type: "string", Description: "Canonical compact token consumed by get-health-oneline"},
				"nodes": {
					Type:        "array",
					Description: "Per-node health and visible-state facts",
					Items: &arrayItems{
						Type: "object",
						Properties: map[string]schemaProperty{
							"name":            {Type: "string", Description: "Node name"},
							"pane_id":         {Type: "string", Description: "Live tmux pane ID for the node"},
							"pane_state":      {Type: "string", Description: "Base pane state before waiting or unread overlays"},
							"waiting_state":   {Type: "string", Description: "Reply-tracked waiting overlay state for the node"},
							"visible_state":   {Type: "string", Description: "Canonical visible state after unread and waiting overlays"},
							"inbox_count":     {Type: "integer", Description: "Unread messages in inbox"},
							"waiting_count":   {Type: "integer", Description: "Waiting-file count for the node"},
							"current_command": {Type: "string", Description: "Current tmux pane command for the node"},
						},
					},
				},
				"windows": {
					Type:        "array",
					Description: "tmux window topology for pure renderer views",
					Items: &arrayItems{
						Type: "object",
						Properties: map[string]schemaProperty{
							"index": {Type: "string", Description: "tmux window index"},
							"nodes": {Type: "array", Description: "Ordered node list for the window"},
						},
					},
				},
			},
		})
	default:
		return fmt.Errorf("unknown command %q; run 'tmux-a2a-postman schema' for config schema", target)
	}
}
