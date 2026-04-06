package status

type NodeHealth struct {
	Name           string `json:"name"`
	PaneID         string `json:"pane_id,omitempty"`
	PaneState      string `json:"pane_state,omitempty"`
	WaitingState   string `json:"waiting_state,omitempty"`
	VisibleState   string `json:"visible_state"`
	InboxCount     int    `json:"inbox_count"`
	WaitingCount   int    `json:"waiting_count"`
	CurrentCommand string `json:"current_command,omitempty"`
}

type WindowNode struct {
	Name string `json:"name"`
}

type SessionWindow struct {
	Index string       `json:"index"`
	Nodes []WindowNode `json:"nodes"`
}

type SessionHealth struct {
	ContextID    string          `json:"context_id"`
	SessionName  string          `json:"session_name"`
	NodeCount    int             `json:"node_count"`
	VisibleState string          `json:"visible_state"`
	Compact      string          `json:"compact"`
	Nodes        []NodeHealth    `json:"nodes"`
	Windows      []SessionWindow `json:"windows"`
}

var stateRank = map[string]int{
	"user_input": 0,
	"ready":      0,
	"active":     0,
	"idle":       0,
	"pending":    1,
	"composing":  2,
	"spinning":   3,
	"stale":      3,
	"stalled":    4,
	"stuck":      4,
}

func StateRank(state string) int {
	return stateRank[NormalizeWaitingState(state)]
}

func NormalizeWaitingState(state string) string {
	switch state {
	case "stuck":
		return "stalled"
	default:
		return state
	}
}

func NormalizePaneState(state string) string {
	switch NormalizeWaitingState(state) {
	case "active", "idle", "ready":
		return "ready"
	case "stalled":
		return "stalled"
	case "pending", "composing", "spinning", "user_input", "stale":
		return NormalizeWaitingState(state)
	case "":
		return "stale"
	default:
		return NormalizeWaitingState(state)
	}
}

func VisibleState(paneState, waitingState string, unreadCount int) string {
	state := NormalizePaneState(paneState)
	if unreadCount > 0 && StateRank("pending") >= StateRank(state) {
		state = "pending"
	}
	waiting := NormalizeWaitingState(waitingState)
	if waiting != "" && StateRank(waiting) >= StateRank(state) {
		state = waiting
	}
	return state
}

func SessionVisibleState(nodes []NodeHealth) string {
	worstState := "ready"
	worstRank := StateRank(worstState)
	for _, node := range nodes {
		state := node.VisibleState
		if state == "" {
			state = VisibleState(node.PaneState, node.WaitingState, node.InboxCount)
		}
		if rank := StateRank(state); rank > worstRank {
			worstRank = rank
			worstState = NormalizeWaitingState(state)
		}
	}
	return worstState
}
