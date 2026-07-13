package config

import (
	_ "embed"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
)

const (
	DefaultDaemonSubmitWorkerLimit = 8
	MaxDaemonSubmitWorkerLimit     = 16
)

//go:embed postman.default.toml
var defaultConfigBytes []byte

// Config holds postman configuration loaded from TOML file.
// Python format: [postman] section contains all settings.
type Config struct {
	// Timing settings
	ScanInterval        float64 `toml:"scan_interval_seconds"`
	SessionScanInterval float64 `toml:"session_scan_interval_seconds"`
	EnterDelay          float64 `toml:"enter_delay_seconds"`
	TmuxTimeout         float64 `toml:"tmux_timeout_seconds"`
	EnterVerifyDelay    float64 `toml:"enter_verify_delay_seconds"` // Delay for post-Enter capture comparison (0 = disabled)
	EnterRetryMax       int     `toml:"enter_retry_max"`            // Max C-m retries on pane capture unchanged (0 = disabled)

	// Node state thresholds.
	NodeActiveSeconds                float64 `toml:"node_active_seconds"`                   // 0-N seconds since pane change: active
	NodeStaleSeconds                 float64 `toml:"node_stale_seconds"`                    // Memory cleanup threshold for pane capture
	InputRequestStaleSeconds         float64 `toml:"input_request_stale_seconds"`           // Status projection threshold for stale unfilled input requests
	VerdictGraceSeconds              float64 `toml:"verdict_grace_seconds"`                 // Grace period for requester verdict stamps after filled reply-required input requests
	VerdictDebtCap                   int     `toml:"verdict_debt_cap"`                      // Maximum unstamped fills a requester may carry before new reply-required sends are refused
	MessageTTLSeconds                float64 `toml:"message_ttl_seconds"`                   // Stale post/ drain TTL; 0 = disabled
	RetentionPeriodDays              int     `toml:"retention_period_days"`                 // Inactive runtime cleanup threshold in days; 0 = disabled
	DaemonSubmitQueueWarnThresholdMs int64   `toml:"daemon_submit_queue_warn_threshold_ms"` // Queue wait WARNING threshold in ms; 0 = use default (30 000)
	MinDeliveryGapSeconds            float64 `toml:"min_delivery_gap_seconds"`              // Duplicate delivery rate limit; 0 = disabled
	StartupDrainWindowSeconds        float64 `toml:"startup_drain_window_seconds"`          // Session-enabled bypass window after daemon start; 0 = disabled (#217)
	AutoPingDelaySeconds             float64 `toml:"auto_ping_delay_seconds"`               // Delay from discovery/replacement to first auto-PING
	DaemonSubmitWorkerLimit          int     `toml:"daemon_submit_worker_limit"`            // Daemon-submit worker concurrency; clamped to MaxDaemonSubmitWorkerLimit

	// Pane capture settings (hybrid idle detection)
	PaneCaptureEnabled         *bool   `toml:"pane_capture_enabled"` // nil = use default (true) (#219)
	PaneCaptureIntervalSeconds float64 `toml:"pane_capture_interval_seconds"`
	PaneCaptureMaxPanes        int     `toml:"pane_capture_max_panes"`
	PaneCaptureTailLines       int     `toml:"pane_capture_tail_lines"`
	ActivityWindowSeconds      float64 `toml:"activity_window_seconds"`

	// Paths
	BaseDir string `toml:"base_dir"`
	// Message templates
	NotificationTemplate         string            `toml:"notification_template"`
	DaemonMessageTemplate        string            `toml:"daemon_message_template"`         // Unified envelope for daemon-originated PING
	DraftTemplate                string            `toml:"draft_template"`                  // Draft body used by send
	CommonTemplate               string            `toml:"common_template"`                 // Issue #49: Shared template for all nodes
	PingSkillCatalogs            map[string]string `toml:"-"`                               // postman.md skill_path inject: ping catalogs
	CompactionSkillCatalogs      map[string]string `toml:"-"`                               // postman.md skill_path inject: compaction_ping catalogs
	EdgeViolationWarningTemplate string            `toml:"edge_violation_warning_template"` // Issue #80: Warning message for routing denied
	EdgeViolationWarningMode     string            `toml:"edge_violation_warning_mode"`     // Issue #92: "compact" or "verbose" (default: compact)
	MessageFooter                string            `toml:"message_footer"`                  // Footer appended to outgoing messages by `send` after message content

	// Global settings
	Edges                          []string                        `toml:"edges"`
	ReplyCommand                   string                          `toml:"reply_command"`
	UINode                         string                          `toml:"ui_node"`                  // Optional target filter for startup auto-PING
	AutoEnableNewSessions          *bool                           `toml:"auto_enable_new_sessions"` // nil = required default true for cross-session startup/discovery auto-PING
	WorkspaceTree                  []WorkspaceTreeNodeConfig       `toml:"workspace_tree"`           // Optional explicit hierarchy for tree aliases
	CommandApproval                []CommandApprovalPolicy         `toml:"command_approval"`
	CommandApproverNode            string                          `toml:"-"` // Mermaid-sourced reviewer node for command approval; unset/unresolvable = fail-open
	DeprecatedCommandApproverNodes []DeprecatedCommandApproverNode `toml:"-"` // Ignored legacy TOML approver keys surfaced in get-status

	// Node-specific configurations (loaded from [nodename] sections)
	Nodes map[string]NodeConfig
	// NodeOrder preserves first-seen node definition order across merged config files.
	NodeOrder []string `toml:"-"`

	// Node-level defaults applied to all nodes (loaded from [node_defaults] section)
	NodeDefaults NodeConfig

	// Shell template execution opt-in (#security)
	AllowShellTemplates bool `toml:"allow_shell_templates"`

	directTemplateRootTrust map[string]bool
	uiNodeSet               bool
}

type CommandApprovalPolicy struct {
	Requester          string  `toml:"requester"`
	Label              string  `toml:"label"`
	Category           string  `toml:"category"`
	Reviewer           string  `toml:"reviewer"`
	Mode               string  `toml:"mode"`
	ApprovalTTLSeconds float64 `toml:"approval_ttl_seconds"`
}

type DeprecatedCommandApproverNode struct {
	Field string
	Value string
}

// NodeConfig holds per-node configuration.
type NodeConfig struct {
	Template   string  `toml:"template"`
	Role       string  `toml:"role"`
	EnterCount int     `toml:"enter_count"`         // Issue #126: Number of Enter keystrokes to send (0/1 = single, 2+ = double)
	EnterDelay float64 `toml:"enter_delay_seconds"` // 0 = use global default
}

// WorkspaceTreeNodeConfig describes one node in the explicit workspace tree hierarchy.
type WorkspaceTreeNodeConfig struct {
	SessionName       string `toml:"session"`
	ID                string `toml:"id"`
	Label             string `toml:"label"`
	ParentSessionName string `toml:"parent"`
	Representative    string `toml:"representative"`
	Order             int    `toml:"order"`
}

// ResolveCommandApproverNode resolves the globally designated command_approver_node
// (#626/#629). valid reports whether the resolved name matches a node known to
// this config (the same set edges/workspace_tree validate against). An empty or
// unresolvable name is never valid — callers MUST fail open in that case rather
// than treat an invalid name as if it were configured, per the decided unified
// fail-open rule.
func (cfg *Config) ResolveCommandApproverNode() (name string, valid bool) {
	if cfg == nil {
		return "", false
	}
	name = strings.TrimSpace(cfg.CommandApproverNode)
	if name == "" {
		return "", false
	}
	_, exists := cfg.Nodes[name]
	return name, exists
}

// BoolVal dereferences a *bool with a default fallback (#219).
func BoolVal(p *bool, defaultVal bool) bool {
	if p == nil {
		return defaultVal
	}
	return *p
}

func EffectiveDaemonSubmitWorkerLimit(configured int) (int, string) {
	switch {
	case configured <= 0:
		return DefaultDaemonSubmitWorkerLimit, fmt.Sprintf("daemon_submit_worker_limit=%d is below minimum 1; using default %d", configured, DefaultDaemonSubmitWorkerLimit)
	case configured > MaxDaemonSubmitWorkerLimit:
		return MaxDaemonSubmitWorkerLimit, fmt.Sprintf("daemon_submit_worker_limit=%d exceeds maximum %d; clamping to %d", configured, MaxDaemonSubmitWorkerLimit, MaxDaemonSubmitWorkerLimit)
	default:
		return configured, ""
	}
}

// DefaultConfig returns a Config with zero values.
// All non-zero defaults are defined in postman.default.toml (SSOT).
// Only structural/derived containers are initialized here.
func DefaultConfig() *Config {
	return &Config{
		Edges:                   []string{},
		Nodes:                   make(map[string]NodeConfig),
		NodeOrder:               []string{},
		PingSkillCatalogs:       make(map[string]string),
		CompactionSkillCatalogs: make(map[string]string),
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
	return name == "postman" || name == "node_defaults"
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

func deprecatedCommandApproverNodes(postmanPrim toml.Primitive, md toml.MetaData) []DeprecatedCommandApproverNode {
	type deprecatedPolicy struct {
		CommandApproverNode string `toml:"command_approver_node"`
	}
	var decoded struct {
		CommandApproverNode string             `toml:"command_approver_node"`
		CommandApproval     []deprecatedPolicy `toml:"command_approval"`
	}
	if err := md.PrimitiveDecode(postmanPrim, &decoded); err != nil {
		return nil
	}
	var deprecated []DeprecatedCommandApproverNode
	if strings.TrimSpace(decoded.CommandApproverNode) != "" {
		deprecated = append(deprecated, DeprecatedCommandApproverNode{
			Field: "command_approver_node",
			Value: strings.TrimSpace(decoded.CommandApproverNode),
		})
	}
	for i, policy := range decoded.CommandApproval {
		if strings.TrimSpace(policy.CommandApproverNode) == "" {
			continue
		}
		deprecated = append(deprecated, DeprecatedCommandApproverNode{
			Field: fmt.Sprintf("command_approval[%d].command_approver_node", i),
			Value: strings.TrimSpace(policy.CommandApproverNode),
		})
	}
	return deprecated
}

func (cfg *Config) recordNodeNames(names ...string) {
	cfg.NodeOrder = appendUniqueNodeNames(cfg.NodeOrder, names...)
}

func (cfg *Config) ensureNodesForEdges() {
	if cfg == nil {
		return
	}
	if cfg.Nodes == nil {
		cfg.Nodes = make(map[string]NodeConfig)
	}
	for _, name := range edgeNodeNamesInOrder(cfg.Edges) {
		if name == "postman" {
			continue
		}
		if _, ok := cfg.Nodes[name]; !ok {
			cfg.Nodes[name] = NodeConfig{}
		}
		cfg.recordNodeNames(name)
	}
}

// OrderedEdgeNodeNames returns unique node names in first-seen edge order.
func OrderedEdgeNodeNames(edges []string) []string {
	return edgeNodeNamesInOrder(edges)
}

func edgeNodeNamesInOrder(edges []string) []string {
	var order []string
	for _, edge := range edges {
		for _, node := range splitEdgeNodeNames(edge) {
			order = appendUniqueNodeNames(order, node)
		}
	}
	return order
}

func splitEdgeNodeNames(edge string) []string {
	edge = strings.TrimSpace(edge)
	if edge == "" {
		return nil
	}
	separator := edgeSeparator(edge)
	if separator == "" {
		return nil
	}

	parts := strings.Split(edge, separator)
	nodes := make([]string, 0, len(parts))
	for _, part := range parts {
		node := strings.TrimSpace(part)
		if node != "" {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func edgeSeparator(edge string) string {
	if strings.Contains(edge, "---") {
		return "---"
	}
	return ""
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

func (cfg *Config) CompactionSkillCatalogForRuntime(runtime string) string {
	if cfg == nil {
		return ""
	}
	return skillCatalogForRuntime(cfg.CompactionSkillCatalogs, runtime)
}

func (cfg *Config) PingSkillCatalogForRuntime(runtime string) string {
	if cfg == nil {
		return ""
	}
	return skillCatalogForRuntime(cfg.PingSkillCatalogs, runtime)
}

func skillCatalogForRuntime(catalogs map[string]string, runtime string) string {
	if len(catalogs) == 0 {
		return ""
	}
	return catalogs[""]
}

// warnDeprecatedKeys logs a warning if deprecated TOML keys are found in rawBytes.
// Deprecated keys are caught here rather than at parse time because the Go TOML
// decoder silently ignores keys not present in the struct.
func warnDeprecatedKeys(rawBytes []byte, path string) {
	deprecated := []string{
		"startup_delay_seconds",
		"auto_enable_new_agents",
		"command_approver_node",
	}
	raw := string(rawBytes)
	for _, key := range deprecated {
		if strings.Contains(raw, key) {
			log.Printf("WARNING: %s: deprecated config key %q will be ignored; remove it from your config", path, key)
		}
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

	// Decode [nodename] sections (everything except reserved sections)
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

	// Decode [node_defaults] section if exists
	if nodeDefaultsPrim, ok := rootSections["node_defaults"]; ok {
		if err := md.PrimitiveDecode(nodeDefaultsPrim, &cfg.NodeDefaults); err != nil {
			return nil, fmt.Errorf("decoding embedded [node_defaults] section: %w", err)
		}
	}

	return cfg, nil
}

type configPathResolver struct {
	getenv      func(string) string
	userHomeDir func() (string, error)
	getwd       func() (string, error)
	stat        func(string) error
	join        func(...string) string
}

type configPathResolution struct {
	configPath   string
	tomlPath     string
	markdownPath string
	overlayDir   string
}

func defaultConfigPathResolver() configPathResolver {
	return configPathResolver{
		getenv:      os.Getenv,
		userHomeDir: os.UserHomeDir,
		getwd:       os.Getwd,
		stat: func(path string) error {
			_, err := os.Stat(path)
			return err
		},
		join: filepath.Join,
	}
}

func (r configPathResolver) withDefaults() configPathResolver {
	defaults := defaultConfigPathResolver()
	if r.getenv == nil {
		r.getenv = defaults.getenv
	}
	if r.userHomeDir == nil {
		r.userHomeDir = defaults.userHomeDir
	}
	if r.getwd == nil {
		r.getwd = defaults.getwd
	}
	if r.stat == nil {
		r.stat = defaults.stat
	}
	if r.join == nil {
		r.join = defaults.join
	}
	return r
}

func (r configPathResolver) resolveConfigPaths(explicitPath string) configPathResolution {
	r = r.withDefaults()
	tomlPath, tomlDir := r.resolvePostmanFile("postman.toml")
	markdownPath, markdownDir := r.resolvePostmanFile("postman.md")

	configPath := explicitPath
	if configPath == "" {
		configPath = tomlPath
	}

	overlayDir := ""
	if markdownPath != "" {
		overlayDir = markdownDir
	} else if tomlPath != "" {
		overlayDir = tomlDir
	}

	return configPathResolution{
		configPath:   configPath,
		tomlPath:     tomlPath,
		markdownPath: markdownPath,
		overlayDir:   overlayDir,
	}
}

func (r configPathResolver) resolvePostmanFile(filename string) (string, string) {
	if xdgConfigHome := r.getenv("XDG_CONFIG_HOME"); xdgConfigHome != "" {
		dir := r.join(xdgConfigHome, "tmux-a2a-postman")
		path := r.join(dir, filename)
		if r.stat(path) == nil {
			return path, dir
		}
		return "", ""
	}

	home, err := r.userHomeDir()
	if err != nil {
		return "", ""
	}
	dir := r.join(home, ".config", "tmux-a2a-postman")
	path := r.join(dir, filename)
	if r.stat(path) == nil {
		return path, dir
	}
	return "", ""
}

func (r configPathResolver) resolveLocalConfigPath(cwd, _ string) (string, error) {
	r = r.withDefaults()
	if cwd == "" {
		if resolvedCWD, err := r.getwd(); err == nil {
			cwd = resolvedCWD
		}
	}
	_ = cwd
	return "", nil
}

// mergeConfig merges override fields into base using "non-zero wins" semantics.
// Non-zero override values replace base values. Bool fields cannot be overridden to false.
// Edges are replaced when override has at least one entry. Nodes are merged field-by-field.
// Used to apply Markdown overlays on top of TOML/embedded config.
func mergeConfig(base, override *Config) {
	// String fields
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
	if override.CommonTemplate != "" {
		base.CommonTemplate = override.CommonTemplate
	}
	if len(override.PingSkillCatalogs) > 0 {
		if base.PingSkillCatalogs == nil {
			base.PingSkillCatalogs = make(map[string]string)
		}
		for runtime, catalog := range override.PingSkillCatalogs {
			if catalog != "" {
				base.PingSkillCatalogs[runtime] = catalog
			}
		}
	}
	if len(override.CompactionSkillCatalogs) > 0 {
		if base.CompactionSkillCatalogs == nil {
			base.CompactionSkillCatalogs = make(map[string]string)
		}
		for runtime, catalog := range override.CompactionSkillCatalogs {
			if catalog != "" {
				base.CompactionSkillCatalogs[runtime] = catalog
			}
		}
	}
	if override.EdgeViolationWarningTemplate != "" {
		base.EdgeViolationWarningTemplate = override.EdgeViolationWarningTemplate
	}
	if override.EdgeViolationWarningMode != "" {
		base.EdgeViolationWarningMode = override.EdgeViolationWarningMode
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
	if override.CommandApproverNode != "" {
		base.CommandApproverNode = override.CommandApproverNode
	}
	// *bool merge: bidirectional override — nil = unset (use base), non-nil = explicit (#219)
	if override.AutoEnableNewSessions != nil {
		base.AutoEnableNewSessions = override.AutoEnableNewSessions
	}
	if len(override.CommandApproval) > 0 {
		base.CommandApproval = override.CommandApproval
	}

	// Float64 fields
	if override.ScanInterval != 0 {
		base.ScanInterval = override.ScanInterval
	}
	if override.SessionScanInterval != 0 {
		base.SessionScanInterval = override.SessionScanInterval
	}
	if override.EnterDelay != 0 {
		base.EnterDelay = override.EnterDelay
	}
	if override.TmuxTimeout != 0 {
		base.TmuxTimeout = override.TmuxTimeout
	}
	if override.EnterVerifyDelay != 0 {
		base.EnterVerifyDelay = override.EnterVerifyDelay
	}
	if override.EnterRetryMax != 0 {
		base.EnterRetryMax = override.EnterRetryMax
	}
	if override.NodeActiveSeconds != 0 {
		base.NodeActiveSeconds = override.NodeActiveSeconds
	}
	if override.NodeStaleSeconds != 0 {
		base.NodeStaleSeconds = override.NodeStaleSeconds
	}
	if override.InputRequestStaleSeconds != 0 {
		base.InputRequestStaleSeconds = override.InputRequestStaleSeconds
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
	if override.AutoPingDelaySeconds != 0 {
		base.AutoPingDelaySeconds = override.AutoPingDelaySeconds
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
	if override.PaneCaptureTailLines != 0 {
		base.PaneCaptureTailLines = override.PaneCaptureTailLines
	}
	if override.RetentionPeriodDays != 0 {
		base.RetentionPeriodDays = override.RetentionPeriodDays
	}
	if override.DaemonSubmitQueueWarnThresholdMs != 0 {
		base.DaemonSubmitQueueWarnThresholdMs = override.DaemonSubmitQueueWarnThresholdMs
	}
	if override.DaemonSubmitWorkerLimit != 0 {
		base.DaemonSubmitWorkerLimit = override.DaemonSubmitWorkerLimit
	}
	if len(override.WorkspaceTree) > 0 {
		base.WorkspaceTree = override.WorkspaceTree
	}

	// *bool fields: bidirectional override (#219)
	if override.PaneCaptureEnabled != nil {
		base.PaneCaptureEnabled = override.PaneCaptureEnabled
	}

	// Edges: replace if override is non-empty
	if len(override.Edges) > 0 {
		base.Edges = override.Edges
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
		if overNode.EnterCount != 0 {
			baseNode.EnterCount = overNode.EnterCount
		}
		if overNode.EnterDelay != 0 {
			baseNode.EnterDelay = overNode.EnterDelay
		}
		base.Nodes[name] = baseNode
	}

	// NodeDefaults: field-level merge
	if override.NodeDefaults.EnterCount != 0 {
		base.NodeDefaults.EnterCount = override.NodeDefaults.EnterCount
	}
	if override.NodeDefaults.EnterDelay != 0 {
		base.NodeDefaults.EnterDelay = override.NodeDefaults.EnterDelay
	}
}

// LoadConfig loads configuration from a TOML file (Python format).
// Python format requires [postman] section and [nodename] sections.
// If path is empty, tries the XDG config fallback chain.
// Issue #81: If no file found, loads embedded default configuration.
func LoadConfig(path string) (*Config, error) {
	resolvedPaths := defaultConfigPathResolver().resolveConfigPaths(path)
	configPath := resolvedPaths.configPath
	xdgPath := resolvedPaths.tomlPath
	xdgMarkdownPath := resolvedPaths.markdownPath // Issue #324

	if configPath == "" {
		if xdgPath == "" && xdgMarkdownPath == "" {
			// No user config anywhere: use embedded default
			return loadEmbeddedConfig()
		}
		configPath = xdgPath
	}

	// Load base config.
	var cfg *Config
	if configPath == "" {
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
			cfg.DeprecatedCommandApproverNodes = deprecatedCommandApproverNodes(postmanPrim, md)
		}

		// Decode [nodename] sections (everything except reserved sections)
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
	xdgConfigDir := resolvedPaths.overlayDir
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

	cfg.ensureNodesForEdges()

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
	return defaultConfigPathResolver().resolveConfigPaths("").tomlPath
}

// ParseEdges parses edge definitions into an adjacency map.
// Edge format: "A --- B --- C", which creates bidirectional edges A↔B, B↔C.
// Returns error for invalid formats.
func ParseEdges(edges []string) (map[string][]string, error) {
	result := make(map[string][]string)

	for _, edge := range edges {
		edge = strings.TrimSpace(edge)
		if edge == "" {
			continue
		}

		nodes := splitEdgeNodeNames(edge)
		if len(nodes) < 2 {
			if edgeSeparator(edge) == "" {
				return nil, fmt.Errorf("invalid edge format (missing '---'): %q", edge)
			}
			return nil, fmt.Errorf("invalid edge format (need at least 2 nodes): %q", edge)
		}
		for _, node := range nodes {
			if strings.HasPrefix(node, "-") {
				return nil, fmt.Errorf("invalid edge format (empty node): %q", edge)
			}
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
// Creates: sessionDir/{inbox,post,draft,read,dead-letter}
func CreateSessionDirs(sessionDir string) error {
	dirs := []string{
		filepath.Join(sessionDir, "inbox"),
		filepath.Join(sessionDir, "post"),
		filepath.Join(sessionDir, "draft"),
		filepath.Join(sessionDir, "read"),
		filepath.Join(sessionDir, "dead-letter"),
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

// ResolveLocalConfigPath is kept for compatibility. Project-local implicit
// overlays are retired, so it always returns empty.
func ResolveLocalConfigPath(cwd, xdgPath string) (string, error) {
	return defaultConfigPathResolver().resolveLocalConfigPath(cwd, xdgPath)
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
	return sessionPIDs.isPIDAlive(pidPath)
}

// IsSessionPIDOwnedByCurrentUser reads postman.pid from
// baseDir/contextName/sessionName/ and returns true only when the PID file is
// owned by the current Unix user and the recorded process is signalable by this
// process. EPERM still means "alive" for cleanup, but not "ours" for routing or
// stop/ownership decisions.
func IsSessionPIDOwnedByCurrentUser(baseDir, contextName, sessionName string) bool {
	pidPath := filepath.Join(baseDir, contextName, sessionName, "postman.pid")
	return sessionPIDs.isPIDOwnedByCurrentUser(pidPath)
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

func isContextDaemonOwnedByCurrentUser(baseDir, contextName string) bool {
	contextDir := filepath.Join(baseDir, contextName)
	sessions, err := os.ReadDir(contextDir)
	if err != nil {
		return false
	}
	for _, s := range sessions {
		if !s.IsDir() {
			continue
		}
		if IsSessionPIDOwnedByCurrentUser(baseDir, contextName, s.Name()) {
			return true
		}
	}
	return false
}

// ContextHasLiveDaemon reports whether any session under contextName has a live
// postman.pid, including a process owned by another Unix user. This is the
// exported lifecycle guard for cleanup safety; ownership decisions use
// current-user PID checks instead.
func ContextHasLiveDaemon(baseDir, contextName string) bool {
	return isContextDaemonAlive(baseDir, contextName)
}

func CurrentUserDaemonLockPath(baseDir string) string {
	return filepath.Join(baseDir, "lock", fmt.Sprintf("user-%d.lock", sessionPIDs.currentUserID()))
}

func FindCurrentUserDaemon(baseDir string) (string, string, bool) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return "", "", false
	}
	for _, contextEntry := range entries {
		if !contextEntry.IsDir() || contextEntry.Name() == "lock" {
			continue
		}
		contextID := contextEntry.Name()
		contextDir := filepath.Join(baseDir, contextID)
		sessionEntries, err := os.ReadDir(contextDir)
		if err != nil {
			continue
		}
		for _, sessionEntry := range sessionEntries {
			if !sessionEntry.IsDir() {
				continue
			}
			sessionName := sessionEntry.Name()
			if IsSessionPIDOwnedByCurrentUser(baseDir, contextID, sessionName) {
				return contextID, sessionName, true
			}
		}
	}
	return "", "", false
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
	if !isContextDaemonOwnedByCurrentUser(baseDir, owner) {
		return ""
	}
	return owner
}

// ContextOwnsSession reports whether contextName currently owns sessionName.
// Ownership means the context has a subdirectory for sessionName, has a live
// current-user daemon PID somewhere under that context, and either:
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
	if !isContextDaemonOwnedByCurrentUser(baseDir, contextName) {
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
		if IsSessionPIDOwnedByCurrentUser(baseDir, contextID, s.Name()) {
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
	if specific.EnterCount != 0 {
		result.EnterCount = specific.EnterCount
	}
	if specific.EnterDelay != 0 {
		result.EnterDelay = specific.EnterDelay
	}
	return result
}
