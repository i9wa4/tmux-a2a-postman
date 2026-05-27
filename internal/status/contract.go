package status

const SchemaVersion = 4

const DeliveryStuckAfterSeconds = 180

type NodeStatus struct {
	Name                string                  `json:"name"`
	PaneID              string                  `json:"pane_id,omitempty"`
	PaneState           string                  `json:"pane_state,omitempty"`
	VisibleState        string                  `json:"visible_state"`
	Severity            string                  `json:"severity,omitempty"`
	SeveritySource      string                  `json:"severity_source,omitempty"`
	SeverityReason      string                  `json:"severity_reason,omitempty"`
	InboxCount          int                     `json:"inbox_count"`
	InputRequiredCount  int                     `json:"input_required_count,omitempty"`
	WaitingOnInputCount int                     `json:"waiting_on_input_count,omitempty"`
	InfoUnreadCount     int                     `json:"info_unread_count,omitempty"`
	InputRequired       []InputRequestDetail    `json:"-"`
	WaitingOnInput      []InputRequestDetail    `json:"-"`
	CurrentCommand      string                  `json:"current_command,omitempty"`
	ScreenProgress      *ScreenProgressEvidence `json:"screen_progress,omitempty"`
	NodeLocal           *NodeLocalStatus        `json:"node_local,omitempty"`
	Flow                *NodeFlowStatus         `json:"flow,omitempty"`
	Queues              *NodeQueues             `json:"queues,omitempty"`
}

type ScreenProgressEvidence struct {
	EvidenceState      string `json:"evidence_state"`
	LastCaptureAt      string `json:"last_capture_at,omitempty"`
	LastScreenChangeAt string `json:"last_screen_change_at,omitempty"`
	ScreenFingerprint  string `json:"screen_fingerprint,omitempty"`
}

type StatusItem struct {
	Node             string `json:"node,omitempty"`
	MessageID        string `json:"message_id,omitempty"`
	InputRequestID   string `json:"input_request_id,omitempty"`
	BlockedReportID  string `json:"blocked_report_id,omitempty"`
	Scope            string `json:"scope,omitempty"`
	ScopeID          string `json:"scope_id,omitempty"`
	Path             string `json:"path,omitempty"`
	Reason           string `json:"reason,omitempty"`
	EvidenceSource   string `json:"evidence_source,omitempty"`
	EvidenceLevel    string `json:"evidence_level,omitempty"`
	ObservedAt       string `json:"observed_at,omitempty"`
	AgeSeconds       int    `json:"age_seconds,omitempty"`
	EnqueuedAt       string `json:"enqueued_at,omitempty"`
	EnqueuedAtSource string `json:"enqueued_at_source,omitempty"`
}

type NodeQueues struct {
	InboxCount int `json:"inbox_count"`
}

type InputRequestSummary struct {
	InputRequiredCount  int                  `json:"input_required_count"`
	WaitingOnInputCount int                  `json:"waiting_on_input_count"`
	InfoUnreadCount     int                  `json:"info_unread_count"`
	InputRequired       []InputRequestDetail `json:"input_required,omitempty"`
	WaitingOnInput      []InputRequestDetail `json:"waiting_on_input,omitempty"`
}

type InputRequestDetail struct {
	Direction      string `json:"direction"`
	MessageID      string `json:"message_id"`
	InputRequestID string `json:"input_request_id,omitempty"`
	Sender         string `json:"sender"`
	Recipient      string `json:"recipient"`
	ReplyPolicy    string `json:"reply_policy,omitempty"`
	OpenedAt       string `json:"opened_at,omitempty"`
	OpenedAtSource string `json:"opened_at_source,omitempty"`
	OpenedEventID  string `json:"opened_event_id,omitempty"`
	ReadAt         string `json:"read_at,omitempty"`
	ReadEventID    string `json:"read_event_id,omitempty"`
}

type BlockedState struct {
	State     string       `json:"state"`
	OpenCount int          `json:"open_count"`
	Items     []StatusItem `json:"items,omitempty"`
}

type NodeFlowStatus struct {
	State          string              `json:"state"`
	Severity       string              `json:"severity"`
	EvidenceLevel  string              `json:"evidence_level"`
	EvidenceSource string              `json:"evidence_source,omitempty"`
	Reason         string              `json:"reason,omitempty"`
	Action         string              `json:"action,omitempty"`
	InputRequests  InputRequestSummary `json:"input_requests"`
	Blocked        BlockedState        `json:"blocked"`
}

type NodeLocalStatus struct {
	State          string                  `json:"state"`
	Severity       string                  `json:"severity"`
	EvidenceLevel  string                  `json:"evidence_level"`
	EvidenceSource string                  `json:"evidence_source,omitempty"`
	Reason         string                  `json:"reason,omitempty"`
	PaneState      string                  `json:"pane_state,omitempty"`
	CurrentCommand string                  `json:"current_command,omitempty"`
	ScreenProgress *ScreenProgressEvidence `json:"screen_progress,omitempty"`
}

type DeliveryStatus struct {
	State                string       `json:"state"`
	Severity             string       `json:"severity"`
	EvidenceLevel        string       `json:"evidence_level"`
	EvidenceSource       string       `json:"evidence_source,omitempty"`
	Reason               string       `json:"reason,omitempty"`
	Action               string       `json:"action,omitempty"`
	PostCount            int          `json:"post_count"`
	DeadLetterCount      int          `json:"dead_letter_count"`
	StuckAfterSeconds    int          `json:"stuck_after_seconds"`
	OldestPostAgeSeconds int          `json:"oldest_post_age_seconds,omitempty"`
	OldestPostObservedAt string       `json:"oldest_post_observed_at,omitempty"`
	Items                []StatusItem `json:"items,omitempty"`
}

type WindowNode struct {
	Name string `json:"name"`
}

type SessionWindow struct {
	Index string       `json:"index"`
	Nodes []WindowNode `json:"nodes"`
}

type SessionQueues struct {
	PostCount       int `json:"post_count"`
	InboxCount      int `json:"inbox_count"`
	DeadLetterCount int `json:"dead_letter_count"`
}

type SessionStatus struct {
	SchemaVersion      int                 `json:"schema_version"`
	ContextID          string              `json:"context_id"`
	SessionName        string              `json:"session_name"`
	NodeCount          int                 `json:"node_count"`
	VisibleState       string              `json:"visible_state"`
	Severity           string              `json:"severity,omitempty"`
	SeveritySource     string              `json:"severity_source,omitempty"`
	SeverityReason     string              `json:"severity_reason,omitempty"`
	Compact            string              `json:"compact"`
	CompactSeverity    string              `json:"compact_severity,omitempty"`
	Queues             SessionQueues       `json:"queues"`
	Delivery           *DeliveryStatus     `json:"delivery,omitempty"`
	RuntimeDiagnostics *RuntimeDiagnostics `json:"runtime_diagnostics,omitempty"`
	Nodes              []NodeStatus        `json:"nodes"`
	Windows            []SessionWindow     `json:"windows"`
}

type DaemonOwner struct {
	ContextID   string `json:"context_id"`
	SessionName string `json:"session_name"`
}

type RuntimeMemoryDiagnostics struct {
	HeapAllocBytes   uint64 `json:"heap_alloc_bytes"`
	HeapSysBytes     uint64 `json:"heap_sys_bytes"`
	HeapObjects      uint64 `json:"heap_objects"`
	StackInuseBytes  uint64 `json:"stack_inuse_bytes"`
	TotalAllocBytes  uint64 `json:"total_alloc_bytes"`
	MemorySysBytes   uint64 `json:"memory_sys_bytes"`
	MemoryFreesCount uint64 `json:"memory_frees_count"`
}

type RuntimeGCDiagnostics struct {
	Count        uint32 `json:"count"`
	NextGCBytes  uint64 `json:"next_gc_bytes"`
	PauseTotalNS uint64 `json:"pause_total_ns"`
	LastPauseNS  uint64 `json:"last_pause_ns"`
}

type GoRuntimeDiagnostics struct {
	Memory         RuntimeMemoryDiagnostics `json:"memory"`
	GC             RuntimeGCDiagnostics     `json:"gc"`
	GoroutineCount int                      `json:"goroutine_count"`
}

type DaemonRuntimeCardinality struct {
	SessionCount            int `json:"session_count"`
	NodeCount               int `json:"node_count"`
	WatchedDirCount         int `json:"watched_dir_count"`
	ClaimedPaneCount        int `json:"claimed_pane_count"`
	ActivePostEventCount    int `json:"active_post_event_count"`
	ActiveAutoPingCount     int `json:"active_auto_ping_count"`
	ActiveDaemonSubmitCount int `json:"active_daemon_submit_count"`
}

type RuntimeDiagnostics struct {
	Source      string                   `json:"source"`
	PointInTime bool                     `json:"point_in_time"`
	ObservedAt  string                   `json:"observed_at"`
	GoRuntime   GoRuntimeDiagnostics     `json:"go_runtime"`
	Daemon      DaemonRuntimeCardinality `json:"daemon"`
}

type AllSessionStatus struct {
	SchemaVersion int             `json:"schema_version"`
	ContextID     string          `json:"context_id"`
	DaemonOwner   *DaemonOwner    `json:"daemon_owner,omitempty"`
	Sessions      []SessionStatus `json:"sessions"`
}

var stateRank = map[string]int{
	"initial": 0,
	"ready":   1,
	"active":  1,
	"idle":    1,
	"waiting": 2,
	"pending": 3,
	"stale":   4,
}

var severityRank = map[string]int{
	"ok":               0,
	"working":          1,
	"expected_wait":    2,
	"needs_action":     3,
	"blocked":          4,
	"attention_stale":  5,
	"delivery_stuck":   6,
	"delivery_failure": 7,
}

func StateRank(state string) int {
	return stateRank[NormalizeState(state)]
}

func SeverityRank(severity string) int {
	if rank, ok := severityRank[severity]; ok {
		return rank
	}
	return severityRank["ok"]
}

func WorseSeverity(left, right string) string {
	if SeverityRank(right) > SeverityRank(left) {
		return right
	}
	if left == "" {
		return right
	}
	return left
}

func NormalizePaneState(state string) string {
	return NormalizeState(state)
}

func NormalizeState(state string) string {
	switch state {
	case "active", "idle", "ready":
		return "ready"
	case "", "initial":
		return "initial"
	case "waiting":
		return "waiting"
	case "pending":
		return "pending"
	case "stale":
		return "stale"
	default:
		return state
	}
}

func VisibleState(paneState string, unreadCount int) string {
	return VisibleStateWithInputRequests(paneState, unreadCount, -1, 0)
}

func VisibleStateWithInputRequests(paneState string, unreadCount, inputRequiredCount, waitingOnInputCount int) string {
	state := NormalizePaneState(paneState)
	if state == "stale" {
		return state
	}
	if inputRequiredCount > 0 {
		return "pending"
	}
	if inputRequiredCount < 0 && unreadCount > 0 {
		return "pending"
	}
	if waitingOnInputCount > 0 {
		return "waiting"
	}
	return state
}

func SessionVisibleState(nodes []NodeStatus) string {
	worstState := "initial"
	worstRank := StateRank(worstState)
	for _, node := range nodes {
		state := node.VisibleState
		if state == "" {
			state = VisibleState(node.PaneState, node.InboxCount)
		}
		if rank := StateRank(state); rank > worstRank {
			worstRank = rank
			worstState = NormalizeState(state)
		}
	}
	return worstState
}
