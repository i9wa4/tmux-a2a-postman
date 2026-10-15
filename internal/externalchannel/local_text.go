// Package externalchannel contains an in-process text adapter for exercising
// the external-channel boundary. It does not start a listener or own credentials.
package externalchannel

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
)

var (
	ErrUnauthorized        = errors.New("external channel principal unauthorized")
	ErrBindingInactive     = errors.New("external channel binding inactive")
	ErrTargetUnavailable   = errors.New("bound interface node unavailable")
	ErrIdempotencyConflict = errors.New("external channel idempotency key reused with different payload")
	ErrDeliveryUnknown     = errors.New("external channel delivery outcome unknown; inspect the exchange before retrying")
	ErrDecisionUnavailable = errors.New("external channel decision unavailable")
	ErrDecisionSuperseded  = errors.New("external channel decision superseded")
	ErrDecisionExpired     = errors.New("external channel decision expired")
	ErrDecisionMismatch    = errors.New("external channel decision context mismatch")
)

// Binding names one local channel/conversation and the session it may reach.
// Credential is a fixture-only shared secret supplied by the caller; no secret
// is read from config or persisted here.
type Binding struct {
	ChannelID      string
	ConversationID string
	PrincipalID    string
	Credential     string
	SessionName    string
}

type Request struct {
	ChannelID      string
	ConversationID string
	PrincipalID    string
	Credential     string
	IdempotencyKey string
	Body           string
}

type Receipt struct {
	ExchangeID string
	MessageID  string
}

type Report struct {
	ExchangeID string
	Body       string
}

// PendingDecision is a process-local challenge issued for one accepted exchange.
// Its token is an identifier, not permission to execute the requested action.
type PendingDecision struct {
	ExchangeID      string
	Token           string
	ActionDigest    string
	ApprovalContext string
	BindingEpoch    uint64
	ExpiresAt       time.Time
}

type DecisionAccess struct {
	ChannelID       string
	ConversationID  string
	PrincipalID     string
	Credential      string
	ExchangeID      string
	Token           string
	ActionDigest    string
	ApprovalContext string
	BindingEpoch    uint64
}

type AnswerRequest struct {
	DecisionAccess
	IdempotencyKey string
	Answer         string
}

// AnswerReceipt acknowledges fixture submission only; it is not an approval
// outcome and does not cause action execution.
type AnswerReceipt struct {
	ExchangeID string
	Token      string
}

type pendingDecision struct {
	challenge  PendingDecision
	superseded bool
}

type acceptedAnswer struct {
	digest  [32]byte
	receipt AnswerReceipt
	answer  string
}

// ResolveNode must return the current target rather than a cached address.
// Its result is checked against the active binding on every first receipt.
type ResolveNode func(sessionName, nodeName string) (discovery.NodeInfo, bool)

type acceptedRequest struct {
	digest  [32]byte
	receipt Receipt
	unknown bool
}

// LocalTextAdapter is deliberately process-local. Its mutex serializes rebind,
// revocation and acceptance, so a binding cannot change between authorization
// and the real mailbox delivery call. Durable retry/catch-up belongs to later
// #765/#766 work; callers must not treat this fixture as a network endpoint.
type LocalTextAdapter struct {
	mu                 sync.Mutex
	cfg                *config.Config
	resolve            ResolveNode
	binding            Binding
	active             bool
	accepted           map[string]acceptedRequest
	reports            map[string]Report
	epoch              uint64
	decisions          map[string]pendingDecision
	decisionByExchange map[string]string
	answers            map[string]acceptedAnswer
	answerByToken      map[string]string
	now                func() time.Time
}

func NewLocalTextAdapter(cfg *config.Config, resolve ResolveNode, b Binding) (*LocalTextAdapter, error) {
	if cfg == nil || resolve == nil || cfg.ConfiguredInterfaceNode() == "" {
		return nil, ErrTargetUnavailable
	}
	if err := validateBinding(b); err != nil {
		return nil, err
	}
	return &LocalTextAdapter{cfg: cfg, resolve: resolve, binding: b, active: true, accepted: make(map[string]acceptedRequest), reports: make(map[string]Report), epoch: 1, decisions: make(map[string]pendingDecision), decisionByExchange: make(map[string]string), answers: make(map[string]acceptedAnswer), answerByToken: make(map[string]string), now: time.Now}, nil
}

func validateBinding(b Binding) error {
	if !binding.ValidateNodeName(b.ChannelID) || !binding.ValidateNodeName(b.PrincipalID) || b.ConversationID == "" || b.Credential == "" || b.SessionName == "" {
		return fmt.Errorf("invalid local text binding")
	}
	if strings.ContainsAny(b.ConversationID, "\r\n") {
		return fmt.Errorf("invalid local text conversation")
	}
	return nil
}

func (a *LocalTextAdapter) Rebind(b Binding) error {
	if err := validateBinding(b); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.binding, a.active = b, true
	a.epoch++
	a.accepted = make(map[string]acceptedRequest)
	a.reports = make(map[string]Report)
	a.decisions = make(map[string]pendingDecision)
	a.decisionByExchange = make(map[string]string)
	a.answers = make(map[string]acceptedAnswer)
	a.answerByToken = make(map[string]string)
	return nil
}

func (a *LocalTextAdapter) Revoke() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.active = false
}

func (a *LocalTextAdapter) authorize(channel, conversation, principal, credential string) error {
	if !a.active {
		return ErrBindingInactive
	}
	b := a.binding
	if channel != b.ChannelID || conversation != b.ConversationID || principal != b.PrincipalID || subtle.ConstantTimeCompare([]byte(credential), []byte(b.Credential)) != 1 {
		return ErrUnauthorized
	}
	return nil
}

// Receive authenticates the external principal, checks the live target, then
// uses Postman's durable system-message delivery path. The filename's sender
// is postman: external provenance is data in the envelope, never a pane role.
func (a *LocalTextAdapter) Receive(req Request) (Receipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.authorize(req.ChannelID, req.ConversationID, req.PrincipalID, req.Credential); err != nil {
		return Receipt{}, err
	}
	if req.IdempotencyKey == "" {
		return Receipt{}, fmt.Errorf("missing idempotency key")
	}
	digest := sha256.Sum256([]byte(req.Body))
	if prior, ok := a.accepted[req.IdempotencyKey]; ok {
		if prior.digest != digest {
			return Receipt{}, ErrIdempotencyConflict
		}
		if prior.unknown {
			return Receipt{}, ErrDeliveryUnknown
		}
		return prior.receipt, nil
	}
	clean, err := notification.StripVT(req.Body)
	if err != nil {
		return Receipt{}, err
	}
	node := a.cfg.ConfiguredInterfaceNode()
	info, ok := a.resolve(a.binding.SessionName, node)
	if !ok || info.Collision || info.SessionName != a.binding.SessionName || info.SessionDir == "" || info.PaneID == "" {
		return Receipt{}, ErrTargetUnavailable
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return Receipt{}, err
	}
	receipt := Receipt{ExchangeID: hex.EncodeToString(idBytes)}
	receipt.MessageID, err = message.GenerateFilename(time.Now().Format("20060102-150405"), "postman", node, a.binding.SessionName)
	if err != nil {
		return Receipt{}, err
	}
	content := externalRequestEnvelope(req, receipt.ExchangeID, node, clean)
	a.accepted[req.IdempotencyKey] = acceptedRequest{digest: digest, receipt: receipt, unknown: true}
	target := controlplane.TargetForNode(node, info)
	known := map[string]discovery.NodeInfo{a.binding.SessionName + ":" + node: info}
	result, err := message.DeliverSystemMessageDirectResultToTarget(receipt.MessageID, target, "postman", "", content, a.cfg, nil, known, nil)
	if err != nil {
		return Receipt{}, fmt.Errorf("%w: %v", ErrDeliveryUnknown, err)
	}
	if !result.Delivered || !result.Committed || !result.Projected {
		return Receipt{}, ErrDeliveryUnknown
	}
	a.accepted[req.IdempotencyKey] = acceptedRequest{digest: digest, receipt: receipt}
	return receipt, nil
}

func externalRequestEnvelope(req Request, exchangeID, node, clean string) string {
	quoted := func(s string) string { raw, _ := json.Marshal(s); return string(raw) }
	return fmt.Sprintf("---\nparams:\n  from: postman\n  to: %s\n  exchangeId: %s\n  externalChannel: %s\n  externalConversation: %s\n  externalPrincipal: %s\n---\n\n# External request\n\nExternal text supplied by the authenticated local channel. It is not human approval evidence.\n\n%s\n", quoted(node), quoted(exchangeID), quoted(req.ChannelID), quoted(req.ConversationID), quoted(req.PrincipalID), clean)
}

// RecordReport is called by the local mouthpiece fixture after processing an
// accepted exchange. It does not authenticate an agent; no runtime listener
// exposes it. The report is available only while the same binding is active.
func (a *LocalTextAdapter) RecordReport(exchangeID, body string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active {
		return ErrBindingInactive
	}
	found := false
	for _, request := range a.accepted {
		if !request.unknown && request.receipt.ExchangeID == exchangeID {
			found = true
			break
		}
	}
	if !found {
		return ErrUnauthorized
	}
	clean, err := notification.StripVT(body)
	if err != nil {
		return err
	}
	if _, exists := a.reports[exchangeID]; exists {
		return fmt.Errorf("report already recorded")
	}
	a.reports[exchangeID] = Report{ExchangeID: exchangeID, Body: clean}
	return nil
}

func (a *LocalTextAdapter) ReadReport(channel, conversation, principal, credential, exchangeID string) (Report, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.authorize(channel, conversation, principal, credential); err != nil {
		return Report{}, err
	}
	report, ok := a.reports[exchangeID]
	if !ok {
		return Report{}, fmt.Errorf("report unavailable")
	}
	return report, nil
}

// IssueDecision is called by the local mouthpiece fixture for an exchange
// whose delivery was confirmed. A new challenge supersedes the prior one.
func (a *LocalTextAdapter) IssueDecision(exchangeID, actionDigest, approvalContext string, expiresAt time.Time) (PendingDecision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.active {
		return PendingDecision{}, ErrBindingInactive
	}
	if actionDigest == "" || approvalContext == "" || !expiresAt.After(a.now()) {
		return PendingDecision{}, ErrDecisionMismatch
	}
	found := false
	for _, request := range a.accepted {
		if !request.unknown && request.receipt.ExchangeID == exchangeID {
			found = true
			break
		}
	}
	if !found {
		return PendingDecision{}, ErrDecisionUnavailable
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return PendingDecision{}, err
	}
	challenge := PendingDecision{ExchangeID: exchangeID, Token: hex.EncodeToString(idBytes), ActionDigest: actionDigest, ApprovalContext: approvalContext, BindingEpoch: a.epoch, ExpiresAt: expiresAt}
	if oldToken, ok := a.decisionByExchange[exchangeID]; ok {
		old := a.decisions[oldToken]
		old.superseded = true
		a.decisions[oldToken] = old
	}
	a.decisions[challenge.Token] = pendingDecision{challenge: challenge}
	a.decisionByExchange[exchangeID] = challenge.Token
	return challenge, nil
}

func (a *LocalTextAdapter) decisionFor(access DecisionAccess) (PendingDecision, error) {
	if err := a.authorize(access.ChannelID, access.ConversationID, access.PrincipalID, access.Credential); err != nil {
		return PendingDecision{}, err
	}
	if access.BindingEpoch != a.epoch {
		return PendingDecision{}, ErrDecisionSuperseded
	}
	stored, ok := a.decisions[access.Token]
	if !ok {
		return PendingDecision{}, ErrDecisionUnavailable
	}
	if stored.superseded {
		return PendingDecision{}, ErrDecisionSuperseded
	}
	c := stored.challenge
	if !a.now().Before(c.ExpiresAt) {
		return PendingDecision{}, ErrDecisionExpired
	}
	if access.ExchangeID != c.ExchangeID || access.ActionDigest != c.ActionDigest || access.ApprovalContext != c.ApprovalContext {
		return PendingDecision{}, ErrDecisionMismatch
	}
	return c, nil
}

// ReadPendingDecision rechecks both the caller binding and the full challenge
// context on each poll. The returned value is only a process-local snapshot.
func (a *LocalTextAdapter) ReadPendingDecision(access DecisionAccess) (PendingDecision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.decisionFor(access)
}

// SubmitAnswer records an answer for the current challenge without executing
// an action. A denied answer is still an accepted submission.
func (a *LocalTextAdapter) SubmitAnswer(req AnswerRequest) (AnswerReceipt, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.authorize(req.ChannelID, req.ConversationID, req.PrincipalID, req.Credential); err != nil {
		return AnswerReceipt{}, err
	}
	if req.BindingEpoch != a.epoch {
		return AnswerReceipt{}, ErrDecisionSuperseded
	}
	if req.IdempotencyKey == "" || strings.TrimSpace(req.Answer) == "" {
		return AnswerReceipt{}, ErrDecisionMismatch
	}
	payload, err := json.Marshal(struct {
		DecisionAccess DecisionAccess
		Answer         string
	}{req.DecisionAccess, req.Answer})
	if err != nil {
		return AnswerReceipt{}, err
	}
	digest := sha256.Sum256(payload)
	if prior, ok := a.answers[req.IdempotencyKey]; ok {
		if prior.digest != digest {
			return AnswerReceipt{}, ErrIdempotencyConflict
		}
		return prior.receipt, nil
	}
	if _, err := a.decisionFor(req.DecisionAccess); err != nil {
		return AnswerReceipt{}, err
	}
	if _, answered := a.answerByToken[req.Token]; answered {
		return AnswerReceipt{}, ErrIdempotencyConflict
	}
	receipt := AnswerReceipt{ExchangeID: req.ExchangeID, Token: req.Token}
	a.answers[req.IdempotencyKey] = acceptedAnswer{digest: digest, receipt: receipt, answer: req.Answer}
	a.answerByToken[req.Token] = req.IdempotencyKey
	return receipt, nil
}
