package cli

import (
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func resultCount(n int) *int { return &n }

func TestResultGuidance(t *testing.T) {
	zero, one, two := resultCount(0), resultCount(1), resultCount(2)
	cases := []struct {
		name, operation, existing string
		self                      workspace.NodeStatus
		success, claimed, pending bool
		want, absent              []string
	}{
		{
			"claimed POP and next unread", "pop", "Read this exact archive through EOF.",
			workspace.NodeStatus{UnreadCount: two, InboundCount: one, OutboundCount: one},
			true, true, false,
			[]string{"Read this exact archive through EOF. Remaining unread messages after this POP: 2", "Own required responses remain", "approve-execution or decline-execution"},
			[]string{"Sent requests await responses"},
		},
		{
			"empty POP despite positive observation", "pop", "",
			workspace.NodeStatus{UnreadCount: one, InboundCount: one},
			true, false, false,
			nil,
			[]string{"tmux-a2a-postman pop", "Own required responses remain"},
		},
		{
			"failed POP despite positive observation", "pop", "",
			workspace.NodeStatus{UnreadCount: one},
			false, false, false,
			nil,
			[]string{"tmux-a2a-postman pop"},
		},
		{
			"send with own unread", "mail", "",
			workspace.NodeStatus{UnreadCount: one, InboundCount: zero, OutboundCount: one},
			true, false, false,
			[]string{"Run `tmux-a2a-postman pop`", "read its complete archive through EOF"},
			[]string{"Sent requests await responses"},
		},
		{
			"exact reply leaves own unread", "mail", "",
			workspace.NodeStatus{UnreadCount: one, InboundCount: zero, OutboundCount: zero},
			true, false, false,
			[]string{"Run `tmux-a2a-postman pop`"},
			[]string{"Own required responses remain"},
		},
		{
			"typed decision with claimed pending work", "mail", "",
			workspace.NodeStatus{UnreadCount: zero, InboundCount: one, OutboundCount: one},
			true, false, false,
			[]string{"Own required responses remain", "approve-execution or decline-execution for typed requests", "Keep long-running work pending"},
			[]string{"Sent requests await responses", "Run `tmux-a2a-postman pop`"},
		},
		{
			"outbound only", "execute", "",
			workspace.NodeStatus{UnreadCount: zero, InboundCount: zero, OutboundCount: one},
			true, false, false,
			[]string{"Sent requests await responses", "Wait for a genuine reply"},
			[]string{"Run `tmux-a2a-postman pop`", "Own required responses remain"},
		},
		{
			"zero obligations", "mail", "",
			workspace.NodeStatus{UnreadCount: zero, InboundCount: zero, OutboundCount: zero},
			true, false, false,
			nil,
			[]string{"pop", "wait", "all clear"},
		},
		{
			"unknown counts", "mail", "",
			workspace.NodeStatus{Reason: "summary unavailable"},
			true, false, false,
			nil,
			[]string{"pop", "wait", "all clear"},
		},
		{
			"unknown inbound prevents wait", "mail", "",
			workspace.NodeStatus{UnreadCount: zero, OutboundCount: one},
			true, false, false,
			nil,
			[]string{"Sent requests await responses"},
		},
		{
			"uncertain send still permits own mail", "mail", "",
			workspace.NodeStatus{UnreadCount: one, InboundCount: zero, OutboundCount: one},
			false, false, false,
			[]string{"Run `tmux-a2a-postman pop`"},
			[]string{"resend", "retry delivery"},
		},
		{
			"external partial and pending", "channel-sync", "",
			workspace.NodeStatus{UnreadCount: one, InboundCount: one, OutboundCount: one},
			false, false, true,
			[]string{"Run `tmux-a2a-postman pop`", "External decisions are pending in channel.pending"},
			[]string{"Sent requests await responses", "retry"},
		},
		{
			"external pending outbound only", "channel-sync", "",
			workspace.NodeStatus{UnreadCount: zero, InboundCount: zero, OutboundCount: one},
			true, false, true,
			[]string{"External decisions are pending in channel.pending"},
			[]string{"Sent requests await responses"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resultGuidance(tc.self, tc.operation, tc.success, tc.claimed, tc.existing, tc.pending, false)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("guidance %q lacks %q", got, want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(got, absent) {
					t.Errorf("guidance %q unexpectedly contains %q", got, absent)
				}
			}
		})
	}
}

func TestResultGuidanceExternalMore(t *testing.T) {
	zero, one := resultCount(0), resultCount(1)
	for _, tc := range []struct {
		name    string
		self    workspace.NodeStatus
		success bool
		want    []string
		absent  []string
	}{
		{
			"bounded follow-up with zero own work",
			workspace.NodeStatus{UnreadCount: zero, InboundCount: zero, OutboundCount: zero},
			true,
			[]string{"channel.more", "After handling accepted mail", "tmux-a2a-postman channel-sync"},
			[]string{"tmux-a2a-postman pop", "Sent requests await responses"},
		},
		{
			"own unread before another scan",
			workspace.NodeStatus{UnreadCount: one, InboundCount: one, OutboundCount: one},
			true,
			[]string{"tmux-a2a-postman pop", "channel.more", "tmux-a2a-postman channel-sync"},
			[]string{"Sent requests await responses"},
		},
		{
			"partial failure does not demand replay",
			workspace.NodeStatus{UnreadCount: zero, InboundCount: zero, OutboundCount: one},
			false,
			nil,
			[]string{"tmux-a2a-postman channel-sync", "Sent requests await responses"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := resultGuidance(tc.self, "channel-sync", tc.success, false, "", false, true)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("guidance %q lacks %q", got, want)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(got, absent) {
					t.Errorf("guidance %q unexpectedly contains %q", got, absent)
				}
			}
		})
	}
}
