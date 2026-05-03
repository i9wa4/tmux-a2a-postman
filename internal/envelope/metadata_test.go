package envelope

import "testing"

func TestResolveReplyPolicyFromMetadata(t *testing.T) {
	tests := []struct {
		name string
		meta Metadata
		want string
	}{
		{
			name: "default none",
			meta: Metadata{From: "orchestrator", To: "worker", Body: "plain update"},
			want: "none",
		},
		{
			name: "explicit required",
			meta: Metadata{From: "orchestrator", To: "worker", ReplyPolicy: "required", Body: "please review"},
			want: "required",
		},
		{
			name: "status request required",
			meta: Metadata{From: "orchestrator", To: "worker", MessageType: "status_request", Body: "status?"},
			want: "required",
		},
		{
			name: "status update none",
			meta: Metadata{From: "orchestrator", To: "worker", MessageType: "status_update", Body: "current task: done"},
			want: "none",
		},
		{
			name: "heartbeat ok terminal none",
			meta: Metadata{From: "worker", To: "orchestrator", Body: "HEARTBEAT_OK\nready"},
			want: "none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveReplyPolicyFromMetadata(tt.meta); got != tt.want {
				t.Fatalf("ResolveReplyPolicyFromMetadata() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveReplyPolicyForSend(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		noReply       bool
		replyRequired bool
		want          string
	}{
		{
			name: "default none",
			body: "plain update",
			want: "none",
		},
		{
			name:          "explicit required",
			body:          "please review",
			replyRequired: true,
			want:          "required",
		},
		{
			name:    "explicit no reply",
			body:    "please review",
			noReply: true,
			want:    "none",
		},
		{
			name: "heartbeat ok terminal",
			body: "HEARTBEAT_OK",
			want: "none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveReplyPolicyForSend(tt.body, tt.noReply, tt.replyRequired)
			if got != tt.want {
				t.Fatalf("ResolveReplyPolicyForSend() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveReplyPolicyFromContentWithoutMetadataDefaultsToNone(t *testing.T) {
	if got := ResolveReplyPolicyFromContent("plain body"); got != "none" {
		t.Fatalf("ResolveReplyPolicyFromContent() = %q, want none", got)
	}
}

func TestParseMetadataAcceptsReplyObligationAlias(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  reply_obligation: required\n  timestamp: 2026-05-03T09:00:00Z\n---\n\nplease review\n"

	got, err := ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if got.ReplyPolicy != "required" {
		t.Fatalf("ReplyPolicy = %q, want required", got.ReplyPolicy)
	}
	if got.Timestamp != "2026-05-03T09:00:00Z" {
		t.Fatalf("Timestamp = %q, want timestamp", got.Timestamp)
	}
}
