package message

import "github.com/i9wa4/tmux-a2a-postman/internal/router"

type deliveryAction string

const (
	deliveryActionNone       deliveryAction = ""
	deliveryActionDeliver    deliveryAction = "deliver"
	deliveryActionDeadLetter deliveryAction = "dead_letter"
)

type deliveryDecision struct {
	Action                     deliveryAction
	DeadLetterSuffix           string
	DeadLetterReason           string
	EventReason                string
	SendDeadLetterNotification bool
	SendRoutingWarning         bool
}

type deliveryPolicyInput struct {
	Filename string
	Info     MessageInfo

	ParseError        bool
	SourceSessionName string
	DaemonSession     string

	EnvelopeChecked  bool
	EnvelopeMismatch bool

	RecipientResolved   bool
	RecipientResolution router.Resolution
	RecipientForeign    bool

	SenderResolved   bool
	SenderResolution router.Resolution

	RoutingChecked bool
	RoutingAllowed bool

	SenderSessionChecked    bool
	SenderSessionEnabled    bool
	RecipientSessionChecked bool
	RecipientSessionEnabled bool

	QueueChecked bool
	QueueCount   int
	QueueCap     int

	EvidencePresenceGateChecked bool
	EvidencePresenceGateActive  bool
	CompletionClaim             bool
	EvidencePresent             bool
}

func planDeliveryPolicy(input deliveryPolicyInput) deliveryDecision {
	if input.ParseError {
		return deliveryDecision{
			Action:           deliveryActionDeadLetter,
			DeadLetterSuffix: dlSuffixParseError,
			EventReason:      "parse error",
		}
	}

	if input.Info.From == "postman" {
		return forgedSenderDecision()
	}
	if input.Info.From == "daemon" && input.DaemonSession != "" && input.SourceSessionName != input.DaemonSession {
		return forgedSenderDecision()
	}

	if input.EnvelopeChecked && input.EnvelopeMismatch {
		return deliveryDecision{
			Action:                     deliveryActionDeadLetter,
			DeadLetterSuffix:           dlSuffixEnvelopeMismatch,
			DeadLetterReason:           deadLetterReasonEnvelopeMismatch,
			EventReason:                deadLetterReasonEnvelopeMismatch,
			SendDeadLetterNotification: true,
		}
	}

	if input.EvidencePresenceGateChecked &&
		input.EvidencePresenceGateActive &&
		input.CompletionClaim &&
		!input.EvidencePresent {
		return deliveryDecision{
			Action:                     deliveryActionDeadLetter,
			DeadLetterSuffix:           dlSuffixMissingEvidence,
			DeadLetterReason:           deadLetterReasonMissingEvidence,
			EventReason:                deadLetterReasonMissingEvidence,
			SendDeadLetterNotification: true,
		}
	}

	if input.RecipientResolved {
		if !input.RecipientResolution.Found {
			if input.RecipientResolution.FailureReason == router.FailureUnknownSession {
				return deliveryDecision{
					Action:                     deliveryActionDeadLetter,
					DeadLetterSuffix:           dlSuffixUnknownSession,
					DeadLetterReason:           deadLetterReasonUnknownRecipientSession,
					EventReason:                "unknown recipient session",
					SendDeadLetterNotification: true,
				}
			}
			return deliveryDecision{
				Action:                     deliveryActionDeadLetter,
				DeadLetterSuffix:           dlSuffixUnknownRecipient,
				DeadLetterReason:           deadLetterReasonUnknownRecipient,
				EventReason:                "unknown recipient",
				SendDeadLetterNotification: true,
			}
		}
		if input.RecipientForeign {
			return deliveryDecision{
				Action:                     deliveryActionDeadLetter,
				DeadLetterSuffix:           dlSuffixForeignSession,
				DeadLetterReason:           deadLetterReasonForeignSession,
				EventReason:                "foreign session",
				SendDeadLetterNotification: true,
			}
		}
	}

	if input.SenderResolved && !input.SenderResolution.Found && input.Info.From != "daemon" {
		return deliveryDecision{
			Action:           deliveryActionDeadLetter,
			DeadLetterSuffix: dlSuffixUnknownSender,
			EventReason:      "unknown sender",
		}
	}

	if input.RoutingChecked && input.Info.From != "daemon" && !input.RoutingAllowed {
		return deliveryDecision{
			Action:             deliveryActionDeadLetter,
			DeadLetterSuffix:   dlSuffixRoutingDenied,
			EventReason:        "routing denied",
			SendRoutingWarning: true,
		}
	}

	if input.SenderSessionChecked && input.Info.From != "daemon" && !input.SenderSessionEnabled {
		return deliveryDecision{
			Action:                     deliveryActionDeadLetter,
			DeadLetterSuffix:           dlSuffixSessionDisabled,
			DeadLetterReason:           deadLetterReasonSenderSessionDisabled,
			EventReason:                "sender session disabled",
			SendDeadLetterNotification: true,
		}
	}

	if input.RecipientSessionChecked && input.Info.From != "daemon" && !input.RecipientSessionEnabled {
		return deliveryDecision{
			Action:                     deliveryActionDeadLetter,
			DeadLetterSuffix:           dlSuffixSessionDisabled,
			DeadLetterReason:           deadLetterReasonRecipientSessionDisabled,
			EventReason:                "recipient session disabled",
			SendDeadLetterNotification: true,
		}
	}

	if input.QueueChecked {
		if input.QueueCount >= input.QueueCap {
			return deliveryDecision{
				Action:                     deliveryActionDeadLetter,
				DeadLetterSuffix:           dlSuffixQueueFull,
				DeadLetterReason:           deadLetterReasonQueueFull,
				EventReason:                "inbox queue full",
				SendDeadLetterNotification: true,
			}
		}
		return deliveryDecision{Action: deliveryActionDeliver}
	}

	return deliveryDecision{}
}

func forgedSenderDecision() deliveryDecision {
	return deliveryDecision{
		Action:           deliveryActionDeadLetter,
		DeadLetterSuffix: dlSuffixForgedSender,
		EventReason:      "forged sender",
	}
}
