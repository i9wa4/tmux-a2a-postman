package config

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

//go:embed postman.default.toml
var defaultConfigBytes []byte

// Config holds postman configuration loaded from TOML file.
// Python format: [postman] section contains all settings.
type Config struct {
	// Version
	A2AVersion string `toml:"a2a_version"`

	// Timing settings
	ScanInterval     float64 `toml:"scan_interval_seconds"`
	EnterDelay       float64 `toml:"enter_delay_seconds"`
	TmuxTimeout      float64 `toml:"tmux_timeout_seconds"`
	StartupDelay     float64 `toml:"startup_delay_seconds"`
	NewNodePingDelay float64 `toml:"new_node_ping_delay_seconds"`
	ReminderInterval float64 `toml:"reminder_interval_seconds"`

	// TUI settings (Issue #37)
	EdgeActivitySeconds float64 `toml:"edge_activity_seconds"`

	// Node state thresholds (Issue #xxx)
	NodeActiveSeconds float64 `toml:"node_active_seconds"` // 0-N seconds: active (green)
	NodeIdleSeconds   float64 `toml:"node_idle_seconds"`   // N+ seconds: idle (orange) or stale (red)
	NodeStaleSeconds  float64 `toml:"node_stale_seconds"`  // Memory cleanup threshold for pane capture

	// Pane capture settings (hybrid idle detection)
	PaneCaptureEnabled         bool    `toml:"pane_capture_enabled"`
	PaneCaptureIntervalSeconds float64 `toml:"pane_capture_interval_seconds"`
	PaneCaptureMaxPanes        int     `toml:"pane_capture_max_panes"`

	// Paths
	BaseDir string `toml:"base_dir"`

	// Message templates
	NotificationTemplate         string `toml:"notification_template"`
	PingTemplate                 string `toml:"ping_template"`
	DraftTemplate                string `toml:"draft_template"`
	ReminderMessage              string `toml:"reminder_message"`
	CommonTemplate               string `toml:"common_template"`                 // Issue #49: Shared template for all nodes
	EdgeViolationWarningTemplate string `toml:"edge_violation_warning_template"` // Issue #80: Warning message for routing denied
	EdgeViolationWarningMode     string `toml:"edge_violation_warning_mode"`     // Issue #92: "compact" or "verbose" (default: compact)
	IdleReminderHeaderTemplate   string `toml:"idle_reminder_header_template"`   // Issue #82: Idle reminder header
	SessionIdleAlertTemplate     string `toml:"session_idle_alert_template"`     // Issue #82: Session idle alert message
	CompactionHeaderTemplate     string `toml:"compaction_header_template"`      // Issue #82: Compaction detection header
	WatchdogAlertTemplate        string `toml:"watchdog_alert_template"`         // Issue #82: Watchdog idle alert message
	CompactionBodyTemplate       string `toml:"compaction_body_template"`        // Issue #82: Compaction notification body
	DroppedBallEventTemplate     string `toml:"dropped_ball_event_template"`     // Issue #82: Dropped ball event message
	RulesTemplate                string `toml:"rules_template"`                  // Issue #75: Shared protocol rules

	// Global settings
	Edges                []string `toml:"edges"`
	ReplyCommand         string   `toml:"reply_command"`
	UINode               string   `toml:"ui_node"`                 // Issue #46: Generalized target node name
	PingMode             string   `toml:"ping_mode"`               // Issue #98: PING mode ("all", "ui_node_only", "disabled")
	InboxUnreadThreshold int      `toml:"inbox_unread_threshold"` // Inbox unread count threshold for summary notification (default: 3, 0 = disabled)

	// Node-specific configurations (loaded from [nodename] sections)
	Nodes map[string]NodeConfig

	// Compaction detection
	CompactionDetection CompactionDetectionConfig

	// Watchdog
	Watchdog WatchdogConfig
}

// NodeConfig holds per-node configuration.
type NodeConfig struct {
	Template                    string  `toml:"template"`
	OnJoin                      string  `toml:"on_join"`
	Role                        string  `toml:"role"`
	ReminderInterval            float64 `toml:"reminder_interval_seconds"`
	ReminderMessage             string  `toml:"reminder_message"`
	IdleTimeoutSeconds          float64 `toml:"idle_timeout_seconds"`
	IdleReminderMessage         string  `toml:"idle_reminder_message"`
	IdleReminderCooldownSeconds float64 `toml:"idle_reminder_cooldown_seconds"`
	DroppedBallTimeoutSeconds   int     `toml:"dropped_ball_timeout_seconds"`  // Issue #56: 0 = disabled (default)
	DroppedBallCooldownSeconds  int     `toml:"dropped_ball_cooldown_seconds"` // Issue #56: default: same as timeout
	DroppedBallNotification     string  `toml:"dropped_ball_notification"`     // Issue #56: "tui" (default) / "display" / "all"
}

// AgentCard holds agent card information.
type AgentCard struct {
	ID          string   `toml:"id"`
	Name        string   `toml:"name"`
	Constraints string   `toml:"constraints"`
	TalksTo     []string `toml:"talks_to"`
	Template    string   `toml:"template"`
	Role        string   `toml:"role"`
}

// CompactionDetectionConfig holds compaction detection configuration.
type CompactionDetectionConfig struct {
	Enabled         bool                      `toml:"enabled"`
	Pattern         string                    `toml:"pattern"`
	DelaySeconds    float64                   `toml:"delay_seconds"`
	MessageTemplate CompactionMessageTemplate `toml:"message_template"`
}

// CompactionMessageTemplate holds message template for compaction notifications.
type CompactionMessageTemplate struct {
	Type string `toml:"type"`
	Body string `toml:"body"`
}

// WatchdogConfig holds watchdog configuration.
type WatchdogConfig struct {
	Enabled                  bool                  `toml:"enabled"`
	IdleThresholdSeconds     float64               `toml:"idle_threshold_seconds"`
	CooldownSeconds          float64               `toml:"cooldown_seconds"`
	HeartbeatIntervalSeconds float64               `toml:"heartbeat_interval_seconds"`
	Capture                  WatchdogCaptureConfig `toml:"capture"`
}

// WatchdogCaptureConfig holds watchdog capture configuration.
type WatchdogCaptureConfig struct {
	Enabled   bool `toml:"enabled"`
	MaxFiles  int  `toml:"max_files"`
	MaxBytes  int  `toml:"max_bytes"`
	TailLines int  `toml:"tail_lines"`
}

// DefaultConfig returns a Config with sane default values.
func DefaultConfig() *Config {
	return &Config{
		ScanInterval:                 1.0,
		EnterDelay:                   0.5,
		TmuxTimeout:                  5.0,
		StartupDelay:                 2.0,
		NewNodePingDelay:             3.0,
		ReminderInterval:             0.0,
		EdgeActivitySeconds:          300.0, // Issue #37: Default 300 seconds (5 min, matches active state duration)
		NodeActiveSeconds:            300.0, // 0-5min: active (green)
		NodeIdleSeconds:              900.0, // 5-15min: idle (orange)
		NodeStaleSeconds:             900.0, // 15min+: stale (red)
		PaneCaptureEnabled:           true,
		PaneCaptureIntervalSeconds:   60.0,
		PaneCaptureMaxPanes:          10,
		BaseDir:                      "",
		NotificationTemplate:         "Message from {sender}",
		PingTemplate:                 "PING from postman",
		DraftTemplate:                "",
		ReminderMessage:              "",
		ReplyCommand:                 "",
		UINode:                       "concierge", // Issue #46: Default UI target node
		PingMode:                     "all",       // Issue #98: Default to ping all nodes
		InboxUnreadThreshold:         3,           // Default threshold for inbox unread summary notification
		Edges:                        []string{},
		Nodes:                        make(map[string]NodeConfig),
		EdgeViolationWarningTemplate: "you can't talk to \"{attempted_recipient}\". Can talk to: {allowed_edges}. Your message has been moved to dead-letter/.",
		EdgeViolationWarningMode:     "compact", // Issue #92: Default to compact mode
		IdleReminderHeaderTemplate:   "## Idle Reminder",
		SessionIdleAlertTemplate:     "## Idle Alert\n\ntmux session `{session_name}` の全ノードが停止しています。\n\nIdle nodes: {idle_nodes}\n\n{talks_to_line}\n\nReply: `tmux-a2a-postman create-draft --to <node>`",
		CompactionHeaderTemplate:     "## Compaction Detected",
		WatchdogAlertTemplate:        "## Idle Alert\n\nPane {pane_id} has been idle for {idle_duration}.\n\nLast activity: {last_activity}",
		CompactionBodyTemplate:       "Compaction detected for node {node}. Please send status update.",
		DroppedBallEventTemplate:     "Dropped ball: {node} (holding for {duration})",
		RulesTemplate:                "",
	}
}

// loadEmbeddedConfig loads configuration from embedded default_postman.toml.
// Issue #81: Use go:embed to provide default configuration.
func loadEmbeddedConfig() (*Config, error) {
	// Parse embedded TOML data
	var rootSections map[string]toml.Primitive
	md, err := toml.Decode(string(defaultConfigBytes), &rootSections)
	if err != nil {
		return nil, fmt.Errorf("parsing embedded config: %w", err)
	}

	// Decode [postman] section (optional, uses defaults if not present)
	cfg := DefaultConfig()
	postmanPrim, ok := rootSections["postman"]
	if ok {
		if err := md.PrimitiveDecode(postmanPrim, cfg); err != nil {
			return nil, fmt.Errorf("decoding embedded [postman] section: %w", err)
		}
	}

	// Decode [nodename] sections (everything except postman, compaction_detection, and watchdog)
	cfg.Nodes = make(map[string]NodeConfig)
	for name, prim := range rootSections {
		if name == "postman" || name == "compaction_detection" || name == "watchdog" {
			continue
		}

		var node NodeConfig
		if err := md.PrimitiveDecode(prim, &node); err != nil {
			return nil, fmt.Errorf("decoding embedded [%s] section: %w", name, err)
		}
		cfg.Nodes[name] = node
	}

	// Decode [compaction_detection] section if exists
	if compactionPrim, ok := rootSections["compaction_detection"]; ok {
		if err := md.PrimitiveDecode(compactionPrim, &cfg.CompactionDetection); err != nil {
			return nil, fmt.Errorf("decoding embedded [compaction_detection] section: %w", err)
		}
	}

	// Decode [watchdog] section if exists
	if watchdogPrim, ok := rootSections["watchdog"]; ok {
		if err := md.PrimitiveDecode(watchdogPrim, &cfg.Watchdog); err != nil {
			return nil, fmt.Errorf("decoding embedded [watchdog] section: %w", err)
		}
	}

	// Issue #37: Validate EdgeActivitySeconds (1-3600 seconds)
	if cfg.EdgeActivitySeconds <= 0 {
		cfg.EdgeActivitySeconds = 1 // Force minimum
	}
	if cfg.EdgeActivitySeconds > 3600 {
		cfg.EdgeActivitySeconds = 3600 // Force maximum
	}

	return cfg, nil
}

// LoadConfig loads configuration from a TOML file (Python format).
// Python format requires [postman] section and [nodename] sections.
// If path is empty, tries XDG config fallback chain.
// Issue #81: If no file found, loads embedded default configuration.
func LoadConfig(path string) (*Config, error) {
	configPath := path
	if configPath == "" {
		configPath = ResolveConfigPath()
		if configPath == "" {
			// No user config: use embedded default
			return loadEmbeddedConfig()
		}
	}

	// Check if file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if path != "" {
			// Explicit path was provided but doesn't exist
			return nil, fmt.Errorf("config file not found: %s", configPath)
		}
		// Fallback path doesn't exist, use embedded default
		return loadEmbeddedConfig()
	}

	// Parse TOML file with metadata (Python format)
	var rootSections map[string]toml.Primitive
	md, err := toml.DecodeFile(configPath, &rootSections)
	if err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	// Decode [postman] section (optional, uses defaults if not present)
	cfg := DefaultConfig()
	postmanPrim, ok := rootSections["postman"]
	if ok {
		if err := md.PrimitiveDecode(postmanPrim, cfg); err != nil {
			return nil, fmt.Errorf("decoding [postman] section: %w", err)
		}
	}

	// Decode [nodename] sections (everything except postman, compaction_detection, and watchdog)
	cfg.Nodes = make(map[string]NodeConfig)
	for name, prim := range rootSections {
		if name == "postman" || name == "compaction_detection" || name == "watchdog" {
			continue
		}

		var node NodeConfig
		if err := md.PrimitiveDecode(prim, &node); err != nil {
			return nil, fmt.Errorf("decoding [%s] section: %w", name, err)
		}
		cfg.Nodes[name] = node
	}

	// Decode [compaction_detection] section if exists
	if compactionPrim, ok := rootSections["compaction_detection"]; ok {
		if err := md.PrimitiveDecode(compactionPrim, &cfg.CompactionDetection); err != nil {
			return nil, fmt.Errorf("decoding [compaction_detection] section: %w", err)
		}
	}

	// Decode [watchdog] section if exists
	if watchdogPrim, ok := rootSections["watchdog"]; ok {
		if err := md.PrimitiveDecode(watchdogPrim, &cfg.Watchdog); err != nil {
			return nil, fmt.Errorf("decoding [watchdog] section: %w", err)
		}
	}

	// Issue #50: Load node files from nodes/ directory
	configDir := filepath.Dir(configPath)
	nodesDir := filepath.Join(configDir, "nodes")
	if info, err := os.Stat(nodesDir); err == nil && info.IsDir() {
		nodeFiles, _ := filepath.Glob(filepath.Join(nodesDir, "*.toml"))
		sort.Strings(nodeFiles) // deterministic alphabetical order
		for _, nodeFile := range nodeFiles {
			var sections map[string]toml.Primitive
			md2, err := toml.DecodeFile(nodeFile, &sections)
			if err != nil {
				log.Printf("warning: skipping %s: %v", nodeFile, err)
				continue
			}
			for name, prim := range sections {
				if name == "postman" || name == "compaction_detection" || name == "watchdog" {
					continue // skip reserved sections
				}
				var node NodeConfig
				if err := md2.PrimitiveDecode(prim, &node); err != nil {
					log.Printf("warning: skipping [%s] in %s: %v", name, nodeFile, err)
					continue
				}
				cfg.Nodes[name] = node // override if exists in postman.toml
			}
		}
	}

	// Issue #37: Validate EdgeActivitySeconds (1-3600 seconds)
	if cfg.EdgeActivitySeconds <= 0 {
		cfg.EdgeActivitySeconds = 1 // Force minimum
	}
	if cfg.EdgeActivitySeconds > 3600 {
		cfg.EdgeActivitySeconds = 3600 // Force maximum
	}

	// Issue #70: Validate configuration
	validationErrors := ValidateConfig(cfg)
	var errors []string
	for _, ve := range validationErrors {
		if ve.Severity == "error" {
			errors = append(errors, ve.Error())
		} else {
			log.Printf("config warning: %s\n", ve.Error())
		}
	}
	if len(errors) > 0 {
		return nil, fmt.Errorf("config validation failed:\n%s", strings.Join(errors, "\n"))
	}

	return cfg, nil
}

// ResolveConfigPath returns the first existing config file in the fallback chain.
// Returns empty string if no config file is found.
func ResolveConfigPath() string {
	// Try XDG_CONFIG_HOME/tmux-a2a-postman/postman.toml
	if xdgConfigHome := os.Getenv("XDG_CONFIG_HOME"); xdgConfigHome != "" {
		path := filepath.Join(xdgConfigHome, "tmux-a2a-postman", "postman.toml")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// Try ~/.config/tmux-a2a-postman/postman.toml
	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".config", "tmux-a2a-postman", "postman.toml")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	return ""
}

// ParseEdges parses edge definitions into an adjacency map.
// Edge format: "A -- B -- C" or "A --> B --> C" (both create bidirectional edges A↔B, B↔C).
// Both "--" (Go format) and "-->" (Python format) are supported.
// Returns error for invalid formats.
func ParseEdges(edges []string) (map[string][]string, error) {
	result := make(map[string][]string)

	for _, edge := range edges {
		edge = strings.TrimSpace(edge)
		if edge == "" {
			continue
		}

		// Determine separator: try "-->" first (Python format), then "--" (Go format)
		var parts []string
		switch {
		case strings.Contains(edge, "-->"):
			parts = strings.Split(edge, "-->")
		case strings.Contains(edge, "--"):
			parts = strings.Split(edge, "--")
		default:
			return nil, fmt.Errorf("invalid edge format (missing '--' or '-->'): %q", edge)
		}

		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid edge format (need at least 2 nodes): %q", edge)
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

// ResolveBaseDir returns the base directory for postman sessions.
// Priority:
// 1. POSTMAN_HOME env var (explicit override)
// 2. configBaseDir (if non-empty, from config file)
// 3. XDG_STATE_HOME/tmux-a2a-postman/ (or ~/.local/state/tmux-a2a-postman/)
func ResolveBaseDir(configBaseDir string) string {
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
	return filepath.Join(stateHome, "tmux-a2a-postman")
}

// CreateSessionDirs creates the session directory structure.
// Legacy signature for backward compatibility with tests.
// Creates: sessionDir/{inbox,post,draft,read,dead-letter,capture}
func CreateSessionDirs(sessionDir string) error {
	dirs := []string{
		filepath.Join(sessionDir, "inbox"),
		filepath.Join(sessionDir, "post"),
		filepath.Join(sessionDir, "draft"),
		filepath.Join(sessionDir, "read"),
		filepath.Join(sessionDir, "dead-letter"),
		filepath.Join(sessionDir, "capture"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// CreateMultiSessionDirs creates the multi-session directory structure.
// For multi-session support: contextDir = baseDir/contextID, sessionName = tmux session name
// Creates: contextDir/sessionName/{inbox,post,draft,read,dead-letter}
func CreateMultiSessionDirs(contextDir, sessionName string) error {
	sessionDir := filepath.Join(contextDir, sessionName)
	return CreateSessionDirs(sessionDir)
}

// ResolveNodesDir returns the nodes directory path for the given config file path.
// Returns empty string if nodes directory doesn't exist.
func ResolveNodesDir(configPath string) string {
	if configPath == "" {
		return ""
	}
	nodesDir := filepath.Join(filepath.Dir(configPath), "nodes")
	if info, err := os.Stat(nodesDir); err == nil && info.IsDir() {
		return nodesDir
	}
	return ""
}

// resolveContextID resolves the context ID with fallback chain.
// Priority:
// ResolveContextID resolves the context ID using the fallback chain:
// 1. explicitID (from --context-id flag)
// 2. A2A_CONTEXT_ID env var
// 3. .postman/current-context-{tmux_session} file
// Returns (contextID, source, error).
func ResolveContextID(explicitID string, baseDir string) (string, string, error) {
	// 1. Explicit --context-id flag
	if explicitID != "" {
		return explicitID, "flag", nil
	}

	// 2. A2A_CONTEXT_ID env var
	if envID := os.Getenv("A2A_CONTEXT_ID"); envID != "" {
		return envID, "env:A2A_CONTEXT_ID", nil
	}

	// 3. current-context file based on tmux session
	tmuxSession := GetTmuxSessionName()
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

// GetTmuxSessionName extracts the tmux session name using tmux command.
// Returns empty string if not in tmux.
func GetTmuxSessionName() string {
	cmd := exec.Command("tmux", "display-message", "-p", "#{session_name}")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}
