package status

const SchemaVersion = 1

type NodeHealth struct {
	Name                string `json:"name"`
	PaneID              string `json:"pane_id,omitempty"`
	PaneState           string `json:"pane_state,omitempty"`
	VisibleState        string `json:"visible_state"`
	InboxCount          int    `json:"inbox_count"`
	ActionRequiredCount int    `json:"action_required_count,omitempty"`
	WaitingOnReplyCount int    `json:"waiting_on_reply_count,omitempty"`
	InfoUnreadCount     int    `json:"info_unread_count,omitempty"`
	CurrentCommand      string `json:"current_command,omitempty"`
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

type SessionHealth struct {
	ContextID    string          `json:"context_id"`
	SessionName  string          `json:"session_name"`
	NodeCount    int             `json:"node_count"`
	VisibleState string          `json:"visible_state"`
	Compact      string          `json:"compact"`
	Queues       SessionQueues   `json:"queues"`
	Nodes        []NodeHealth    `json:"nodes"`
	Windows      []SessionWindow `json:"windows"`
}

type DaemonOwner struct {
	ContextID   string `json:"context_id"`
	SessionName string `json:"session_name"`
}

type AllSessionHealth struct {
	SchemaVersion int             `json:"schema_version"`
	ContextID     string          `json:"context_id"`
	DaemonOwner   *DaemonOwner    `json:"daemon_owner,omitempty"`
	Sessions      []SessionHealth `json:"sessions"`
}

var stateRank = map[string]int{
	"ready":   0,
	"active":  0,
	"idle":    0,
	"waiting": 1,
	"pending": 2,
	"stale":   3,
}

func StateRank(state string) int {
	return stateRank[NormalizeState(state)]
}

func NormalizePaneState(state string) string {
	return NormalizeState(state)
}

func NormalizeState(state string) string {
	switch state {
	case "active", "idle", "ready":
		return "ready"
	case "waiting":
		return "waiting"
	case "pending":
		return "pending"
	case "stale":
		return "stale"
	case "":
		return "stale"
	default:
		return state
	}
}

func VisibleState(paneState string, unreadCount int) string {
	return VisibleStateWithObligations(paneState, unreadCount, -1, 0)
}

func VisibleStateWithObligations(paneState string, unreadCount, actionRequiredCount, waitingOnReplyCount int) string {
	state := NormalizePaneState(paneState)
	if state == "stale" {
		return state
	}
	if actionRequiredCount > 0 {
		return "pending"
	}
	if actionRequiredCount < 0 && unreadCount > 0 {
		return "pending"
	}
	if waitingOnReplyCount > 0 {
		return "waiting"
	}
	return state
}

func SessionVisibleState(nodes []NodeHealth) string {
	worstState := "ready"
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
