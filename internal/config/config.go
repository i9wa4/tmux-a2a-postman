package config

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"
	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
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
	ReminderInterval float64 `toml:"reminder_interval_messages"`
	EnterVerifyDelay float64 `toml:"enter_verify_delay_seconds"` // Delay for post-Enter capture comparison (0 = disabled)
	EnterRetryMax    int     `toml:"enter_retry_max"`            // Max C-m retries on pane capture unchanged (0 = disabled)

	// TUI settings (Issue #37)
	EdgeActivitySeconds float64 `toml:"edge_activity_seconds"`

	// Node state thresholds (Issue #xxx)
	NodeActiveSeconds         float64 `toml:"node_active_seconds"`          // 0-N seconds: active (green)
	NodeIdleSeconds           float64 `toml:"node_idle_seconds"`            // N+ seconds: idle (orange) or stale (red)
	NodeStaleSeconds          float64 `toml:"node_stale_seconds"`           // Memory cleanup threshold for pane capture
	NodeSpinningSeconds       float64 `toml:"node_spinning_seconds"`        // Hard ceiling for active-but-no-reply detection; 0 = disabled
	MessageAgeWarningSeconds  float64 `toml:"message_age_warning_seconds"`  // Delivery latency warning threshold; 0 = disabled
	MessageTTLSeconds         float64 `toml:"message_ttl_seconds"`          // Stale post/ drain TTL; 0 = disabled
	RetentionPeriodDays       int     `toml:"retention_period_days"`        // Inactive runtime cleanup threshold in days; 0 = disabled
	MinDeliveryGapSeconds     float64 `toml:"min_delivery_gap_seconds"`     // Duplicate delivery rate limit; 0 = disabled
	StartupDrainWindowSeconds float64 `toml:"startup_drain_window_seconds"` // Session-enabled bypass window after daemon start; 0 = disabled (#217)

	// Pane capture settings (hybrid idle detection)
	PaneCaptureEnabled         *bool   `toml:"pane_capture_enabled"` // nil = use default (true) (#219)
	PaneCaptureIntervalSeconds float64 `toml:"pane_capture_interval_seconds"`
	PaneCaptureMaxPanes        int     `toml:"pane_capture_max_panes"`
	ActivityWindowSeconds      float64 `toml:"activity_window_seconds"`

	// Paths
	BaseDir      string `toml:"base_dir"`
	BindingsPath string `toml:"bindings_path"` // #306: path to bindings.toml; empty = phony dispatch disabled

	// Message templates
	NotificationTemplate            string `toml:"notification_template"`
	DaemonMessageTemplate           string `toml:"daemon_message_template"` // Unified envelope for ping, alert, heartbeat
	DraftTemplate                   string `toml:"draft_template"`
	ReminderMessage                 string `toml:"reminder_message"`
	CommonTemplate                  string `toml:"common_template"`                     // Issue #49: Shared template for all nodes
	EdgeViolationWarningTemplate    string `toml:"edge_violation_warning_template"`     // Issue #80: Warning message for routing denied
	EdgeViolationWarningMode        string `toml:"edge_violation_warning_mode"`         // Issue #92: "compact" or "verbose" (default: compact)
	DroppedBallEventTemplate        string `toml:"dropped_ball_event_template"`         // Issue #82: Dropped ball event message
	AlertActionReachableTemplate    string `toml:"alert_action_reachable_template"`     // Action text when ui_node can reach the target node
	AlertActionUnreachableTemplate  string `toml:"alert_action_unreachable_template"`   // Action text when ui_node cannot reach the target node
	InboxStagnationAlertTemplate    string `toml:"inbox_stagnation_alert_template"`     // Alert message body for inbox stagnation
	InboxUnreadSummaryAlertTemplate string `toml:"inbox_unread_summary_alert_template"` // Alert message body for inbox unread summary
	NodeInactivityAlertTemplate     string `toml:"node_inactivity_alert_template"`      // Alert message body for node inactivity
	UnrepliedMessageAlertTemplate   string `toml:"unreplied_message_alert_template"`    // Alert message body for unreplied messages
	SpinningAlertTemplate           string `toml:"spinning_alert_template"`             // Alert body for spinning detection
	StalledAlertTemplate            string `toml:"stalled_alert_template"`              // Alert body for stalled waiting-state detection
	MessageFooter                   string `toml:"message_footer"`                      // Footer appended to outgoing messages by `send` after message content

	// Global settings
	Edges                              []string `toml:"edges"`
	ReplyCommand                       string   `toml:"reply_command"`
	UINode                             string   `toml:"ui_node"`                               // Issue #46: Generalized target node name
	InboxUnreadThreshold               int      `toml:"inbox_unread_threshold"`                // Inbox unread count threshold for summary notification (default: 3, 0 = disabled)
	AlertCooldownSeconds               int      `toml:"alert_cooldown_seconds"`                // min seconds between any alert/warning to same recipient
	AlertDeliveryWindowSeconds         int      `toml:"alert_delivery_window_seconds"`         // suppress alert if recipient received msg within this window
	PaneNotifyCooldownSeconds          int      `toml:"pane_notify_cooldown_seconds"`          // min seconds between SendToPane calls to the same pane; 0 = use default (600)
	ReadContextMode                    string   `toml:"read_context_mode"`                     // Read-time context rendering mode for bare interactive pop
	ReadContextPieces                  []string `toml:"read_context_pieces"`                   // Ordered built-in pieces rendered for bare interactive pop
	ReadContextHeading                 string   `toml:"read_context_heading"`                  // Heading for the rendered read-time context block
	AutoEnableNewSessions              *bool    `toml:"auto_enable_new_sessions"`              // nil = use default (false) (#219)
	AutoEnableNewAgents                *bool    `toml:"auto_enable_new_agents"`                // nil = use default (true) (#219)
	JournalHealthCutoverEnabled        *bool    `toml:"journal_health_cutover_enabled"`        // nil = use default (false) (#379)
	JournalCompatibilityCutoverEnabled *bool    `toml:"journal_compatibility_cutover_enabled"` // nil = use default (false) (#379)

	// Node-specific configurations (loaded from [nodename] sections)
	Nodes map[string]NodeConfig
	// NodeOrder preserves first-seen node definition order across merged config files.
	NodeOrder []string `toml:"-"`

	// Node-level defaults applied to all nodes (loaded from [node_defaults] section)
	NodeDefaults NodeConfig

	// Heartbeat
	Heartbeat HeartbeatConfig

	// Shell template execution opt-in (#security)
	AllowShellTemplates bool `toml:"allow_shell_templates"`

	directTemplateRootTrust  map[string]bool
	projectLocalExplicitZero localExplicitZeroInventory
	uiNodeSet                bool
}

// NodeConfig holds per-node configuration.
type NodeConfig struct {
	Template                   string  `toml:"template"`
	Role                       string  `toml:"role"`
	ReminderInterval           float64 `toml:"reminder_interval_messages"`
	ReminderMessage            string  `toml:"reminder_message"`
	IdleTimeoutSeconds         float64 `toml:"idle_timeout_seconds"`
	DroppedBallTimeoutSeconds  int     `toml:"dropped_ball_timeout_seconds"`  // Issue #56: 0 = disabled (default)
	DroppedBallCooldownSeconds int     `toml:"dropped_ball_cooldown_seconds"` // Issue #56: default: same as timeout
	DroppedBallNotification    string  `toml:"dropped_ball_notification"`     // Issue #56: "tui" (default) / "display" / "all"
	EnterCount                 int     `toml:"enter_count"`                   // Issue #126: Number of Enter keystrokes to send (0/1 = single, 2+ = double)
	EnterDelay                 float64 `toml:"enter_delay_seconds"`           // 0 = use global default
	DeliveryIdleTimeoutSeconds float64 `toml:"delivery_idle_timeout_seconds"` // Issue #282: 0 = disabled
	DeliveryIdleRetryMax       int     `toml:"delivery_idle_retry_max"`       // Issue #282: max re-delivery attempts (0 = use default 3)
}

type localExplicitZeroInventory struct {
	postman      map[string]bool
	nodeDefaults map[string]bool
	nodes        map[string]map[string]bool
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

// HeartbeatConfig holds configuration for the HEARTBEAT-LLM integration.
type HeartbeatConfig struct {
	Enabled         *bool   `toml:"enabled"` // nil = use default (false) (#219)
	IntervalSeconds float64 `toml:"interval_seconds"`
	LLMNode         string  `toml:"llm_node"`
	Prompt          string  `toml:"prompt"`
}

// boolPtr returns a pointer to a bool value (#219).
func boolPtr(v bool) *bool { return &v }

// BoolVal dereferences a *bool with a default fallback (#219).
func BoolVal(p *bool, defaultVal bool) bool {
	if p == nil {
		return defaultVal
	}
	return *p
}

// DefaultConfig returns a Config with zero values.
// All non-zero defaults are defined in postman.default.toml (SSOT).
// Only structural fields (Nodes map, Edges slice) are initialized here.
func DefaultConfig() *Config {
	return &Config{
		Edges:     []string{},
		Nodes:     make(map[string]NodeConfig),
		NodeOrder: []string{},
	}
}

func appendUniqueNodeNames(order []string, names ...string) []string {
	seen := make(map[string]bool, len(order))
	for _, name := range order {
		seen[name] = true
	}
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		order = append(order, name)
		seen[name] = true
	}
	return order
}

func isReservedNodeSection(name string) bool {
	return name == "postman" || name == "heartbeat" || name == "node_defaults"
}

func orderedTOMLNodeNames(md toml.MetaData) []string {
	var order []string
	for _, key := range md.Keys() {
		if len(key) == 0 {
			continue
		}
		name := key[0]
		if isReservedNodeSection(name) {
			continue
		}
		order = appendUniqueNodeNames(order, name)
	}
	return order
}

func tomlHasField(md toml.MetaData, section, field string) bool {
	for _, key := range md.Keys() {
		if len(key) != 2 {
			continue
		}
		if key[0] == section && key[1] == field {
			return true
		}
	}
	return false
}

func (cfg *Config) recordNodeNames(names ...string) {
	cfg.NodeOrder = appendUniqueNodeNames(cfg.NodeOrder, names...)
}

func (cfg *Config) OrderedNodeNames() []string {
	if cfg == nil {
		return nil
	}

	order := append([]string{}, cfg.NodeOrder...)
	var extras []string
	for name := range cfg.Nodes {
		if !containsString(order, name) {
			extras = append(extras, name)
		}
	}
	sort.Strings(extras)
	return append(order, extras...)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (cfg *Config) HasExplicitUINodeSetting() bool {
	if cfg == nil {
		return false
	}
	return cfg.uiNodeSet
}

// warnDeprecatedKeys logs a warning if deprecated TOML keys are found in rawBytes.
func warnDeprecatedKeys(rawBytes []byte, path string) {
	if bytes.Contains(rawBytes, []byte("reminder_interval_seconds")) {
		log.Printf("config warning: 'reminder_interval_seconds' is deprecated; "+
			"rename to 'reminder_interval_messages' in %s", path)
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

	// Decode [nodename] sections (everything except postman and heartbeat)
	cfg.Nodes = make(map[string]NodeConfig)
	for _, name := range orderedTOMLNodeNames(md) {
		prim := rootSections[name]
		var node NodeConfig
		if err := md.PrimitiveDecode(prim, &node); err != nil {
			return nil, fmt.Errorf("decoding embedded [%s] section: %w", name, err)
		}
		cfg.Nodes[name] = node
		cfg.recordNodeNames(name)
	}

	// Decode [heartbeat] section if exists
	if heartbeatPrim, ok := rootSections["heartbeat"]; ok {
		if err := md.PrimitiveDecode(heartbeatPrim, &cfg.Heartbeat); err != nil {
			return nil, fmt.Errorf("decoding embedded [heartbeat] section: %w", err)
		}
	}

	// Decode [node_defaults] section if exists
	if nodeDefaultsPrim, ok := rootSections["node_defaults"]; ok {
		if err := md.PrimitiveDecode(nodeDefaultsPrim, &cfg.NodeDefaults); err != nil {
			return nil, fmt.Errorf("decoding embedded [node_defaults] section: %w", err)
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

// resolveProjectLocalConfig searches upward from cwd for .tmux-a2a-postman/postman.toml.
// Stops before the home directory. Deduplicates against xdgPath via EvalSymlinks.
// Returns the project-local config path, or "" if not found.
// Issue #121: Project-local config support.
func resolveProjectLocalConfig(cwd, xdgPath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil // cannot determine home; skip project-local
	}
	homeResolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		homeResolved = home
	}
	xdgResolved := ""
	if xdgPath != "" {
		r, err := filepath.EvalSymlinks(xdgPath)
		if err == nil {
			xdgResolved = r
		} else {
			xdgResolved = xdgPath
		}
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, ".tmux-a2a-postman", "postman.toml")
		if _, err := os.Stat(candidate); err == nil {
			candidateResolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				candidateResolved = candidate
			}
			if candidateResolved != xdgResolved {
				return candidate, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil // filesystem root reached
		}
		parentResolved, err := filepath.EvalSymlinks(parent)
		if err != nil {
			parentResolved = parent
		}
		if parentResolved == homeResolved {
			return "", nil // stop before home directory
		}
		dir = parent
	}
}

// resolveXDGMarkdownPath returns the path to postman.md in the XDG config
// directory, or "" if not found. Mirrors ResolveConfigPath() for Markdown.
// Issue #324: Markdown config format support.
func resolveXDGMarkdownPath() string {
	if xdgConfigHome := os.Getenv("XDG_CONFIG_HOME"); xdgConfigHome != "" {
		path := filepath.Join(xdgConfigHome, "tmux-a2a-postman", "postman.md")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".config", "tmux-a2a-postman", "postman.md")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

// resolveProjectLocalMarkdown searches upward from cwd for
// .tmux-a2a-postman/postman.md. Stops before the home directory.
// Deduplicates against xdgMarkdownPath via EvalSymlinks.
// Returns the project-local markdown path, or "" if not found.
// Issue #324: Markdown config format support.
func resolveProjectLocalMarkdown(cwd, xdgMarkdownPath string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil
	}
	homeResolved, err := filepath.EvalSymlinks(home)
	if err != nil {
		homeResolved = home
	}
	xdgResolved := ""
	if xdgMarkdownPath != "" {
		r, err := filepath.EvalSymlinks(xdgMarkdownPath)
		if err == nil {
			xdgResolved = r
		} else {
			xdgResolved = xdgMarkdownPath
		}
	}
	dir := cwd
	for {
		candidate := filepath.Join(dir, ".tmux-a2a-postman", "postman.md")
		if _, err := os.Stat(candidate); err == nil {
			candidateResolved, err := filepath.EvalSymlinks(candidate)
			if err != nil {
				candidateResolved = candidate
			}
			if candidateResolved != xdgResolved {
				return candidate, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		parentResolved, err := filepath.EvalSymlinks(parent)
		if err != nil {
			parentResolved = parent
		}
		if parentResolved == homeResolved {
			return "", nil
		}
		dir = parent
	}
}

// loadConfigFile parses a TOML config file into a zero-value Config.
// Unlike LoadConfig, starts from zero-value (not DefaultConfig) so only
// explicitly-set fields are non-zero. Does not load sibling nodes/ directory.
// Issue #121: Used for project-local config overlay.
func loadConfigFile(path string) (*Config, error) {
	rawBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading project-local config %s: %w", path, err)
	}
	warnDeprecatedKeys(rawBytes, path)
	var rootSections map[string]toml.Primitive
	md, err := toml.Decode(string(rawBytes), &rootSections)
	if err != nil {
		return nil, fmt.Errorf("parsing project-local config %s: %w", path, err)
	}

	cfg := &Config{Nodes: make(map[string]NodeConfig), NodeOrder: []string{}}

	if postmanPrim, ok := rootSections["postman"]; ok {
		if err := md.PrimitiveDecode(postmanPrim, cfg); err != nil {
			return nil, fmt.Errorf("decoding [postman] in %s: %w", path, err)
		}
	}

	for _, name := range orderedTOMLNodeNames(md) {
		prim := rootSections[name]
		var node NodeConfig
		if err := md.PrimitiveDecode(prim, &node); err != nil {
			return nil, fmt.Errorf("decoding [%s] in %s: %w", name, path, err)
		}
		cfg.Nodes[name] = node
		cfg.recordNodeNames(name)
	}

	if heartbeatPrim, ok := rootSections["heartbeat"]; ok {
		if err := md.PrimitiveDecode(heartbeatPrim, &cfg.Heartbeat); err != nil {
			return nil, fmt.Errorf("decoding [heartbeat] in %s: %w", path, err)
		}
	}

	// Decode [node_defaults] section if exists
	if nodeDefaultsPrim, ok := rootSections["node_defaults"]; ok {
		if err := md.PrimitiveDecode(nodeDefaultsPrim, &cfg.NodeDefaults); err != nil {
			return nil, fmt.Errorf("decoding [node_defaults] in %s: %w", path, err)
		}
	}

	cfg.uiNodeSet = tomlHasField(md, "postman", "ui_node")
	markLocalExplicitZeroInventory(cfg, md)

	return cfg, nil
}

func markLocalExplicitZeroInventory(cfg *Config, md toml.MetaData) {
	inventory := localExplicitZeroInventory{
		postman:      make(map[string]bool),
		nodeDefaults: make(map[string]bool),
		nodes:        make(map[string]map[string]bool),
	}
	for _, key := range md.Keys() {
		if len(key) != 2 {
			continue
		}
		section := key[0]
		field := key[1]
		switch section {
		case "postman":
			if localPostmanExplicitZero(cfg, field) {
				inventory.postman[field] = true
			}
		case "node_defaults":
			if localNodeDefaultsExplicitZero(cfg.NodeDefaults, field) {
				inventory.nodeDefaults[field] = true
			}
		case "heartbeat":
			continue
		default:
			node := cfg.Nodes[section]
			if localNodeExplicitZero(node, field) {
				if inventory.nodes[section] == nil {
					inventory.nodes[section] = make(map[string]bool)
				}
				inventory.nodes[section][field] = true
			}
		}
	}
	cfg.projectLocalExplicitZero = inventory
}

func localPostmanExplicitZero(cfg *Config, field string) bool {
	switch field {
	case "reminder_interval_messages":
		return cfg.ReminderInterval == 0
	case "enter_verify_delay_seconds":
		return cfg.EnterVerifyDelay == 0
	case "enter_retry_max":
		return cfg.EnterRetryMax == 0
	case "node_spinning_seconds":
		return cfg.NodeSpinningSeconds == 0
	case "message_age_warning_seconds":
		return cfg.MessageAgeWarningSeconds == 0
	case "message_ttl_seconds":
		return cfg.MessageTTLSeconds == 0
	case "retention_period_days":
		return cfg.RetentionPeriodDays == 0
	case "min_delivery_gap_seconds":
		return cfg.MinDeliveryGapSeconds == 0
	case "startup_drain_window_seconds":
		return cfg.StartupDrainWindowSeconds == 0
	case "pane_capture_max_panes":
		return cfg.PaneCaptureMaxPanes == 0
	case "inbox_unread_threshold":
		return cfg.InboxUnreadThreshold == 0
	case "pane_notify_cooldown_seconds":
		return cfg.PaneNotifyCooldownSeconds == 0
	default:
		return false
	}
}

func localNodeExplicitZero(node NodeConfig, field string) bool {
	switch field {
	case "idle_timeout_seconds":
		return node.IdleTimeoutSeconds == 0
	case "dropped_ball_timeout_seconds":
		return node.DroppedBallTimeoutSeconds == 0
	case "dropped_ball_cooldown_seconds":
		return node.DroppedBallCooldownSeconds == 0
	case "enter_count":
		return node.EnterCount == 0
	case "enter_delay_seconds":
		return node.EnterDelay == 0
	case "delivery_idle_timeout_seconds":
		return node.DeliveryIdleTimeoutSeconds == 0
	case "delivery_idle_retry_max":
		return node.DeliveryIdleRetryMax == 0
	default:
		return false
	}
}

func localNodeDefaultsExplicitZero(node NodeConfig, field string) bool {
	switch field {
	case "enter_count":
		return node.EnterCount == 0
	default:
		return false
	}
}

func applyProjectLocalExplicitZero(base, override *Config) {
	if override == nil {
		return
	}
	for field := range override.projectLocalExplicitZero.postman {
		switch field {
		case "reminder_interval_messages":
			base.ReminderInterval = 0
		case "enter_verify_delay_seconds":
			base.EnterVerifyDelay = 0
		case "enter_retry_max":
			base.EnterRetryMax = 0
		case "node_spinning_seconds":
			base.NodeSpinningSeconds = 0
		case "message_age_warning_seconds":
			base.MessageAgeWarningSeconds = 0
		case "message_ttl_seconds":
			base.MessageTTLSeconds = 0
		case "retention_period_days":
			base.RetentionPeriodDays = 0
		case "min_delivery_gap_seconds":
			base.MinDeliveryGapSeconds = 0
		case "startup_drain_window_seconds":
			base.StartupDrainWindowSeconds = 0
		case "pane_capture_max_panes":
			base.PaneCaptureMaxPanes = 0
		case "inbox_unread_threshold":
			base.InboxUnreadThreshold = 0
		case "pane_notify_cooldown_seconds":
			base.PaneNotifyCooldownSeconds = 0
		}
	}
	for name, fields := range override.projectLocalExplicitZero.nodes {
		node := base.Nodes[name]
		for field := range fields {
			switch field {
			case "idle_timeout_seconds":
				node.IdleTimeoutSeconds = 0
			case "dropped_ball_timeout_seconds":
				node.DroppedBallTimeoutSeconds = 0
			case "dropped_ball_cooldown_seconds":
				node.DroppedBallCooldownSeconds = 0
			case "enter_count":
				node.EnterCount = 0
			case "enter_delay_seconds":
				node.EnterDelay = 0
			case "delivery_idle_timeout_seconds":
				node.DeliveryIdleTimeoutSeconds = 0
			case "delivery_idle_retry_max":
				node.DeliveryIdleRetryMax = 0
			}
		}
		base.Nodes[name] = node
	}
	for field := range override.projectLocalExplicitZero.nodeDefaults {
		switch field {
		case "enter_count":
			base.NodeDefaults.EnterCount = 0
		}
	}
	base.mergeProjectLocalExplicitZeroNodes(override.projectLocalExplicitZero.nodes)
}

func (cfg *Config) mergeProjectLocalExplicitZeroNodes(nodes map[string]map[string]bool) {
	if len(nodes) == 0 {
		return
	}
	if cfg.projectLocalExplicitZero.nodes == nil {
		cfg.projectLocalExplicitZero.nodes = make(map[string]map[string]bool)
	}
	for name, fields := range nodes {
		if cfg.projectLocalExplicitZero.nodes[name] == nil {
			cfg.projectLocalExplicitZero.nodes[name] = make(map[string]bool)
		}
		for field := range fields {
			cfg.projectLocalExplicitZero.nodes[name][field] = true
		}
	}
}

func (cfg *Config) hasProjectLocalExplicitZeroNode(name, field string) bool {
	if cfg == nil {
		return false
	}
	fields := cfg.projectLocalExplicitZero.nodes[name]
	return fields[field]
}

// mergeConfig merges override fields into base using "non-zero wins" semantics.
// Non-zero override values replace base values. Bool fields cannot be overridden to false.
// Edges are replaced when override has at least one entry. Nodes are merged field-by-field.
// Issue #121: Used to apply project-local config on top of XDG/embedded config.
func mergeConfig(base, override *Config) {
	// String fields
	if override.A2AVersion != "" {
		base.A2AVersion = override.A2AVersion
	}
	if override.BaseDir != "" {
		base.BaseDir = override.BaseDir
	}
	if override.NotificationTemplate != "" {
		base.NotificationTemplate = override.NotificationTemplate
	}
	if override.DaemonMessageTemplate != "" {
		base.DaemonMessageTemplate = override.DaemonMessageTemplate
	}
	if override.DraftTemplate != "" {
		base.DraftTemplate = override.DraftTemplate
	}
	if override.ReminderMessage != "" {
		base.ReminderMessage = override.ReminderMessage
	}
	if override.CommonTemplate != "" {
		base.CommonTemplate = override.CommonTemplate
	}
	if override.EdgeViolationWarningTemplate != "" {
		base.EdgeViolationWarningTemplate = override.EdgeViolationWarningTemplate
	}
	if override.EdgeViolationWarningMode != "" {
		base.EdgeViolationWarningMode = override.EdgeViolationWarningMode
	}
	if override.DroppedBallEventTemplate != "" {
		base.DroppedBallEventTemplate = override.DroppedBallEventTemplate
	}
	if override.AlertActionReachableTemplate != "" {
		base.AlertActionReachableTemplate = override.AlertActionReachableTemplate
	}
	if override.AlertActionUnreachableTemplate != "" {
		base.AlertActionUnreachableTemplate = override.AlertActionUnreachableTemplate
	}
	if override.InboxStagnationAlertTemplate != "" {
		base.InboxStagnationAlertTemplate = override.InboxStagnationAlertTemplate
	}
	if override.InboxUnreadSummaryAlertTemplate != "" {
		base.InboxUnreadSummaryAlertTemplate = override.InboxUnreadSummaryAlertTemplate
	}
	if override.NodeInactivityAlertTemplate != "" {
		base.NodeInactivityAlertTemplate = override.NodeInactivityAlertTemplate
	}
	if override.UnrepliedMessageAlertTemplate != "" {
		base.UnrepliedMessageAlertTemplate = override.UnrepliedMessageAlertTemplate
	}
	if override.SpinningAlertTemplate != "" {
		base.SpinningAlertTemplate = override.SpinningAlertTemplate
	}
	if override.StalledAlertTemplate != "" {
		base.StalledAlertTemplate = override.StalledAlertTemplate
	}
	if override.MessageFooter != "" {
		base.MessageFooter = override.MessageFooter
	}
	if override.ReplyCommand != "" {
		base.ReplyCommand = override.ReplyCommand
	}
	if override.UINode != "" || override.uiNodeSet {
		base.UINode = override.UINode
		base.uiNodeSet = base.uiNodeSet || override.uiNodeSet
	}
	if override.ReadContextMode != "" {
		base.ReadContextMode = override.ReadContextMode
	}
	if override.ReadContextHeading != "" {
		base.ReadContextHeading = override.ReadContextHeading
	}
	// *bool merge: bidirectional override — nil = unset (use base), non-nil = explicit (#219)
	if override.AutoEnableNewSessions != nil {
		base.AutoEnableNewSessions = override.AutoEnableNewSessions
	}
	if override.AutoEnableNewAgents != nil {
		base.AutoEnableNewAgents = override.AutoEnableNewAgents
	}
	if override.JournalHealthCutoverEnabled != nil {
		base.JournalHealthCutoverEnabled = override.JournalHealthCutoverEnabled
	}
	if override.JournalCompatibilityCutoverEnabled != nil {
		base.JournalCompatibilityCutoverEnabled = override.JournalCompatibilityCutoverEnabled
	}

	// Float64 fields
	if override.ScanInterval != 0 {
		base.ScanInterval = override.ScanInterval
	}
	if override.EnterDelay != 0 {
		base.EnterDelay = override.EnterDelay
	}
	if override.TmuxTimeout != 0 {
		base.TmuxTimeout = override.TmuxTimeout
	}
	if override.StartupDelay != 0 {
		base.StartupDelay = override.StartupDelay
	}
	if override.ReminderInterval != 0 {
		base.ReminderInterval = override.ReminderInterval
	}
	if override.EnterVerifyDelay != 0 {
		base.EnterVerifyDelay = override.EnterVerifyDelay
	}
	if override.EnterRetryMax != 0 {
		base.EnterRetryMax = override.EnterRetryMax
	}
	if override.EdgeActivitySeconds != 0 {
		base.EdgeActivitySeconds = override.EdgeActivitySeconds
	}
	if override.NodeActiveSeconds != 0 {
		base.NodeActiveSeconds = override.NodeActiveSeconds
	}
	if override.NodeIdleSeconds != 0 {
		base.NodeIdleSeconds = override.NodeIdleSeconds
	}
	if override.NodeStaleSeconds != 0 {
		base.NodeStaleSeconds = override.NodeStaleSeconds
	}
	if override.NodeSpinningSeconds != 0 {
		base.NodeSpinningSeconds = override.NodeSpinningSeconds
	}
	if override.MessageAgeWarningSeconds != 0 {
		base.MessageAgeWarningSeconds = override.MessageAgeWarningSeconds
	}
	if override.MessageTTLSeconds != 0 {
		base.MessageTTLSeconds = override.MessageTTLSeconds
	}
	if override.MinDeliveryGapSeconds != 0 {
		base.MinDeliveryGapSeconds = override.MinDeliveryGapSeconds
	}
	if override.StartupDrainWindowSeconds != 0 {
		base.StartupDrainWindowSeconds = override.StartupDrainWindowSeconds
	}
	if override.PaneCaptureIntervalSeconds != 0 {
		base.PaneCaptureIntervalSeconds = override.PaneCaptureIntervalSeconds
	}
	if override.ActivityWindowSeconds != 0 {
		base.ActivityWindowSeconds = override.ActivityWindowSeconds
	}

	// Int fields
	if override.PaneCaptureMaxPanes != 0 {
		base.PaneCaptureMaxPanes = override.PaneCaptureMaxPanes
	}
	if override.RetentionPeriodDays != 0 {
		base.RetentionPeriodDays = override.RetentionPeriodDays
	}
	if override.InboxUnreadThreshold != 0 {
		base.InboxUnreadThreshold = override.InboxUnreadThreshold
	}
	if override.AlertCooldownSeconds != 0 {
		base.AlertCooldownSeconds = override.AlertCooldownSeconds
	}
	if override.AlertDeliveryWindowSeconds != 0 {
		base.AlertDeliveryWindowSeconds = override.AlertDeliveryWindowSeconds
	}
	if override.PaneNotifyCooldownSeconds != 0 {
		base.PaneNotifyCooldownSeconds = override.PaneNotifyCooldownSeconds
	}

	// *bool fields: bidirectional override (#219)
	if override.PaneCaptureEnabled != nil {
		base.PaneCaptureEnabled = override.PaneCaptureEnabled
	}

	// Edges: replace if override is non-empty
	if len(override.Edges) > 0 {
		base.Edges = override.Edges
	}
	if len(override.ReadContextPieces) > 0 {
		base.ReadContextPieces = append([]string{}, override.ReadContextPieces...)
	}
	base.recordNodeNames(override.NodeOrder...)

	// Nodes: field-level merge per node
	for name, overNode := range override.Nodes {
		baseNode := base.Nodes[name]
		if overNode.Template != "" {
			baseNode.Template = overNode.Template
		}
		if overNode.Role != "" {
			baseNode.Role = overNode.Role
		}
		if overNode.ReminderInterval != 0 {
			baseNode.ReminderInterval = overNode.ReminderInterval
		}
		if overNode.ReminderMessage != "" {
			baseNode.ReminderMessage = overNode.ReminderMessage
		}
		if overNode.IdleTimeoutSeconds != 0 {
			baseNode.IdleTimeoutSeconds = overNode.IdleTimeoutSeconds
		}
		if overNode.DroppedBallTimeoutSeconds != 0 {
			baseNode.DroppedBallTimeoutSeconds = overNode.DroppedBallTimeoutSeconds
		}
		if overNode.DroppedBallCooldownSeconds != 0 {
			baseNode.DroppedBallCooldownSeconds = overNode.DroppedBallCooldownSeconds
		}
		if overNode.DroppedBallNotification != "" {
			baseNode.DroppedBallNotification = overNode.DroppedBallNotification
		}
		if overNode.EnterCount != 0 {
			baseNode.EnterCount = overNode.EnterCount
		}
		if overNode.EnterDelay != 0 {
			baseNode.EnterDelay = overNode.EnterDelay
		}
		if overNode.DeliveryIdleTimeoutSeconds != 0 {
			baseNode.DeliveryIdleTimeoutSeconds = overNode.DeliveryIdleTimeoutSeconds
		}
		if overNode.DeliveryIdleRetryMax != 0 {
			baseNode.DeliveryIdleRetryMax = overNode.DeliveryIdleRetryMax
		}
		base.Nodes[name] = baseNode
	}

	// NodeDefaults: field-level merge
	if override.NodeDefaults.EnterCount != 0 {
		base.NodeDefaults.EnterCount = override.NodeDefaults.EnterCount
	}

	if override.Heartbeat.Enabled != nil {
		base.Heartbeat.Enabled = override.Heartbeat.Enabled
	}
	if override.Heartbeat.IntervalSeconds != 0 {
		base.Heartbeat.IntervalSeconds = override.Heartbeat.IntervalSeconds
	}
	if override.Heartbeat.LLMNode != "" {
		base.Heartbeat.LLMNode = override.Heartbeat.LLMNode
	}
	if override.Heartbeat.Prompt != "" {
		base.Heartbeat.Prompt = override.Heartbeat.Prompt
	}
}

func appendMessageFooter(baseFooter, localFooter string) string {
	if localFooter == "" {
		return baseFooter
	}
	if baseFooter == "" {
		return localFooter
	}
	return strings.TrimRight(baseFooter, "\n") + "\n" + strings.TrimLeft(localFooter, "\n")
}

// LoadConfig loads configuration from a TOML file (Python format).
// Python format requires [postman] section and [nodename] sections.
// If path is empty, tries XDG config fallback chain, then project-local config
// (.tmux-a2a-postman/postman.toml walked up from CWD, stopping before home dir).
// Issue #81: If no file found, loads embedded default configuration.
// Issue #121: Project-local config is merged on top of XDG/embedded config.
func LoadConfig(path string) (*Config, error) {
	configPath := path
	localPath := ""
	localMarkdownPath := ""

	xdgPath := ResolveConfigPath()
	xdgMarkdownPath := resolveXDGMarkdownPath() // Issue #324
	// Issue #274: Resolve project-local config unconditionally so that an explicit
	// --config flag does not bypass the project-local nodes/ overlay.
	if cwd, err := os.Getwd(); err == nil {
		localPath, _ = resolveProjectLocalConfig(cwd, xdgPath)
		localMarkdownPath, _ = resolveProjectLocalMarkdown(cwd, xdgMarkdownPath) // Issue #324
	}

	if configPath == "" {
		if xdgPath == "" && localPath == "" && xdgMarkdownPath == "" && localMarkdownPath == "" {
			// No user config anywhere: use embedded default
			return loadEmbeddedConfig()
		}
		configPath = xdgPath
	}

	// Load base config.
	var cfg *Config
	if configPath == "" {
		// No XDG config but project-local exists: start from embedded defaults.
		var err error
		cfg, err = loadEmbeddedConfig()
		if err != nil {
			return nil, err
		}
	} else if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if path != "" {
			// Explicit path was provided but doesn't exist
			return nil, fmt.Errorf("config file not found: %s", configPath)
		}
		// XDG path doesn't exist: start from embedded defaults.
		var embErr error
		cfg, embErr = loadEmbeddedConfig()
		if embErr != nil {
			return nil, embErr
		}
	} else {
		// Parse TOML file with metadata (Python format)
		rawBytes, err := os.ReadFile(configPath)
		if err != nil {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
		warnDeprecatedKeys(rawBytes, configPath)
		var rootSections map[string]toml.Primitive
		md, err := toml.Decode(string(rawBytes), &rootSections)
		if err != nil {
			return nil, fmt.Errorf("parsing config file: %w", err)
		}

		// Decode [postman] section (optional, uses embedded defaults as base)
		var embErr error
		cfg, embErr = loadEmbeddedConfig()
		if embErr != nil {
			return nil, embErr
		}
		postmanPrim, ok := rootSections["postman"]
		if ok {
			if err := md.PrimitiveDecode(postmanPrim, cfg); err != nil {
				return nil, fmt.Errorf("decoding [postman] section: %w", err)
			}
			cfg.uiNodeSet = tomlHasField(md, "postman", "ui_node")
		}

		// Decode [nodename] sections (everything except postman and heartbeat)
		cfg.Nodes = make(map[string]NodeConfig)
		cfg.NodeOrder = []string{}
		for _, name := range orderedTOMLNodeNames(md) {
			prim := rootSections[name]
			var node NodeConfig
			if err := md.PrimitiveDecode(prim, &node); err != nil {
				return nil, fmt.Errorf("decoding [%s] section: %w", name, err)
			}
			cfg.Nodes[name] = node
			cfg.recordNodeNames(name)
		}

		// Decode [heartbeat] section if exists
		if heartbeatPrim, ok := rootSections["heartbeat"]; ok {
			if err := md.PrimitiveDecode(heartbeatPrim, &cfg.Heartbeat); err != nil {
				return nil, fmt.Errorf("decoding [heartbeat] section: %w", err)
			}
		}

		// Decode [node_defaults] section if exists
		if nodeDefaultsPrim, ok := rootSections["node_defaults"]; ok {
			if err := md.PrimitiveDecode(nodeDefaultsPrim, &cfg.NodeDefaults); err != nil {
				return nil, fmt.Errorf("decoding [node_defaults] section: %w", err)
			}
		}

		// Issue #50: Load node files from nodes/ directory
		configDir := filepath.Dir(configPath)
		nodesDir := filepath.Join(configDir, "nodes")
		if info, err := os.Stat(nodesDir); err == nil && info.IsDir() {
			nodeFiles, _ := filepath.Glob(filepath.Join(nodesDir, "*.toml"))
			sort.Strings(nodeFiles) // deterministic alphabetical order
			for _, nodeFile := range nodeFiles {
				nodeBytes, err := os.ReadFile(nodeFile)
				if err != nil {
					log.Printf("warning: skipping %s (read error): %v", nodeFile, err)
					continue
				}
				warnDeprecatedKeys(nodeBytes, nodeFile)
				var sections map[string]toml.Primitive
				md2, err := toml.Decode(string(nodeBytes), &sections)
				if err != nil {
					log.Printf("warning: skipping %s: %v", nodeFile, err)
					continue
				}
				for _, name := range orderedTOMLNodeNames(md2) {
					prim := sections[name]
					var node NodeConfig
					if err := md2.PrimitiveDecode(prim, &node); err != nil {
						log.Printf("warning: skipping [%s] in %s: %v", name, nodeFile, err)
						continue
					}
					cfg.Nodes[name] = node // override if exists in postman.toml
					cfg.recordNodeNames(name)
				}
			}
		}
	}

	// Issue #324: XDG Markdown overlay — nodes/*.md then postman.md.
	// Load order per level: postman.toml → nodes/*.toml → nodes/*.md → postman.md
	xdgConfigDir := ""
	if xdgMarkdownPath != "" {
		xdgConfigDir = filepath.Dir(xdgMarkdownPath)
	} else if xdgPath != "" {
		xdgConfigDir = filepath.Dir(xdgPath)
	}
	if xdgConfigDir != "" {
		xdgMDNodesDir := filepath.Join(xdgConfigDir, "nodes")
		if info, err := os.Stat(xdgMDNodesDir); err == nil && info.IsDir() {
			mdFiles, _ := filepath.Glob(filepath.Join(xdgMDNodesDir, "*.md"))
			sort.Strings(mdFiles)
			for _, mdFile := range mdFiles {
				nodeName, nc, err := loadNodeMarkdownFile(mdFile)
				if err != nil {
					log.Printf("warning: skipping %s: %v", mdFile, err)
					continue
				}
				node := cfg.Nodes[nodeName]
				if nc.Template != "" {
					node.Template = nc.Template
				}
				if nc.Role != "" {
					node.Role = nc.Role
				}
				cfg.Nodes[nodeName] = node
				cfg.recordNodeNames(nodeName)
			}
		}
	}
	if xdgMarkdownPath != "" {
		if mdCfg, err := loadMarkdownConfig(xdgMarkdownPath); err == nil {
			mergeConfig(cfg, mdCfg)
		} else {
			log.Printf("warning: skipping %s: %v", xdgMarkdownPath, err)
		}
	}

	cfg.initDirectTemplateRootTrust()

	// Issue #121: Apply project-local config on top if found.
	if localPath != "" {
		localCfg, err := loadConfigFile(localPath)
		if err != nil {
			return nil, err
		}
		cfg.markDirectTemplateRootsUntrusted(localCfg)
		// Snapshot AllowShellTemplates from XDG config before applying project-local
		// overlay. Project-local config must NOT be able to self-elevate this flag.
		xdgAllowShell := cfg.AllowShellTemplates
		mergeConfig(cfg, localCfg)
		applyProjectLocalExplicitZero(cfg, localCfg)
		// Issue #274: Apply project-local nodes/ directory on top of merged config.
		localNodesDir := filepath.Join(filepath.Dir(localPath), "nodes")
		if info, err := os.Stat(localNodesDir); err == nil && info.IsDir() {
			nodeFiles, _ := filepath.Glob(filepath.Join(localNodesDir, "*.toml"))
			sort.Strings(nodeFiles) // deterministic alphabetical order
			for _, nodeFile := range nodeFiles {
				nodeBytes, err := os.ReadFile(nodeFile)
				if err != nil {
					log.Printf("warning: skipping %s (read error): %v", nodeFile, err)
					continue
				}
				warnDeprecatedKeys(nodeBytes, nodeFile)
				var sections map[string]toml.Primitive
				md2, err := toml.Decode(string(nodeBytes), &sections)
				if err != nil {
					log.Printf("warning: skipping %s: %v", nodeFile, err)
					continue
				}
				for _, name := range orderedTOMLNodeNames(md2) {
					prim := sections[name]
					var node NodeConfig
					if err := md2.PrimitiveDecode(prim, &node); err != nil {
						log.Printf("warning: skipping [%s] in %s: %v", name, nodeFile, err)
						continue
					}
					if node.ReminderMessage != "" {
						cfg.directTemplateRootTrust[reminderTemplateRoot(name)] = false
					}
					cfg.Nodes[name] = node // override if exists in XDG or postman.toml
					cfg.recordNodeNames(name)
				}
			}
		}
		// Restore: only XDG config is authoritative for shell privilege.
		cfg.AllowShellTemplates = xdgAllowShell
	}

	// Issue #324: Project-local Markdown overlay — independent of localPath (M1/I4).
	// nodes/*.md then postman.md; same load order as XDG level.
	localConfigDir := ""
	if localMarkdownPath != "" {
		localConfigDir = filepath.Dir(localMarkdownPath)
	} else if localPath != "" {
		localConfigDir = filepath.Dir(localPath)
	}
	if localConfigDir != "" {
		localMDNodesDir := filepath.Join(localConfigDir, "nodes")
		if info, err := os.Stat(localMDNodesDir); err == nil && info.IsDir() {
			mdFiles, _ := filepath.Glob(filepath.Join(localMDNodesDir, "*.md"))
			sort.Strings(mdFiles)
			for _, mdFile := range mdFiles {
				nodeName, nc, err := loadNodeMarkdownFile(mdFile)
				if err != nil {
					log.Printf("warning: skipping %s: %v", mdFile, err)
					continue
				}
				node := cfg.Nodes[nodeName]
				if nc.Template != "" {
					node.Template = nc.Template
				}
				if nc.Role != "" {
					node.Role = nc.Role
				}
				cfg.Nodes[nodeName] = node
				cfg.recordNodeNames(nodeName)
			}
		}
	}
	if localMarkdownPath != "" {
		if mdCfg, err := loadMarkdownConfig(localMarkdownPath); err == nil {
			cfg.markDirectTemplateRootsUntrusted(mdCfg)
			localFooter := mdCfg.MessageFooter
			mdCfg.MessageFooter = ""
			mergeConfig(cfg, mdCfg)
			cfg.MessageFooter = appendMessageFooter(cfg.MessageFooter, localFooter)
		} else {
			log.Printf("warning: skipping %s: %v", localMarkdownPath, err)
		}
	}

	// Issue #37: Validate EdgeActivitySeconds (1-3600 seconds)
	if cfg.EdgeActivitySeconds <= 0 {
		cfg.EdgeActivitySeconds = 1 // Force minimum
	}
	if cfg.EdgeActivitySeconds > 3600 {
		cfg.EdgeActivitySeconds = 3600 // Force maximum
	}

	// Embedded defaults intentionally allow an empty topology. Preserve that
	// behavior only when there is no XDG or explicit TOML base and overlays only
	// tweak global settings such as ui_node.
	if path == "" && xdgPath == "" && len(cfg.Nodes) == 0 && len(cfg.Edges) == 0 {
		return cfg, nil
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

// GetEdgeNodeNames extracts all unique node names from edge definitions.
func GetEdgeNodeNames(edges []string) map[string]bool {
	adjacency, err := ParseEdges(edges)
	if err != nil {
		return map[string]bool{}
	}
	nodes := make(map[string]bool)
	for node, neighbors := range adjacency {
		nodes[node] = true
		for _, neighbor := range neighbors {
			nodes[neighbor] = true
		}
	}
	return nodes
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
// Creates: sessionDir/{inbox,post,draft,read,dead-letter,waiting,todo}
func CreateSessionDirs(sessionDir string) error {
	dirs := []string{
		filepath.Join(sessionDir, "inbox"),
		filepath.Join(sessionDir, "post"),
		filepath.Join(sessionDir, "draft"),
		filepath.Join(sessionDir, "read"),
		filepath.Join(sessionDir, "dead-letter"),
		filepath.Join(sessionDir, "waiting"),
		filepath.Join(sessionDir, "todo"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// CreateMultiSessionDirs creates the multi-session directory structure.
// For multi-session support: contextDir = baseDir/contextID, sessionName = tmux session name
// Creates: contextDir/sessionName/{inbox,post,draft,read,dead-letter,waiting,todo}
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

// ResolveLocalConfigPath is the exported wrapper for resolveProjectLocalConfig.
// Returns the project-local config path walked upward from cwd, or "" if not found.
func ResolveLocalConfigPath(cwd, xdgPath string) (string, error) {
	return resolveProjectLocalConfig(cwd, xdgPath)
}

// ResolveContextID returns the context ID from the explicit --context-id flag.
// Returns error if explicitID is empty.
func ResolveContextID(explicitID string) (string, error) {
	if explicitID != "" {
		if !binding.ValidateNodeName(explicitID) {
			return "", fmt.Errorf("--context-id %q: invalid value", explicitID)
		}
		return explicitID, nil
	}
	return "", fmt.Errorf("--context-id is required")
}

// ValidateSessionName validates and sanitises a tmux session name.
// Returns the filepath.Base-cleaned name, or an error if the name
// contains path separators or collapses to a dot component.
func ValidateSessionName(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("session name %q: invalid value", name)
	}
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("session name %q: invalid value", name)
	}
	if strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("session name %q: invalid value", name)
	}
	clean := filepath.Base(name)
	if clean == "" || clean == "." || clean == ".." {
		return "", fmt.Errorf("session name %q: invalid value", name)
	}
	return clean, nil
}

// IsSessionPIDAlive reads postman.pid from baseDir/contextName/sessionName/
// and returns true if the recorded process is still running.
// Issue #249: liveness check for context disambiguation.
func IsSessionPIDAlive(baseDir, contextName, sessionName string) bool {
	pidPath := filepath.Join(baseDir, contextName, sessionName, "postman.pid")
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	sigErr := proc.Signal(syscall.Signal(0))
	return sigErr == nil || errors.Is(sigErr, syscall.EPERM)
}

// isContextDaemonAlive checks if any session subdirectory under
// baseDir/contextName contains a live postman.pid. A daemon writes its PID
// file under its own tmux session, but may manage other sessions via
// cross-session discovery. This helper detects liveness regardless of which
// session the daemon started from.
func isContextDaemonAlive(baseDir, contextName string) bool {
	contextDir := filepath.Join(baseDir, contextName)
	sessions, err := os.ReadDir(contextDir)
	if err != nil {
		return false
	}
	for _, s := range sessions {
		if !s.IsDir() {
			continue
		}
		if IsSessionPIDAlive(baseDir, contextName, s.Name()) {
			return true
		}
	}
	return false
}

// ContextHasLiveDaemon reports whether any session under contextName has a live
// postman.pid. This is the exported lifecycle guard for cleanup and ownership
// decisions.
func ContextHasLiveDaemon(baseDir, contextName string) bool {
	return isContextDaemonAlive(baseDir, contextName)
}

func enabledSessionOwner(baseDir, sessionName string) string {
	if sessionName == "" {
		return ""
	}
	out, err := exec.Command("tmux", "show-options", "-gqv", "@a2a_session_on_"+sessionName).Output()
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return ""
	}
	owner, _, _ := strings.Cut(value, ":")
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return ""
	}
	if !isContextDaemonAlive(baseDir, owner) {
		return ""
	}
	return owner
}

// ContextOwnsSession reports whether contextName currently owns sessionName.
// Ownership means the context has a subdirectory for sessionName, has a live
// daemon PID somewhere under that context, and either:
//   - the live enabled-session marker names that context, or
//   - the queried session is the daemon's own tmux session.
func ContextOwnsSession(baseDir, contextName, sessionName string) bool {
	if baseDir == "" || contextName == "" || sessionName == "" {
		return false
	}
	sessionDir := filepath.Join(baseDir, contextName, sessionName)
	if _, err := os.Stat(sessionDir); err != nil {
		return false
	}
	if !isContextDaemonAlive(baseDir, contextName) {
		return false
	}
	if owner := enabledSessionOwner(baseDir, sessionName); owner != "" {
		return owner == contextName
	}
	return FindContextSessionName(baseDir, contextName) == sessionName
}

// ResolveContextIDFromSession scans baseDir for context directories that
// contain a live postman daemon managing sessionName. A daemon writes its PID
// file under its own tmux session but may manage other sessions via
// cross-session discovery. The resolver checks: (1) the context has a
// subdirectory for sessionName (daemon knows about this session), and (2) any
// session subdirectory under the context has a live PID file.
// Issue #229: safe auto-resolution without env vars or stale files.
// Issue #249: liveness-aware resolution using postman.pid.
func ResolveContextIDFromSession(baseDir, sessionName string) (string, error) {
	if baseDir == "" || sessionName == "" {
		return "", fmt.Errorf("no active postman found: base_dir or session name is empty")
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return "", fmt.Errorf("no active postman found: %w", err)
	}
	var matches []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if ContextOwnsSession(baseDir, e.Name(), sessionName) {
			matches = append(matches, e.Name())
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no active postman found: no live daemon in %s", baseDir)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("constraint violation: %d live daemons: %s", len(matches), strings.Join(matches, ", "))
	}
}

// FindSessionOwner scans baseDir for a context (other than ownContextID) that has
// a live postman daemon managing sessionName.
// A daemon's PID file may be under a different session subdirectory than the
// one being queried (cross-session management). The check verifies: (1) the
// context has a subdirectory for sessionName, and (2) any session subdirectory
// under the context has a live PID file.
// Returns the first matching context ID, or "" if none found.
// Issue #249: Used by TUI-level session-ON guard to prevent duplicate routing.
func FindSessionOwner(baseDir, sessionName, ownContextID string) string {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() || e.Name() == ownContextID {
			continue
		}
		if ContextOwnsSession(baseDir, e.Name(), sessionName) {
			return e.Name()
		}
	}
	return ""
}

// FindContextSessionName returns the tmux session name where the given context's
// daemon is currently running (the session subdir that has a live postman.pid).
// Returns "" if not found.
func FindContextSessionName(baseDir, contextID string) string {
	contextDir := filepath.Join(baseDir, contextID)
	sessions, err := os.ReadDir(contextDir)
	if err != nil {
		return ""
	}
	for _, s := range sessions {
		if !s.IsDir() {
			continue
		}
		if IsSessionPIDAlive(baseDir, contextID, s.Name()) {
			return s.Name()
		}
	}
	return ""
}

func SetSessionEnabledMarker(contextID, sessionName string, enabled bool) error {
	if sessionName == "" {
		return fmt.Errorf("session name is empty")
	}
	key := "@a2a_session_on_" + sessionName
	if enabled {
		if contextID == "" {
			return fmt.Errorf("context ID is empty")
		}
		value := contextID + ":" + strconv.Itoa(os.Getpid())
		return exec.Command("tmux", "set-option", "-g", key, value).Run()
	}
	return exec.Command("tmux", "set-option", "-gu", key).Run()
}

// GetTmuxSessionName extracts the tmux session name using tmux command.
// Uses TMUX_PANE env var to target the originating pane, not the currently focused pane.
// Fails closed (returns empty) if TMUX_PANE is set but targeted lookup fails.
func GetTmuxSessionName() string {
	paneID := os.Getenv("TMUX_PANE")
	if paneID != "" {
		cmd := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{session_name}")
		output, err := cmd.Output()
		if err != nil {
			return "" // fail closed
		}
		return strings.TrimSpace(string(output))
	}
	// TMUX_PANE absent: untargeted fallback (existing behavior)
	cmd := exec.Command("tmux", "display-message", "-p", "#{session_name}")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// GetTmuxPaneID returns the current tmux pane ID (e.g. "%42").
// Uses TMUX_PANE env var when available; falls back to display-message query.
// Returns empty string if not in tmux or if the command fails.
func GetTmuxPaneID() string {
	paneID := os.Getenv("TMUX_PANE")
	if paneID != "" {
		return paneID
	}
	cmd := exec.Command("tmux", "display-message", "-p", "#{pane_id}")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// GetTmuxPaneName returns the current tmux pane title.
// Uses TMUX_PANE env var to target the originating pane, not the currently focused pane.
// Fails closed (returns empty) if TMUX_PANE is set but targeted lookup fails.
func GetTmuxPaneName() string {
	paneID := os.Getenv("TMUX_PANE")
	if paneID != "" {
		cmd := exec.Command("tmux", "display-message", "-t", paneID, "-p", "#{pane_title}")
		output, err := cmd.Output()
		if err != nil {
			return "" // fail closed
		}
		return strings.TrimSpace(string(output))
	}
	// TMUX_PANE absent: untargeted fallback (existing behavior)
	cmd := exec.Command("tmux", "display-message", "-p", "#{pane_title}")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

// GetNodeConfig returns the effective NodeConfig for the given node name,
// applying NodeDefaults as base with node-specific config merged on top.
func (cfg *Config) GetNodeConfig(name string) NodeConfig {
	result := cfg.NodeDefaults
	specific, ok := cfg.Nodes[name]
	if !ok {
		return result
	}
	if specific.Template != "" {
		result.Template = specific.Template
	}
	if specific.Role != "" {
		result.Role = specific.Role
	}
	if specific.ReminderInterval != 0 {
		result.ReminderInterval = specific.ReminderInterval
	}
	if specific.ReminderMessage != "" {
		result.ReminderMessage = specific.ReminderMessage
	}
	if specific.IdleTimeoutSeconds != 0 || cfg.hasProjectLocalExplicitZeroNode(name, "idle_timeout_seconds") {
		result.IdleTimeoutSeconds = specific.IdleTimeoutSeconds
	}
	if specific.DroppedBallTimeoutSeconds != 0 || cfg.hasProjectLocalExplicitZeroNode(name, "dropped_ball_timeout_seconds") {
		result.DroppedBallTimeoutSeconds = specific.DroppedBallTimeoutSeconds
	}
	if specific.DroppedBallCooldownSeconds != 0 || cfg.hasProjectLocalExplicitZeroNode(name, "dropped_ball_cooldown_seconds") {
		result.DroppedBallCooldownSeconds = specific.DroppedBallCooldownSeconds
	}
	if specific.DroppedBallNotification != "" {
		result.DroppedBallNotification = specific.DroppedBallNotification
	}
	if specific.EnterCount != 0 || cfg.hasProjectLocalExplicitZeroNode(name, "enter_count") {
		result.EnterCount = specific.EnterCount
	}
	if specific.EnterDelay != 0 || cfg.hasProjectLocalExplicitZeroNode(name, "enter_delay_seconds") {
		result.EnterDelay = specific.EnterDelay
	}
	if specific.DeliveryIdleTimeoutSeconds != 0 || cfg.hasProjectLocalExplicitZeroNode(name, "delivery_idle_timeout_seconds") {
		result.DeliveryIdleTimeoutSeconds = specific.DeliveryIdleTimeoutSeconds
	}
	if specific.DeliveryIdleRetryMax != 0 || cfg.hasProjectLocalExplicitZeroNode(name, "delivery_idle_retry_max") {
		result.DeliveryIdleRetryMax = specific.DeliveryIdleRetryMax
	}
	return result
}
