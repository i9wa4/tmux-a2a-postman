package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

// runSchema prints JSON Schema for the postman config or a specific command's options.
//
//	tmux-a2a-postman schema              # postman.toml config schema
//	tmux-a2a-postman schema send-message # send-message options schema
func runSchema(args []string) error {
	enc := json.NewEncoder(os.Stdout)
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
		return json.NewEncoder(os.Stdout).Encode(struct {
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
			Title:  "postman.toml config",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"scan_interval_seconds":      {Type: "integer", Description: "Inbox scan interval in seconds"},
				"enter_delay_seconds":        {Type: "number", Description: "Delay before entering a pane in seconds"},
				"tmux_timeout_seconds":       {Type: "integer", Description: "Timeout for tmux commands in seconds"},
				"startup_delay_seconds":      {Type: "integer", Description: "Delay before starting the daemon in seconds"},
				"reminder_interval_messages": {Type: "integer", Description: "Send reminder after N messages (0 = disabled)"},
				"edges":                      {Type: "array", Description: "List of 'node-a,node-b' routing edges"},
				"ui_node":                    {Type: "string", Description: "Node name for the postman TUI pane"},
				"base_dir":                   {Type: "string", Description: "Override state base directory (also: POSTMAN_HOME)"},
			},
		})
	// Properties = --params scope only (excluded flags omitted; see alwaysExcludedParams)
	// SYNC: options struct fields; alwaysExcludedParams + perCommandExcludedParams maps
	case "send-message":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "send-message options",
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
	case "get-session-status-oneline":
		return enc.Encode(jsonSchema{
			Schema: "https://json-schema.org/draft/2020-12/schema",
			Title:  "get-session-status-oneline options",
			Type:   "object",
			Properties: map[string]schemaProperty{
				"json": {Type: "boolean", Description: "Output JSON: {\"status\": \"[1]●●●●\"}"},
			},
		})
	case "get-session-health":
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
			Title:  "get-session-health output",
			Type:   "object",
			Properties: map[string]schemaPropertyWithItems{
				"context_id": {Type: "string", Description: "Active context ID for the current tmux session"},
				"node_count": {Type: "integer", Description: "Number of known nodes"},
				"nodes": {
					Type:        "array",
					Description: "Per-node health status",
					Items: &arrayItems{
						Type: "object",
						Properties: map[string]schemaProperty{
							"name":          {Type: "string", Description: "Node name"},
							"inbox_count":   {Type: "integer", Description: "Unread messages in inbox"},
							"waiting_count": {Type: "integer", Description: "Messages waiting to be delivered"},
						},
					},
				},
			},
		})
	default:
		return fmt.Errorf("unknown command %q; run 'tmux-a2a-postman schema' for config schema", target)
	}
}
