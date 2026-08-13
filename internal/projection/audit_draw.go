package projection

import (
	"encoding/json"
	"math"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
)

const (
	AuditDrawEventType            = "audit_draw_event"
	VerdictEventType              = "verdict_event"
	DefaultAuditReviewProbability = 0.05
	DefaultAuditFailureMultiplier = 3
)

type VerdictEventPayload struct {
	SchemaVersion    int    `json:"schema_version"`
	VerdictMessageID string `json:"verdict_message_id"`
	Verdict          string `json:"verdict"`
	VerdictOf        string `json:"verdict_of"`
	Requester        string `json:"requester"`
	Recipient        string `json:"recipient"`
	RecordedAt       string `json:"recorded_at"`
}

type AuditDrawPayload struct {
	SchemaVersion          int     `json:"schema_version"`
	VerdictMessageID       string  `json:"verdict_message_id"`
	VerdictOf              string  `json:"verdict_of"`
	Reviewer               string  `json:"reviewer"`
	Identity               string  `json:"identity"`
	WorkClass              string  `json:"work_class"`
	RequestMessageID       string  `json:"request_message_id,omitempty"`
	AcceptedFillMessageID  string  `json:"accepted_fill_message_id,omitempty"`
	AcceptedFillContent    string  `json:"accepted_fill_content,omitempty"`
	AcceptanceCriteria     string  `json:"acceptance_criteria,omitempty"`
	PassCount              int     `json:"pass_count"`
	FailCount              int     `json:"fail_count"`
	PReview                float64 `json:"p_review"`
	PMin                   float64 `json:"p_min"`
	AuditFailureMultiplier int     `json:"audit_failure_multiplier"`
	Sampled                bool    `json:"sampled"`
	AuditTarget            string  `json:"audit_target,omitempty"`
	AuditRequestID         string  `json:"audit_request_id,omitempty"`
	AuditMessageID         string  `json:"audit_message_id,omitempty"`
	DrawnAt                string  `json:"drawn_at"`
}

type auditVerdictRequest struct {
	requester          string
	filler             string
	workClass          string
	messageID          string
	content            string
	fillMessageID      string
	fillContent        string
	acceptanceCriteria string
}

type auditTrackRecord struct {
	pass int
	fail int
}

func BuildAuditDrawPayload(sessionDir, sessionName string, verdict VerdictEventPayload, now time.Time, pMin float64) (AuditDrawPayload, bool, error) {
	if strings.TrimSpace(verdict.Verdict) != "pass" || strings.TrimSpace(verdict.VerdictOf) == "" {
		return AuditDrawPayload{}, false, nil
	}
	state, ok := loadCurrentSessionState(sessionDir)
	if !ok {
		return AuditDrawPayload{}, false, nil
	}
	events, err := journal.Replay(sessionDir)
	if err != nil || len(events) == 0 {
		return AuditDrawPayload{}, false, err
	}

	requests := make(map[string]auditVerdictRequest)
	records := make(map[string]auditTrackRecord)
	auditRequests := make(map[string]AuditDrawPayload)
	var current auditVerdictRequest
	for _, event := range events {
		if event.SessionKey != state.SessionKey || event.Generation != state.Generation {
			continue
		}
		switch event.Type {
		case MailboxProjectionDeliveredEventType, VerdictEventType, AuditDrawEventType:
		default:
			continue
		}
		if event.Type == AuditDrawEventType {
			var draw AuditDrawPayload
			if err := json.Unmarshal(event.Payload, &draw); err != nil {
				continue
			}
			if draw.AuditRequestID != "" {
				auditRequests[draw.AuditRequestID] = draw
			}
			continue
		}
		if event.Type == VerdictEventType {
			var prior VerdictEventPayload
			if err := json.Unmarshal(event.Payload, &prior); err != nil || prior.VerdictMessageID == verdict.VerdictMessageID {
				continue
			}
			if auditDraw, ok := auditRequests[prior.VerdictOf]; ok && prior.Verdict == "fail" {
				key := auditRecordKey(auditDraw.Identity, auditDraw.WorkClass)
				record := records[key]
				record.fail += DefaultAuditFailureMultiplier
				records[key] = record
				continue
			}
			request, ok := requests[prior.VerdictOf]
			if !ok || request.requester != fullNameForSession(prior.Requester, sessionName) {
				continue
			}
			key := auditRecordKey(simpleNameForSession(request.filler, sessionName), request.workClass)
			record := records[key]
			switch prior.Verdict {
			case "pass":
				record.pass++
			case "fail":
				record.fail++
			}
			records[key] = record
			continue
		}
		payload, ok := decodeMailboxEventPayload(event.Payload)
		if !ok || payload.Content == "" {
			continue
		}
		meta, err := envelope.ParseMetadata(payload.Content)
		if err != nil {
			continue
		}
		meta.From = fullNameForSession(meta.From, sessionName)
		meta.To = fullNameForSession(meta.To, sessionName)
		if meta.MessageID == "" {
			meta.MessageID = payload.MessageID
		}

		if event.Type == MailboxProjectionDeliveredEventType && envelope.ResolveReplyPolicyFromMetadata(meta) == "required" && meta.InputRequestID != "" {
			requests[meta.InputRequestID] = auditVerdictRequest{
				requester:          meta.From,
				filler:             meta.To,
				workClass:          auditWorkClass(meta),
				messageID:          meta.MessageID,
				content:            payload.Content,
				acceptanceCriteria: envelope.BodyFromContent(payload.Content),
			}
			continue
		}
		if meta.FillsInputRequestID != "" {
			request := requests[meta.FillsInputRequestID]
			if request.filler == meta.From {
				request.fillMessageID = meta.MessageID
				request.fillContent = payload.Content
				requests[meta.FillsInputRequestID] = request
			}
		}
	}

	request, ok := requests[verdict.VerdictOf]
	if !ok || request.requester != fullNameForSession(verdict.Requester, sessionName) {
		return AuditDrawPayload{}, false, nil
	}
	current = request
	identity := simpleNameForSession(current.filler, sessionName)
	record := records[auditRecordKey(identity, current.workClass)]
	pMin = NormalizeAuditReviewProbabilityFloor(pMin)
	return AuditDrawPayload{
		SchemaVersion:          1,
		VerdictMessageID:       verdict.VerdictMessageID,
		VerdictOf:              verdict.VerdictOf,
		Reviewer:               simpleNameForSession(verdict.Requester, sessionName),
		Identity:               identity,
		WorkClass:              current.workClass,
		RequestMessageID:       current.messageID,
		AcceptedFillMessageID:  current.fillMessageID,
		AcceptedFillContent:    current.fillContent,
		AcceptanceCriteria:     current.acceptanceCriteria,
		PassCount:              record.pass,
		FailCount:              record.fail,
		PReview:                ComputeAuditReviewProbability(record.pass, record.fail, pMin),
		PMin:                   pMin,
		AuditFailureMultiplier: DefaultAuditFailureMultiplier,
		DrawnAt:                now.UTC().Format(time.RFC3339),
	}, true, nil
}

func ComputeAuditReviewProbability(passCount, failCount int, pMin float64) float64 {
	pMin = NormalizeAuditReviewProbabilityFloor(pMin)
	total := passCount + failCount
	if total <= 0 {
		return 1
	}
	lcb := wilsonLowerConfidenceBound(passCount, total)
	pReview := 1 - lcb
	if pReview < pMin {
		return pMin
	}
	if pReview > 1 {
		return 1
	}
	return pReview
}

func NormalizeAuditReviewProbabilityFloor(pMin float64) float64 {
	if pMin <= 0 {
		return DefaultAuditReviewProbability
	}
	if pMin > 1 {
		return 1
	}
	return pMin
}

func wilsonLowerConfidenceBound(passCount, total int) float64 {
	if total <= 0 {
		return 0
	}
	z := 1.96
	n := float64(total)
	phat := float64(passCount) / n
	z2 := z * z
	numerator := phat + z2/(2*n) - z*math.Sqrt((phat*(1-phat)+z2/(4*n))/n)
	denominator := 1 + z2/n
	if denominator == 0 {
		return 0
	}
	lcb := numerator / denominator
	if lcb < 0 {
		return 0
	}
	if lcb > 1 {
		return 1
	}
	return lcb
}

func auditWorkClass(meta envelope.Metadata) string {
	if meta.CompletionRule != "" {
		return meta.CompletionRule
	}
	if meta.BranchID != "" {
		return meta.BranchID
	}
	if meta.InputRequestSetID != "" {
		return meta.InputRequestSetID
	}
	return "default"
}

func auditRecordKey(identity, workClass string) string {
	return identity + "\x00" + workClass
}

func fullNameForSession(name, sessionName string) string {
	return nodeaddr.Full(name, sessionName)
}
