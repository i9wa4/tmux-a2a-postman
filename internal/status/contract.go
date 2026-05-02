package status

const SchemaVersion = 1

type NodeHealth struct {
	Name           string `json:"name"`
	PaneID         string `json:"pane_id,omitempty"`
	PaneState      string `json:"pane_state,omitempty"`
	VisibleState   string `json:"visible_state"`
	InboxCount     int    `json:"inbox_count"`
	CurrentCommand string `json:"current_command,omitempty"`
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
	"pending": 1,
	"stale":   2,
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
	state := NormalizePaneState(paneState)
	if unreadCount > 0 && StateRank("pending") >= StateRank(state) {
		state = "pending"
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
