package mandate

import "strconv"

const (
	DefaultAcceptancePredicate = "transport_fills_do_not_imply_task_acceptance"
	SupersessionCurrent        = "current"
	SupersessionSuperseded     = "superseded"
	TerminalPending            = "pending"
	TerminalAccepted           = "accepted"
	TerminalRejected           = "rejected"
)

type Model struct {
	MandateID               string `json:"mandate_id,omitempty"`
	AuthorityGeneration     int    `json:"authority_generation,omitempty"`
	LaneID                  string `json:"lane_id,omitempty"`
	ParentLaneID            string `json:"parent_lane_id,omitempty"`
	AcceptancePredicate     string `json:"acceptance_predicate,omitempty"`
	IncompleteLanes         []Lane `json:"incomplete_lanes,omitempty"`
	SupersessionState       string `json:"supersession_state,omitempty"`
	TerminalAcceptanceState string `json:"terminal_acceptance_state,omitempty"`
}

type Lane struct {
	LaneID                  string `json:"lane_id"`
	ParentLaneID            string `json:"parent_lane_id,omitempty"`
	Owner                   string `json:"owner,omitempty"`
	State                   string `json:"state"`
	NextAction              string `json:"next_action,omitempty"`
	MessageID               string `json:"message_id,omitempty"`
	InputRequestID          string `json:"input_request_id,omitempty"`
	BranchID                string `json:"branch_id,omitempty"`
	SupersessionState       string `json:"supersession_state,omitempty"`
	TerminalAcceptanceState string `json:"terminal_acceptance_state,omitempty"`
}

type Fields struct {
	TaskID                  string
	RunID                   string
	MandateID               string
	AuthorityGeneration     int
	LaneID                  string
	ParentLaneID            string
	BranchID                string
	InputRequestID          string
	FillsInputRequestID     string
	InputRequestSetID       string
	MessageID               string
	AcceptancePredicate     string
	CompletionRule          string
	SupersessionState       string
	TerminalAcceptanceState string
}

type Authority struct {
	MandateID           string
	AuthorityGeneration int
}

type SupersessionRecord struct {
	MandateID           string
	AuthorityGeneration int
	SupersessionState   string
}

func FromFields(fields Fields) Model {
	model := Model{
		MandateID:               firstNonEmpty(fields.MandateID, fields.TaskID, fields.RunID),
		AuthorityGeneration:     fields.AuthorityGeneration,
		LaneID:                  firstNonEmpty(fields.LaneID, fields.BranchID, fields.InputRequestID, fields.FillsInputRequestID, fields.InputRequestSetID, fields.MessageID),
		ParentLaneID:            fields.ParentLaneID,
		AcceptancePredicate:     firstNonEmpty(fields.AcceptancePredicate, fields.CompletionRule),
		SupersessionState:       firstNonEmpty(fields.SupersessionState, SupersessionCurrent),
		TerminalAcceptanceState: firstNonEmpty(fields.TerminalAcceptanceState, TerminalPending),
	}
	if model.AcceptancePredicate == "" {
		model.AcceptancePredicate = DefaultAcceptancePredicate
	}
	return model
}

func SupersessionRecordFromFields(fields Fields) SupersessionRecord {
	return SupersessionRecord{
		MandateID:           fields.MandateID,
		AuthorityGeneration: fields.AuthorityGeneration,
		SupersessionState:   firstNonEmpty(fields.SupersessionState, SupersessionCurrent),
	}
}

func SupersessionRecordFromAuthority(authority Authority) SupersessionRecord {
	return SupersessionRecord{
		MandateID:           authority.MandateID,
		AuthorityGeneration: authority.AuthorityGeneration,
		SupersessionState:   SupersessionCurrent,
	}
}

func (record SupersessionRecord) Authority() Authority {
	return Authority{
		MandateID:           record.MandateID,
		AuthorityGeneration: record.AuthorityGeneration,
	}
}

func RequiresExactAuthority(authority Authority) bool {
	return authority.MandateID != "" || authority.AuthorityGeneration > 0
}

func ExactAuthorityMatches(request, fill Authority) bool {
	if !RequiresExactAuthority(request) {
		return fill.MandateID == "" && fill.AuthorityGeneration <= 0
	}
	if request.MandateID == "" || request.AuthorityGeneration <= 0 {
		return false
	}
	return fill.MandateID == request.MandateID && fill.AuthorityGeneration == request.AuthorityGeneration
}

func SupersessionFillMatches(request, fill SupersessionRecord) bool {
	if request.SupersessionState == SupersessionSuperseded || fill.SupersessionState == SupersessionSuperseded {
		return false
	}
	return ExactAuthorityMatches(request.Authority(), fill.Authority())
}

func SupersessionAdvanceSupersedesOpenRequest(current, next SupersessionRecord) bool {
	if current.MandateID == "" || next.MandateID == "" || current.MandateID != next.MandateID {
		return false
	}
	return current.AuthorityGeneration > 0 && next.AuthorityGeneration > current.AuthorityGeneration
}

func SupersessionStateForGeneration(requestGeneration, fillGeneration int) string {
	if requestGeneration > 0 && fillGeneration > 0 && fillGeneration != requestGeneration {
		return SupersessionSuperseded
	}
	if requestGeneration > 0 && fillGeneration <= 0 {
		return SupersessionSuperseded
	}
	return SupersessionCurrent
}

func IsAcceptancePredicate(value string) bool {
	return value == "accepted" || value == "task_accepted"
}

func IntString(value int) string {
	if value <= 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func ParseGeneration(value string) int {
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
