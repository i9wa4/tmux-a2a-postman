package journal

const (
	CommandApprovalRequestedEventType = "command_approval_requested"
	CommandApprovalDecidedEventType   = "command_approval_decided"
	// CommandApprovalReplySlotReconciledEventType is an operator-authored,
	// evidence-carrying projection repair.  It is deliberately distinct from a
	// mailbox reply: it must never claim that a reviewer sent a message.
	CommandApprovalReplySlotReconciledEventType = "command_approval_reply_slot_reconciled"
	CommandExecutionDecidedEventType            = "command_execution_decided"
	CommandExecutionCompletedEventType          = "command_execution_completed"
)

// CommandApprovalReplySlotReconciledPayload records the immutable tuple an
// operator inspected before requesting a projection-only slot reconciliation.
// Consumers must independently replay and validate every field; this payload
// is an audit request, not authority to close an arbitrary input request.
type CommandApprovalReplySlotReconciledPayload struct {
	InputRequestID   string `json:"input_request_id"`
	ThreadID         string `json:"thread_id"`
	CommandHash      string `json:"command_hash"`
	Requester        string `json:"requester"`
	RequesterAddress string `json:"requester_address"`
	Approver         string `json:"approver"`
	ApproverAddress  string `json:"approver_address"`
	DecisionEventID  string `json:"decision_event_id"`
}

type CommandApprovalRequestPayload struct {
	Requester        string `json:"requester"`
	RequesterAddress string `json:"requester_address,omitempty"`
	Reviewer         string `json:"reviewer"`
	Mode             string `json:"mode"`
	Label            string `json:"label"`
	Category         string `json:"category,omitempty"`
	CommandHash      string `json:"command_hash"`
	// InputRequestID binds a decision reply to the precise delivered approval
	// prompt without recording command text.
	InputRequestID string `json:"input_request_id,omitempty"`
	Reason         string `json:"reason,omitempty"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	CommandText    string `json:"command_text,omitempty"`
	// CommandApproverNode is the config-resolved, validated command_approver_node (#626) at
	// the moment this request was created — never requester-controlled,
	// unlike Reviewer above (a plain policy-matched audit label). Decisions
	// MUST be validated against this field, not Reviewer, or the requester
	// can self-approve by controlling both sides of the comparison.
	CommandApproverNode    string `json:"command_approver_node,omitempty"`
	CommandApproverAddress string `json:"command_approver_address,omitempty"`
}

type CommandApprovalDecisionPayload struct {
	Reviewer         string           `json:"reviewer"`
	ReviewerAddress  string           `json:"reviewer_address,omitempty"`
	RequesterAddress string           `json:"requester_address,omitempty"`
	Decision         ApprovalDecision `json:"decision"`
	Reason           string           `json:"reason,omitempty"`
	MessageID        string           `json:"message_id,omitempty"`
	InputRequestID   string           `json:"input_request_id,omitempty"`
	CommandHash      string           `json:"command_hash,omitempty"`
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
