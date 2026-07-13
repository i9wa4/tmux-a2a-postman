package verdictgate

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

type Options struct {
	GraceSeconds  int
	DebtCap       int
	ExemptUINode  string
	RecordTimeout func(sessionDir, sessionName string, payload journal.MailboxEventPayload) error
}

const (
	DefaultGraceSeconds = 3600
	DefaultDebtCap      = 3
)

var timeoutMu sync.Mutex

func Enforce(sessionDir, sender, filename, content string, opts Options) error {
	meta, err := envelope.ParseMetadata(content)
	if err != nil {
		return nil
	}
	if envelope.ResolveReplyPolicyFromMetadata(meta) != "required" {
		return nil
	}
	sessionName := filepath.Base(sessionDir)
	sender, err = NormalizeSender(sessionName, sender, meta.From)
	if err != nil {
		return err
	}
	if IsExemptSender(sender, opts.ExemptUINode) {
		return nil
	}
	state, ok, err := projection.ProjectVerdictDebtState(sessionDir, sessionName, time.Now(), opts.GraceSeconds)
	if err != nil || !ok {
		return err
	}
	debt := applyOutgoingVerdict(state.Requesters[sender], sender, meta)
	if debt.ExpiredCount > 0 {
		if err := recordVerdictNoneTimeouts(sessionDir, sessionName, sender, opts.GraceSeconds, debt.Items, opts.RecordTimeout); err != nil {
			return fmt.Errorf("reply gate: recording verdict:none timeout for requester %q: %w", sender, err)
		}
		return fmt.Errorf("reply gate: verdict required before new reply-required send: requester %q has %d unstamped fill(s) past verdict_grace_seconds=%d", sender, debt.ExpiredCount, opts.GraceSeconds)
	}
	if opts.DebtCap >= 0 && debt.UnstampedCount > opts.DebtCap {
		return fmt.Errorf("reply gate: verdict required before new reply-required send: requester %q has verdict debt %d above verdict_debt_cap=%d", sender, debt.UnstampedCount, opts.DebtCap)
	}
	return nil
}

func NormalizeSender(sessionName, sender, envelopeSender string) (string, error) {
	sender = strings.TrimSpace(sender)
	envelopeSender = strings.TrimSpace(envelopeSender)
	if sender == "" {
		return "", fmt.Errorf("reply gate: refusing reply-required send without authoritative daemon-submit sender")
	}
	if envelopeSender == "" {
		return "", fmt.Errorf("reply gate: refusing reply-required send without envelope sender")
	}
	normalizedSender := projection.SimpleNameForSession(sender, sessionName)
	normalizedEnvelopeSender := projection.SimpleNameForSession(envelopeSender, sessionName)
	if normalizedSender != normalizedEnvelopeSender {
		return "", fmt.Errorf("reply gate: daemon-submit sender %q does not match envelope sender %q", sender, envelopeSender)
	}
	return normalizedSender, nil
}

func IsExemptSender(sender, exemptUINode string) bool {
	return sender == "messenger" || sender == exemptUINode
}

func applyOutgoingVerdict(debt projection.VerdictRequesterDebt, sender string, meta envelope.Metadata) projection.VerdictRequesterDebt {
	if strings.TrimSpace(meta.Verdict) == "" || strings.TrimSpace(meta.VerdictOf) == "" {
		return debt
	}
	verdictOf := strings.TrimSpace(meta.VerdictOf)
	filtered := debt.Items[:0]
	for _, item := range debt.Items {
		if item.Requester == sender && item.InputRequestID == verdictOf {
			continue
		}
		filtered = append(filtered, item)
	}
	debt.Items = filtered
	debt.UnstampedCount = 0
	debt.ExpiredCount = 0
	for _, item := range debt.Items {
		debt.UnstampedCount++
		if item.Expired {
			debt.ExpiredCount++
		}
	}
	return debt
}

func recordVerdictNoneTimeouts(sessionDir, sessionName, requester string, graceSeconds int, items []projection.VerdictDebtItem, recorder func(string, string, journal.MailboxEventPayload) error) error {
	timeoutMu.Lock()
	defer timeoutMu.Unlock()

	recorded, err := recordedVerdictNoneTimeouts(sessionDir, sessionName)
	if err != nil {
		return err
	}
	for _, item := range items {
		if !item.Expired || item.Requester != requester {
			continue
		}
		key := timeoutKey(item.Requester, item.InputRequestID)
		if recorded[key] {
			continue
		}
		payload := journal.MailboxEventPayload{
			MessageID: item.FillMessage + ".verdict-none",
			From:      item.Requester,
			To:        item.Filler,
			Content:   verdictNoneTimeoutContent(item),
			FailureReason: fmt.Sprintf(
				"verdict timeout after verdict_grace_seconds=%d",
				graceSeconds,
			),
		}
		if recorder == nil {
			recorder = RecordTimeoutWithCurrentLease
		}
		if err := recorder(sessionDir, sessionName, payload); err != nil {
			return err
		}
		recorded[key] = true
	}
	return nil
}

func RecordTimeoutWithCurrentLease(sessionDir, sessionName string, payload journal.MailboxEventPayload) error {
	writer, err := journal.OpenCurrentWriter(sessionDir)
	if err != nil {
		return err
	}
	_, err = writer.AppendEvent(projection.VerdictNoneTimeoutEventType, journal.VisibilityOperatorVisible, payload, time.Now())
	return err
}

func recordedVerdictNoneTimeouts(sessionDir, sessionName string) (map[string]bool, error) {
	recorded := make(map[string]bool)
	sessionKey, generation, ok := projection.CurrentSessionIdentity(sessionDir)
	if !ok {
		return recorded, nil
	}
	if err := journal.ReplayEach(sessionDir, func(event journal.Event) error {
		if event.Type != projection.VerdictNoneTimeoutEventType ||
			event.TmuxSessionName != sessionName ||
			event.SessionKey != sessionKey ||
			event.Generation != generation {
			return nil
		}
		var payload journal.MailboxEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return err
		}
		meta, err := envelope.ParseMetadata(payload.Content)
		if err != nil {
			return nil
		}
		requester := projection.SimpleNameForSession(payload.From, sessionName)
		verdictOf := strings.TrimSpace(meta.VerdictOf)
		if requester == "" || verdictOf == "" {
			return nil
		}
		recorded[timeoutKey(requester, verdictOf)] = true
		return nil
	}); err != nil {
		return nil, err
	}
	return recorded, nil
}

func timeoutKey(requester, inputRequestID string) string {
	return requester + "\x00" + inputRequestID
}

func verdictNoneTimeoutContent(item projection.VerdictDebtItem) string {
	messageID := item.FillMessage + ".verdict-none"
	return "---\nparams:\n" +
		"  from: " + item.Requester + "\n" +
		"  to: " + item.Filler + "\n" +
		"  messageId: " + messageID + "\n" +
		"  verdict: none\n" +
		"  verdictOf: " + item.InputRequestID + "\n" +
		"---\n\nverdict timeout recorded\n"
}
