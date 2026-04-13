package journal

const (
	ApprovalRequestedEventType = "approval_requested"
	ApprovalDecidedEventType   = "approval_decided"
)

type ApprovalRequestPayload struct {
	Requester string `json:"requester"`
	Reviewer  string `json:"reviewer"`
	MessageID string `json:"message_id,omitempty"`
}

type ApprovalDecision string

const (
	ApprovalDecisionApproved ApprovalDecision = "approved"
	ApprovalDecisionRejected ApprovalDecision = "rejected"
)

type ApprovalDecisionPayload struct {
	Reviewer  string           `json:"reviewer"`
	Decision  ApprovalDecision `json:"decision"`
	MessageID string           `json:"message_id,omitempty"`
}
