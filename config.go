package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds postman configuration loaded from TOML file.
type Config struct {
	// Timing settings
	ScanInterval       float64 `toml:"scan_interval_seconds"`
	EnterDelay         float64 `toml:"enter_delay_seconds"`
	TmuxTimeout        float64 `toml:"tmux_timeout_seconds"`
	StartupDelay       float64 `toml:"startup_delay_seconds"`
	NewNodePingDelay   float64 `toml:"new_node_ping_delay_seconds"`
	ReminderInterval   float64 `toml:"reminder_interval_seconds"`

	// Paths
	BaseDir string `toml:"base_dir"`

	// Message templates
	NotificationTemplate string `toml:"notification_template"`
	PingTemplate         string `toml:"ping_template"`
	DigestTemplate       string `toml:"digest_template"`
	DraftTemplate        string `toml:"draft_template"`
	ReminderMessage      string `toml:"reminder_message"`

	// Global settings
	Edges        []string `toml:"edges"`
	ReplyCommand string   `toml:"reply_command"`

	// Node-specific configurations
	Nodes map[string]NodeConfig `toml:"node"`
}

// NodeConfig holds per-node configuration.
type NodeConfig struct {
	Template         string   `toml:"template"`
	OnJoin           string   `toml:"on_join"`
	Observes         []string `toml:"observes"`
	Role             string   `toml:"role"`
	SubscribeDigest  bool     `toml:"subscribe_digest"`
	ReminderInterval float64  `toml:"reminder_interval_seconds"`
	ReminderMessage  string   `toml:"reminder_message"`
}

// AgentCard holds agent card information.
type AgentCard struct {
	ID              string   `toml:"id"`
	Name            string   `toml:"name"`
	Constraints     string   `toml:"constraints"`
	TalksTo         []string `toml:"talks_to"`
	Template        string   `toml:"template"`
	Role            string   `toml:"role"`
	SubscribeDigest bool     `toml:"subscribe_digest"`
}

// DefaultConfig returns a Config with sane default values.
func DefaultConfig() *Config {
	return &Config{
		ScanInterval:         1.0,
		EnterDelay:           0.5,
		TmuxTimeout:          5.0,
		StartupDelay:         2.0,
		NewNodePingDelay:     3.0,
		ReminderInterval:     0.0,
		BaseDir:              "",
		NotificationTemplate: "Message from {sender}",
		PingTemplate:         "PING from postman",
		DigestTemplate:       "",
		DraftTemplate:        "",
		ReminderMessage:      "",
		ReplyCommand:         "",
		Edges:                []string{},
		Nodes:                make(map[string]NodeConfig),
	}
}

// LoadConfig loads configuration from a TOML file.
// If path is empty, tries XDG config fallback chain.
// If no file found, returns DefaultConfig().
func LoadConfig(path string) (*Config, error) {
	configPath := path
	if configPath == "" {
		configPath = resolveConfigPath()
		if configPath == "" {
			// No config file found, use defaults
			return DefaultConfig(), nil
		}
	}

	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if path != "" {
			// Explicit path was provided but doesn't exist
			return nil, fmt.Errorf("config file not found: %s", configPath)
		}
		// Fallback path doesn't exist, use defaults
		return DefaultConfig(), nil
	}

	// Parse TOML file
	cfg := DefaultConfig()
	if _, err := toml.DecodeFile(configPath, cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	return cfg, nil
}

// resolveConfigPath returns the first existing config file in the fallback chain.
// Returns empty string if no config file is found.
func resolveConfigPath() string {
	// Try XDG_CONFIG_HOME/postman/config.toml
	if xdgConfigHome := os.Getenv("XDG_CONFIG_HOME"); xdgConfigHome != "" {
		path := filepath.Join(xdgConfigHome, "postman", "config.toml")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// Try ~/.config/postman/config.toml
	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".config", "postman", "config.toml")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return ""
}

// ParseEdges parses edge definitions into an adjacency map.
// Edge format: "A -- B -- C" (chain syntax, creates bidirectional edges A↔B, B↔C).
// Returns error for invalid formats.
func ParseEdges(edges []string) (map[string][]string, error) {
	result := make(map[string][]string)

	for _, edge := range edges {
		edge = strings.TrimSpace(edge)
		if edge == "" {
			continue
		}

		// Split by "--" separator
		parts := strings.Split(edge, "--")
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid edge format (missing '--'): %q", edge)
		}

		// Trim all parts
		nodes := make([]string, 0, len(parts))
		for _, p := range parts {
			node := strings.TrimSpace(p)
			if node == "" {
				return nil, fmt.Errorf("invalid edge format (empty node): %q", edge)
			}
			nodes = append(nodes, node)
		}

		// Create bidirectional edges between adjacent nodes
		for i := 0; i < len(nodes)-1; i++ {
			from := nodes[i]
			to := nodes[i+1]
			result[from] = append(result[from], to)
			result[to] = append(result[to], from)
		}
	}

	return result, nil
}

// GetTalksTo returns the list of nodes that the specified node can communicate with.
// Returns nodes that have an edge to the specified node in the adjacency map.
func GetTalksTo(adjacency map[string][]string, nodeName string) []string {
	if neighbors, ok := adjacency[nodeName]; ok {
		// Return a copy to avoid external modification
		result := make([]string, len(neighbors))
		copy(result, neighbors)
		return result
	}
	return []string{}
}

// resolveBaseDir returns the base directory for postman sessions.
// Priority:
// 1. POSTMAN_HOME env var (explicit override)
// 2. configBaseDir (if non-empty, from config file)
// 3. XDG_STATE_HOME/postman/ (or ~/.local/state/postman/)
func resolveBaseDir(configBaseDir string) string {
	// 1. Explicit override
	if v := os.Getenv("POSTMAN_HOME"); v != "" {
		return v
	}
	// 2. Config file base_dir
	if configBaseDir != "" {
		return configBaseDir
	}
	// 3. XDG_STATE_HOME (enforced)
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			stateHome = filepath.Join(home, ".local", "state")
		}
	}
	return filepath.Join(stateHome, "postman")
}

// createSessionDirs creates the session directory structure.
func createSessionDirs(sessionDir string) error {
	dirs := []string{
		filepath.Join(sessionDir, "inbox"),
		filepath.Join(sessionDir, "post"),
		filepath.Join(sessionDir, "draft"),
		filepath.Join(sessionDir, "read"),
		filepath.Join(sessionDir, "dead-letter"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// SaveConfig saves the configuration to a TOML file.
// NOTE: This will remove comments from the TOML file.
func SaveConfig(path string, cfg *Config) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating config file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	return nil
}

// resolveContextID resolves the context ID with fallback chain.
// Priority:
// 1. explicitID (from --context-id flag)
// 2. A2A_CONTEXT_ID env var
// 3. .postman/current-context-{tmux_session} file
// Returns (contextID, source, error).
func resolveContextID(explicitID string, baseDir string) (string, string, error) {
	// 1. Explicit --context-id flag
	if explicitID != "" {
		return explicitID, "flag", nil
	}

	// 2. A2A_CONTEXT_ID env var
	if envID := os.Getenv("A2A_CONTEXT_ID"); envID != "" {
		return envID, "env:A2A_CONTEXT_ID", nil
	}

	// 3. current-context file based on tmux session
	tmuxSession := getTmuxSessionName()
	if tmuxSession != "" {
		contextFile := filepath.Join(baseDir, fmt.Sprintf("current-context-%s", tmuxSession))
		if data, err := os.ReadFile(contextFile); err == nil {
			contextID := strings.TrimSpace(string(data))
			if contextID != "" {
				return contextID, fmt.Sprintf("file:%s", contextFile), nil
			}
		}
	}

	return "", "", fmt.Errorf("no context ID found (tried: flag, A2A_CONTEXT_ID env, current-context file)")
}

// getTmuxSessionName extracts the tmux session name using tmux command.
// Returns empty string if not in tmux.
func getTmuxSessionName() string {
	cmd := exec.Command("tmux", "display-message", "-p", "#{session_name}")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
