package journal

const (
	CommandApprovalRequestedEventType  = "command_approval_requested"
	CommandApprovalDecidedEventType    = "command_approval_decided"
	CommandExecutionDecidedEventType   = "command_execution_decided"
	CommandExecutionCompletedEventType = "command_execution_completed"
)

type CommandApprovalRequestPayload struct {
	Requester   string `json:"requester"`
	Reviewer    string `json:"reviewer"`
	Mode        string `json:"mode"`
	Label       string `json:"label"`
	Category    string `json:"category,omitempty"`
	CommandHash string `json:"command_hash"`
	Reason      string `json:"reason,omitempty"`
	ExpiresAt   string `json:"expires_at,omitempty"`
	CommandText string `json:"command_text,omitempty"`
}

type CommandApprovalDecisionPayload struct {
	Reviewer string           `json:"reviewer"`
	Decision ApprovalDecision `json:"decision"`
	Reason   string           `json:"reason,omitempty"`
}

type CommandExecutionDecisionPayload struct {
	Requester      string `json:"requester"`
	Reviewer       string `json:"reviewer"`
	Mode           string `json:"mode"`
	Label          string `json:"label"`
	Category       string `json:"category,omitempty"`
	CommandHash    string `json:"command_hash"`
	Decision       string `json:"decision"`
	Reason         string `json:"reason,omitempty"`
	Override       bool   `json:"override,omitempty"`
	ApprovalThread string `json:"approval_thread,omitempty"`
	CommandText    string `json:"command_text,omitempty"`
}

type CommandExecutionCompletedPayload struct {
	Requester      string `json:"requester"`
	Reviewer       string `json:"reviewer,omitempty"`
	Mode           string `json:"mode"`
	Label          string `json:"label"`
	Category       string `json:"category,omitempty"`
	CommandHash    string `json:"command_hash"`
	ApprovalThread string `json:"approval_thread,omitempty"`
	StartedAt      string `json:"started_at"`
	CompletedAt    string `json:"completed_at"`
	DurationMillis int64  `json:"duration_ms"`
	ExitStatus     int    `json:"exit_status"`
	CommandText    string `json:"command_text,omitempty"`
}
