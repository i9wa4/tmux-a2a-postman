package envelope

import (
	"strings"
	"testing"
)

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

func TestParseMetadataAcceptsExactReplySlotFields(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  messageId: m1.md\n  replyPolicy: required\n  reply_slot_id: rslot_123\n  fills_reply_slot_id: rslot_prev\n  reply_set_id: rset_1\n  branch_id: branch_1\n  completion_rule: all\n---\n\nplease review\n"

	got, err := ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if got.ReplySlotID != "rslot_123" {
		t.Fatalf("ReplySlotID = %q, want rslot_123", got.ReplySlotID)
	}
	if got.FillsReplySlotID != "rslot_prev" {
		t.Fatalf("FillsReplySlotID = %q, want rslot_prev", got.FillsReplySlotID)
	}
	if got.ReplySetID != "rset_1" {
		t.Fatalf("ReplySetID = %q, want rset_1", got.ReplySetID)
	}
	if got.ObligationID != "rslot_123" || got.SatisfiesObligationID != "rslot_prev" || got.ObligationGroupID != "rset_1" {
		t.Fatalf("legacy aliases = %q/%q/%q, want rslot_123/rslot_prev/rset_1", got.ObligationID, got.SatisfiesObligationID, got.ObligationGroupID)
	}
	if got.BranchID != "branch_1" {
		t.Fatalf("BranchID = %q, want branch_1", got.BranchID)
	}
	if got.CompletionRule != "all" {
		t.Fatalf("CompletionRule = %q, want all", got.CompletionRule)
	}
}

func TestParseMetadataAcceptsLegacyExactObligationFields(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  messageId: m1.md\n  replyPolicy: required\n  obligation_id: obl_123\n  satisfies_obligation_id: obl_prev\n  obligation_group_id: group_1\n  branch_id: branch_1\n  completion_rule: all\n---\n\nplease review\n"

	got, err := ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if got.ReplySlotID != "obl_123" {
		t.Fatalf("ReplySlotID = %q, want obl_123", got.ReplySlotID)
	}
	if got.FillsReplySlotID != "obl_prev" {
		t.Fatalf("FillsReplySlotID = %q, want obl_prev", got.FillsReplySlotID)
	}
	if got.ReplySetID != "group_1" {
		t.Fatalf("ReplySetID = %q, want group_1", got.ReplySetID)
	}
	if got.ObligationID != "obl_123" {
		t.Fatalf("ObligationID = %q, want obl_123", got.ObligationID)
	}
	if got.SatisfiesObligationID != "obl_prev" {
		t.Fatalf("SatisfiesObligationID = %q, want obl_prev", got.SatisfiesObligationID)
	}
	if got.ObligationGroupID != "group_1" {
		t.Fatalf("ObligationGroupID = %q, want group_1", got.ObligationGroupID)
	}
	if got.BranchID != "branch_1" {
		t.Fatalf("BranchID = %q, want branch_1", got.BranchID)
	}
	if got.CompletionRule != "all" {
		t.Fatalf("CompletionRule = %q, want all", got.CompletionRule)
	}
}

func TestParseMetadataRejectsConflictingReplySlotAliases(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  reply_slot_id: rslot_123\n  obligation_id: obl_123\n---\n\nplease review\n"

	_, err := ParseMetadata(content)
	if err == nil {
		t.Fatal("ParseMetadata() error = nil, want conflict")
	}
	if !strings.Contains(err.Error(), "conflicting reply_slot_id aliases") {
		t.Fatalf("ParseMetadata() error = %v, want reply_slot_id conflict", err)
	}
}

func TestParseMetadataDoesNotAcceptDecorativeObligationAliases(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  obligationId: obl_123\n  satisfiesObligationId: obl_prev\n---\n\nplease review\n"

	got, err := ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if got.ObligationID != "" {
		t.Fatalf("ObligationID = %q, want empty", got.ObligationID)
	}
	if got.SatisfiesObligationID != "" {
		t.Fatalf("SatisfiesObligationID = %q, want empty", got.SatisfiesObligationID)
	}
}

func TestParseMetadataIgnoresNestedParamsFields(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  audit:\n    replyPolicy: required\n    messageType: status_request\n---\n\nplain update\n"

	got, err := ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if got.ReplyPolicy != "" {
		t.Fatalf("ReplyPolicy = %q, want empty", got.ReplyPolicy)
	}
	if got.MessageType != "" {
		t.Fatalf("MessageType = %q, want empty", got.MessageType)
	}
}

func TestParseMetadataAcceptsWiderDirectParamsIndent(t *testing.T) {
	content := "---\nparams:\n    from: orchestrator\n    to: worker\n    replyPolicy: required\n    messageType: status_request\n    audit:\n        replyPolicy: none\n---\n\nplain update\n"

	got, err := ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if got.From != "orchestrator" || got.To != "worker" {
		t.Fatalf("from/to = %q/%q, want orchestrator/worker", got.From, got.To)
	}
	if got.ReplyPolicy != "required" {
		t.Fatalf("ReplyPolicy = %q, want required", got.ReplyPolicy)
	}
	if got.MessageType != "status_request" {
		t.Fatalf("MessageType = %q, want status_request", got.MessageType)
	}
}

func TestParseMetadataReplyPolicyAliasesUseLastWins(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "reply obligation after reply policy",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: none\n  reply_obligation: required\n---\n\nbody\n",
			want:    "required",
		},
		{
			name:    "reply policy after reply obligation",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  reply_obligation: required\n  replyPolicy: none\n---\n\nbody\n",
			want:    "none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMetadata(tt.content)
			if err != nil {
				t.Fatalf("ParseMetadata() error = %v", err)
			}
			if got.ReplyPolicy != tt.want {
				t.Fatalf("ReplyPolicy = %q, want %q", got.ReplyPolicy, tt.want)
			}
		})
	}
}

func TestEnsureParamsUpdatesManagedFields(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  messageId: old.md\n  replyPolicy: none\n---\n\nplease review\n"

	got := EnsureParams(content, map[string]string{
		"messageId":   "new.md",
		"replyPolicy": "required",
	})

	for _, want := range []string{
		"messageId: new.md",
		"replyPolicy: required",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("EnsureParams() missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"messageId: old.md",
		"replyPolicy: none",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("EnsureParams() kept %q:\n%s", unwanted, got)
		}
	}
}

func TestEnsureParamsInsertsExactReplySlotFields(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n---\n\nplease review\n"

	got := EnsureParams(content, map[string]string{
		"messageId":           "m1.md",
		"replyPolicy":         "required",
		"reply_slot_id":       "rslot_123",
		"fills_reply_slot_id": "rslot_prev",
		"reply_set_id":        "rset_1",
		"branch_id":           "branch_1",
		"completion_rule":     "all",
	})

	for _, want := range []string{
		"messageId: m1.md",
		"replyPolicy: required",
		"reply_slot_id: rslot_123",
		"fills_reply_slot_id: rslot_prev",
		"reply_set_id: rset_1",
		"branch_id: branch_1",
		"completion_rule: all",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("EnsureParams() missing %q:\n%s", want, got)
		}
	}
}

func TestEnsureParamsPreservesLegacyReplySlotAliasKeys(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  obligation_id: old\n---\n\nplease review\n"

	got := EnsureParams(content, map[string]string{
		"reply_slot_id": "rslot_123",
	})

	if !strings.Contains(got, "obligation_id: rslot_123") {
		t.Fatalf("EnsureParams() did not update legacy alias key:\n%s", got)
	}
	if strings.Contains(got, "reply_slot_id: rslot_123") {
		t.Fatalf("EnsureParams() inserted duplicate canonical key:\n%s", got)
	}
}

func TestEnsureParamsPreservesWiderDirectParamsIndent(t *testing.T) {
	content := "---\nparams:\n    from: orchestrator\n    to: worker\n    messageId: old.md\n    audit:\n        replyPolicy: display-only\n---\n\nplease review\n"

	got := EnsureParams(content, map[string]string{
		"messageId":   "new.md",
		"replyPolicy": "required",
	})

	for _, want := range []string{
		"    messageId: new.md",
		"    replyPolicy: required",
		"    audit:\n        replyPolicy: display-only",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("EnsureParams() missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"\n  messageId: new.md",
		"\n  replyPolicy: required",
		"messageId: old.md",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("EnsureParams() contains unwanted %q:\n%s", unwanted, got)
		}
	}
}

func TestValidateObligationToken(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "opaque token", value: "obl_0123456789abcdef"},
		{name: "empty", wantErr: true},
		{name: "leading whitespace", value: " obl_123", wantErr: true},
		{name: "internal whitespace", value: "obl 123", wantErr: true},
		{name: "path separator", value: "obl/123", wantErr: true},
		{name: "windows path separator", value: "obl\\123", wantErr: true},
		{name: "control character", value: "obl_\n123", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateObligationToken(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateObligationToken(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
			}
		})
	}
}

func TestEnsureParamsUpdatesOnlyParamsBlock(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  audit:\n    replyPolicy: display-only\naudit:\n  replyPolicy: display-only\n---\n\nplease review\n"

	got := EnsureParams(content, map[string]string{
		"replyPolicy": "required",
	})

	if !strings.Contains(got, "params:\n  replyPolicy: required\n  from: orchestrator") {
		t.Fatalf("EnsureParams() did not insert params replyPolicy:\n%s", got)
	}
	for _, want := range []string{
		"  audit:\n    replyPolicy: display-only",
		"audit:\n  replyPolicy: display-only",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("EnsureParams() rewrote audit block %q:\n%s", want, got)
		}
	}
}

func TestParamsReplyPolicyUsesPlaceholder(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "params placeholder with extra space",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy:  {reply_policy}\n---\n\nbody\n",
			want:    true,
		},
		{
			name:    "params alias placeholder",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  reply_obligation: {reply_policy}\n---\n\nbody\n",
			want:    true,
		},
		{
			name:    "wider direct params placeholder",
			content: "---\nparams:\n    from: orchestrator\n    to: worker\n    replyPolicy: {reply_policy}\n---\n\nbody\n",
			want:    true,
		},
		{
			name:    "body placeholder does not count",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: none\n---\n\nreplyPolicy: {reply_policy}\n",
			want:    false,
		},
		{
			name:    "other frontmatter block placeholder does not count",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\naudit:\n  replyPolicy: {reply_policy}\n---\n\nbody\n",
			want:    false,
		},
		{
			name:    "nested params placeholder does not count",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  audit:\n    replyPolicy: {reply_policy}\n---\n\nbody\n",
			want:    false,
		},
		{
			name:    "explicit policy overrides placeholder alias",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: none\n  reply_obligation: {reply_policy}\n---\n\nbody\n",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParamsReplyPolicyUsesPlaceholder(tt.content); got != tt.want {
				t.Fatalf("ParamsReplyPolicyUsesPlaceholder() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExplicitParamsReplyPolicy(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
		wantOK  bool
	}{
		{
			name:    "direct explicit",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: required\n---\n\nbody\n",
			want:    "required",
			wantOK:  true,
		},
		{
			name:    "placeholder is not explicit",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: {reply_policy}\n---\n\nbody\n",
		},
		{
			name:    "nested explicit is ignored",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  audit:\n    replyPolicy: required\n---\n\nbody\n",
		},
		{
			name:    "explicit wins over placeholder alias",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: required\n  reply_obligation: {reply_policy}\n---\n\nbody\n",
			want:    "required",
			wantOK:  true,
		},
		{
			name:    "last explicit alias wins over earlier explicit",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: none\n  reply_obligation: required\n---\n\nbody\n",
			want:    "required",
			wantOK:  true,
		},
		{
			name:    "last explicit reply policy wins over earlier alias",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  reply_obligation: required\n  replyPolicy: none\n---\n\nbody\n",
			want:    "none",
			wantOK:  true,
		},
		{
			name:    "wider direct explicit",
			content: "---\nparams:\n    from: orchestrator\n    to: worker\n    replyPolicy: required\n---\n\nbody\n",
			want:    "required",
			wantOK:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ExplicitParamsReplyPolicy(tt.content)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("ExplicitParamsReplyPolicy() = %q, %v; want %q, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestExplicitParamsReplyPolicyIgnoringGenerated(t *testing.T) {
	generated := "__generated_reply_policy__"
	tests := []struct {
		name    string
		content string
		want    string
		wantOK  bool
	}{
		{
			name:    "explicit value wins over generated alias",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: required\n  reply_obligation: __generated_reply_policy__\n---\n\nbody\n",
			want:    "required",
			wantOK:  true,
		},
		{
			name:    "expanded-only field is explicit",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: __generated_reply_policy__\n  reply_obligation: none\n---\n\nbody\n",
			want:    "none",
			wantOK:  true,
		},
		{
			name:    "last expanded explicit alias wins",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: required\n  replyPolicy: none\n  reply_obligation: required\n---\n\nbody\n",
			want:    "required",
			wantOK:  true,
		},
		{
			name:    "generated only is not explicit",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: __generated_reply_policy__\n---\n\nbody\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ExplicitParamsReplyPolicyIgnoringGenerated(tt.content, generated)
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("ExplicitParamsReplyPolicyIgnoringGenerated() = %q, %v; want %q, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
