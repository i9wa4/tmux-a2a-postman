package message

import (
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/router"
)

func TestPlanDeliveryPolicy(t *testing.T) {
	baseInfo := MessageInfo{
		Timestamp: "20260201-030000",
		From:      "orchestrator",
		To:        "worker",
	}
	foundRecipient := router.Resolution{
		Address:     "test:worker",
		SessionName: "test",
		NodeName:    "worker",
		Found:       true,
	}
	foundSender := router.Resolution{
		Address:     "test:orchestrator",
		SessionName: "test",
		NodeName:    "orchestrator",
		Found:       true,
	}

	tests := []struct {
		name string
		in   deliveryPolicyInput
		want deliveryDecision
	}{
		{
			name: "parse error",
			in: deliveryPolicyInput{
				Filename:   "badname.md",
				ParseError: true,
			},
			want: deliveryDecision{
				Action:           deliveryActionDeadLetter,
				DeadLetterSuffix: dlSuffixParseError,
				EventReason:      "parse error",
			},
		},
		{
			name: "forged postman sender",
			in: deliveryPolicyInput{
				Info:              MessageInfo{From: "postman", To: "worker"},
				SourceSessionName: "test",
				DaemonSession:     "test",
			},
			want: deliveryDecision{
				Action:           deliveryActionDeadLetter,
				DeadLetterSuffix: dlSuffixForgedSender,
				EventReason:      "forged sender",
			},
		},
		{
			name: "foreign daemon sender",
			in: deliveryPolicyInput{
				Info:              MessageInfo{From: "daemon", To: "worker"},
				SourceSessionName: "foreign",
				DaemonSession:     "daemon-session",
			},
			want: deliveryDecision{
				Action:           deliveryActionDeadLetter,
				DeadLetterSuffix: dlSuffixForgedSender,
				EventReason:      "forged sender",
			},
		},
		{
			name: "envelope mismatch",
			in: deliveryPolicyInput{
				Info:             baseInfo,
				EnvelopeChecked:  true,
				EnvelopeMismatch: true,
			},
			want: deliveryDecision{
				Action:                     deliveryActionDeadLetter,
				DeadLetterSuffix:           dlSuffixEnvelopeMismatch,
				DeadLetterReason:           deadLetterReasonEnvelopeMismatch,
				EventReason:                deadLetterReasonEnvelopeMismatch,
				SendDeadLetterNotification: true,
			},
		},
		{
			name: "unknown recipient",
			in: deliveryPolicyInput{
				Info:              baseInfo,
				RecipientResolved: true,
				RecipientResolution: router.Resolution{
					FailureReason: router.FailureUnknownNode,
				},
			},
			want: deliveryDecision{
				Action:                     deliveryActionDeadLetter,
				DeadLetterSuffix:           dlSuffixUnknownRecipient,
				DeadLetterReason:           deadLetterReasonUnknownRecipient,
				EventReason:                "unknown recipient",
				SendDeadLetterNotification: true,
			},
		},
		{
			name: "unknown recipient session",
			in: deliveryPolicyInput{
				Info:              baseInfo,
				RecipientResolved: true,
				RecipientResolution: router.Resolution{
					FailureReason: router.FailureUnknownSession,
				},
			},
			want: deliveryDecision{
				Action:                     deliveryActionDeadLetter,
				DeadLetterSuffix:           dlSuffixUnknownSession,
				DeadLetterReason:           deadLetterReasonUnknownRecipientSession,
				EventReason:                "unknown recipient session",
				SendDeadLetterNotification: true,
			},
		},
		{
			name: "unknown sender",
			in: deliveryPolicyInput{
				Info:                baseInfo,
				RecipientResolved:   true,
				RecipientResolution: foundRecipient,
				SenderResolved:      true,
				SenderResolution: router.Resolution{
					FailureReason: router.FailureUnknownNode,
				},
			},
			want: deliveryDecision{
				Action:           deliveryActionDeadLetter,
				DeadLetterSuffix: dlSuffixUnknownSender,
				EventReason:      "unknown sender",
			},
		},
		{
			name: "route denial",
			in: deliveryPolicyInput{
				Info:                baseInfo,
				RecipientResolved:   true,
				RecipientResolution: foundRecipient,
				SenderResolved:      true,
				SenderResolution:    foundSender,
				RoutingChecked:      true,
				RoutingAllowed:      false,
			},
			want: deliveryDecision{
				Action:             deliveryActionDeadLetter,
				DeadLetterSuffix:   dlSuffixRoutingDenied,
				EventReason:        "routing denied",
				SendRoutingWarning: true,
			},
		},
		{
			name: "recipient foreign session",
			in: deliveryPolicyInput{
				Info:                baseInfo,
				RecipientResolved:   true,
				RecipientResolution: foundRecipient,
				RecipientForeign:    true,
			},
			want: deliveryDecision{
				Action:                     deliveryActionDeadLetter,
				DeadLetterSuffix:           dlSuffixForeignSession,
				DeadLetterReason:           deadLetterReasonForeignSession,
				EventReason:                "foreign session",
				SendDeadLetterNotification: true,
			},
		},
		{
			name: "sender session disabled",
			in: deliveryPolicyInput{
				Info:                    baseInfo,
				RecipientResolved:       true,
				RecipientResolution:     foundRecipient,
				SenderResolved:          true,
				SenderResolution:        foundSender,
				RoutingChecked:          true,
				RoutingAllowed:          true,
				SenderSessionChecked:    true,
				SenderSessionEnabled:    false,
				RecipientSessionChecked: true,
				RecipientSessionEnabled: true,
			},
			want: deliveryDecision{
				Action:                     deliveryActionDeadLetter,
				DeadLetterSuffix:           dlSuffixSessionDisabled,
				DeadLetterReason:           deadLetterReasonSenderSessionDisabled,
				EventReason:                "sender session disabled",
				SendDeadLetterNotification: true,
			},
		},
		{
			name: "recipient session disabled",
			in: deliveryPolicyInput{
				Info:                    baseInfo,
				RecipientResolved:       true,
				RecipientResolution:     foundRecipient,
				SenderResolved:          true,
				SenderResolution:        foundSender,
				RoutingChecked:          true,
				RoutingAllowed:          true,
				SenderSessionChecked:    true,
				SenderSessionEnabled:    true,
				RecipientSessionChecked: true,
				RecipientSessionEnabled: false,
			},
			want: deliveryDecision{
				Action:                     deliveryActionDeadLetter,
				DeadLetterSuffix:           dlSuffixSessionDisabled,
				DeadLetterReason:           deadLetterReasonRecipientSessionDisabled,
				EventReason:                "recipient session disabled",
				SendDeadLetterNotification: true,
			},
		},
		{
			name: "queue full",
			in: deliveryPolicyInput{
				Info:                    baseInfo,
				RecipientResolved:       true,
				RecipientResolution:     foundRecipient,
				SenderResolved:          true,
				SenderResolution:        foundSender,
				RoutingChecked:          true,
				RoutingAllowed:          true,
				SenderSessionChecked:    true,
				SenderSessionEnabled:    true,
				RecipientSessionChecked: true,
				RecipientSessionEnabled: true,
				QueueChecked:            true,
				QueueCount:              inboxQueueCap,
				QueueCap:                inboxQueueCap,
			},
			want: deliveryDecision{
				Action:                     deliveryActionDeadLetter,
				DeadLetterSuffix:           dlSuffixQueueFull,
				DeadLetterReason:           deadLetterReasonQueueFull,
				EventReason:                "inbox queue full",
				SendDeadLetterNotification: true,
			},
		},
		{
			name: "delivery",
			in: deliveryPolicyInput{
				Info:                    baseInfo,
				RecipientResolved:       true,
				RecipientResolution:     foundRecipient,
				SenderResolved:          true,
				SenderResolution:        foundSender,
				RoutingChecked:          true,
				RoutingAllowed:          true,
				SenderSessionChecked:    true,
				SenderSessionEnabled:    true,
				RecipientSessionChecked: true,
				RecipientSessionEnabled: true,
				QueueChecked:            true,
				QueueCount:              inboxQueueCap - 1,
				QueueCap:                inboxQueueCap,
			},
			want: deliveryDecision{Action: deliveryActionDeliver},
		},
		{
			name: "daemon direct delivery bypasses route and sessions",
			in: deliveryPolicyInput{
				Info:                    MessageInfo{From: "daemon", To: "worker"},
				SourceSessionName:       "daemon-session",
				DaemonSession:           "daemon-session",
				RecipientResolved:       true,
				RecipientResolution:     foundRecipient,
				RoutingChecked:          true,
				RoutingAllowed:          false,
				SenderSessionChecked:    true,
				SenderSessionEnabled:    false,
				RecipientSessionChecked: true,
				RecipientSessionEnabled: false,
				QueueChecked:            true,
				QueueCount:              0,
				QueueCap:                inboxQueueCap,
			},
			want: deliveryDecision{Action: deliveryActionDeliver},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := planDeliveryPolicy(tt.in)
			if got != tt.want {
				t.Fatalf("planDeliveryPolicy() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
