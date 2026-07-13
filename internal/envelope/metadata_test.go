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

func TestParseMetadataAcceptsReplyPolicy(t *testing.T) {
	content := "---\nparams:\n  contextId: ctx-replay\n  from: orchestrator\n  to: worker\n  reply_policy: required\n  timestamp: 2026-05-03T09:00:00Z\n---\n\nplease review\n"

	got, err := ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if got.ContextID != "ctx-replay" {
		t.Fatalf("ContextID = %q, want ctx-replay", got.ContextID)
	}
	if got.ReplyPolicy != "required" {
		t.Fatalf("ReplyPolicy = %q, want required", got.ReplyPolicy)
	}
	if got.Timestamp != "2026-05-03T09:00:00Z" {
		t.Fatalf("Timestamp = %q, want timestamp", got.Timestamp)
	}
}

func TestParseMetadataAcceptsExactInputRequestFields(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  messageId: m1.md\n  replyPolicy: required\n  input_request_id: ireq_123\n  fills_input_request_id: ireq_prev\n  input_request_set_id: ireqset_1\n  branch_id: branch_1\n  completion_rule: all\n---\n\nplease review\n"

	got, err := ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if got.InputRequestID != "ireq_123" {
		t.Fatalf("InputRequestID = %q, want ireq_123", got.InputRequestID)
	}
	if got.FillsInputRequestID != "ireq_prev" {
		t.Fatalf("FillsInputRequestID = %q, want ireq_prev", got.FillsInputRequestID)
	}
	if got.InputRequestSetID != "ireqset_1" {
		t.Fatalf("InputRequestSetID = %q, want ireqset_1", got.InputRequestSetID)
	}
	if got.BranchID != "branch_1" {
		t.Fatalf("BranchID = %q, want branch_1", got.BranchID)
	}
	if got.CompletionRule != "all" {
		t.Fatalf("CompletionRule = %q, want all", got.CompletionRule)
	}
}

func TestParseMetadataAcceptsVerdictFields(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  messageId: m1.md\n  verdict: pass\n  verdictOf: ireq_123\n---\n\nlgtm\n"

	got, err := ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if got.Verdict != "pass" {
		t.Fatalf("Verdict = %q, want pass", got.Verdict)
	}
	if got.VerdictOf != "ireq_123" {
		t.Fatalf("VerdictOf = %q, want ireq_123", got.VerdictOf)
	}
}

func TestParseMetadataAcceptsSnakeCaseVerdictOf(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  messageId: m1.md\n  verdict: fail\n  verdict_of: ireq_456\n---\n\nnot yet\n"

	got, err := ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if got.Verdict != "fail" {
		t.Fatalf("Verdict = %q, want fail", got.Verdict)
	}
	if got.VerdictOf != "ireq_456" {
		t.Fatalf("VerdictOf = %q, want ireq_456", got.VerdictOf)
	}
}

func TestParseMetadataAcceptsExternalTaskRunFields(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  messageId: m1.md\n  task_id: TASK-123\n  run_id: run-20260617-01\n---\n\nplease work\n"

	got, err := ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if got.TaskID != "TASK-123" {
		t.Fatalf("TaskID = %q, want TASK-123", got.TaskID)
	}
	if got.RunID != "run-20260617-01" {
		t.Fatalf("RunID = %q, want run-20260617-01", got.RunID)
	}
}

func TestParseMetadataIgnoresLegacyReplyIdentityFields(t *testing.T) {
	inputRequestAlias := "obligation" + "_id"
	fillsAlias := "satisfies" + "_obligation" + "_id"
	setAlias := "obligation" + "_group" + "_id"
	content := "---\nparams:\n" +
		"  from: orchestrator\n" +
		"  to: worker\n" +
		"  messageId: m1.md\n" +
		"  replyPolicy: required\n" +
		"  " + inputRequestAlias + ": ireq_legacy\n" +
		"  " + fillsAlias + ": ireq_prev\n" +
		"  " + setAlias + ": ireqset_1\n" +
		"  branch_id: branch_1\n" +
		"  completion_rule: all\n" +
		"---\n\nplease review\n"

	got, err := ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if got.InputRequestID != "" {
		t.Fatalf("InputRequestID = %q, want empty", got.InputRequestID)
	}
	if got.FillsInputRequestID != "" {
		t.Fatalf("FillsInputRequestID = %q, want empty", got.FillsInputRequestID)
	}
	if got.InputRequestSetID != "" {
		t.Fatalf("InputRequestSetID = %q, want empty", got.InputRequestSetID)
	}
	if got.BranchID != "branch_1" {
		t.Fatalf("BranchID = %q, want branch_1", got.BranchID)
	}
	if got.CompletionRule != "all" {
		t.Fatalf("CompletionRule = %q, want all", got.CompletionRule)
	}
}

func TestParseMetadataKeepsCanonicalInputRequestWhenLegacyIdentityFieldPresent(t *testing.T) {
	legacyAlias := "reply" + "_request" + "_id"
	content := "---\nparams:\n" +
		"  from: orchestrator\n" +
		"  to: worker\n" +
		"  input_request_id: ireq_123\n" +
		"  " + legacyAlias + ": ireq_legacy\n" +
		"---\n\nplease review\n"

	got, err := ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if got.InputRequestID != "ireq_123" {
		t.Fatalf("InputRequestID = %q, want ireq_123", got.InputRequestID)
	}
}

func TestParseMetadataDoesNotAcceptDecorativeReplyIdentityAliases(t *testing.T) {
	content := "---\nparams:\n" +
		"  from: orchestrator\n" +
		"  to: worker\n" +
		"  inputRequestId: ireq_123\n" +
		"  fillsInputRequestId: ireq_prev\n" +
		"---\n\nplease review\n"

	got, err := ParseMetadata(content)
	if err != nil {
		t.Fatalf("ParseMetadata() error = %v", err)
	}
	if got.InputRequestID != "" {
		t.Fatalf("InputRequestID = %q, want empty", got.InputRequestID)
	}
	if got.FillsInputRequestID != "" {
		t.Fatalf("FillsInputRequestID = %q, want empty", got.FillsInputRequestID)
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

func TestParseMetadataReplyPolicyDuplicatesUseLastWins(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "second reply policy wins",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: none\n  reply_policy: required\n---\n\nbody\n",
			want:    "required",
		},
		{
			name:    "last reply policy wins",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  reply_policy: required\n  replyPolicy: none\n---\n\nbody\n",
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

func TestScanFrontmatterPreservesCurrentDelimiterSemantics(t *testing.T) {
	tests := []struct {
		name            string
		content         string
		wantFrontmatter string
		wantBody        string
		wantOK          bool
		wantErr         string
	}{
		{
			name:            "standard envelope",
			content:         "---\nparams:\n  from: orchestrator\n  to: worker\n---\n\nbody\n",
			wantFrontmatter: "params:\n  from: orchestrator\n  to: worker",
			wantBody:        "\n\nbody\n",
			wantOK:          true,
		},
		{
			name:            "body delimiters remain body content",
			content:         "---\nparams:\n  from: orchestrator\n  to: worker\n---\n\nbody\n---\nnot frontmatter\n",
			wantFrontmatter: "params:\n  from: orchestrator\n  to: worker",
			wantBody:        "\n\nbody\n---\nnot frontmatter\n",
			wantOK:          true,
		},
		{
			name:            "leading text before first delimiter is ignored",
			content:         "prefix\n---\nparams:\n  from: orchestrator\n  to: worker\n---\n\nbody\n",
			wantFrontmatter: "params:\n  from: orchestrator\n  to: worker",
			wantBody:        "\n\nbody\n",
			wantOK:          true,
		},
		{
			name:            "leading bom before first delimiter is ignored",
			content:         "\ufeff---\nparams:\n  from: orchestrator\n  to: worker\n---\n\nbody\n",
			wantFrontmatter: "params:\n  from: orchestrator\n  to: worker",
			wantBody:        "\n\nbody\n",
			wantOK:          true,
		},
		{
			name:    "crlf opening delimiter does not match current scanner",
			content: "---\r\nparams:\r\n  from: orchestrator\r\n  to: worker\r\n---\r\nbody\r\n",
		},
		{
			name:    "bom before crlf opening delimiter does not match current scanner",
			content: "\ufeff---\r\nparams:\r\n  from: orchestrator\r\n  to: worker\r\n---\r\nbody\r\n",
		},
		{
			name:    "missing frontmatter",
			content: "plain body\n",
		},
		{
			name:    "unclosed frontmatter",
			content: "---\nparams:\n  from: orchestrator\n",
			wantErr: "frontmatter not closed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFrontmatter, gotBody, gotOK, err := ScanFrontmatter(tt.content)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("ScanFrontmatter() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ScanFrontmatter() error = %v", err)
			}
			if gotOK != tt.wantOK {
				t.Fatalf("ScanFrontmatter() ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotFrontmatter != tt.wantFrontmatter {
				t.Fatalf("frontmatter = %q, want %q", gotFrontmatter, tt.wantFrontmatter)
			}
			if gotBody != tt.wantBody {
				t.Fatalf("body = %q, want %q", gotBody, tt.wantBody)
			}
		})
	}
}

func TestDecodeEnvelopeMetadataPreservesParamsSemantics(t *testing.T) {
	frontmatter := "params:\n" +
		"  from: orchestrator\n" +
		"  to: worker\n" +
		"  replyPolicy: none\n" +
		"  reply_policy: required\n" +
		"  blocked_reason: \"value: with colon\"\n" +
		"  blocked_scope: >\n" +
		"    generated\n" +
		"    value\n" +
		"  audit:\n" +
		"    replyPolicy: none\n"

	got, err := DecodeEnvelopeMetadata(frontmatter, "\n\nbody\n")
	if err != nil {
		t.Fatalf("DecodeEnvelopeMetadata() error = %v", err)
	}
	if got.From != "orchestrator" || got.To != "worker" {
		t.Fatalf("from/to = %q/%q, want orchestrator/worker", got.From, got.To)
	}
	if got.ReplyPolicy != "required" {
		t.Fatalf("ReplyPolicy = %q, want required", got.ReplyPolicy)
	}
	if got.BlockedReason != "\"value: with colon\"" {
		t.Fatalf("BlockedReason = %q, want quoted colon value", got.BlockedReason)
	}
	if got.BlockedScope != ">" {
		t.Fatalf("BlockedScope = %q, want current folded marker value", got.BlockedScope)
	}
	if got.Body != "body" {
		t.Fatalf("Body = %q, want body", got.Body)
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

func TestEnsureParamsInsertsExactInputRequestFields(t *testing.T) {
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n---\n\nplease review\n"

	got := EnsureParams(content, map[string]string{
		"messageId":              "m1.md",
		"replyPolicy":            "required",
		"input_request_id":       "ireq_123",
		"fills_input_request_id": "ireq_prev",
		"input_request_set_id":   "ireqset_1",
		"branch_id":              "branch_1",
		"completion_rule":        "all",
	})

	for _, want := range []string{
		"messageId: m1.md",
		"replyPolicy: required",
		"input_request_id: ireq_123",
		"fills_input_request_id: ireq_prev",
		"input_request_set_id: ireqset_1",
		"branch_id: branch_1",
		"completion_rule: all",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("EnsureParams() missing %q:\n%s", want, got)
		}
	}
}

func TestEnsureParamsInsertsCanonicalInputRequestWhenLegacyIdentityFieldExists(t *testing.T) {
	legacyAlias := "obligation" + "_id"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  " + legacyAlias + ": old\n---\n\nplease review\n"

	got := EnsureParams(content, map[string]string{
		"input_request_id": "ireq_123",
	})

	if !strings.Contains(got, "input_request_id: ireq_123") {
		t.Fatalf("EnsureParams() did not insert canonical key:\n%s", got)
	}
	if !strings.Contains(got, legacyAlias+": old") {
		t.Fatalf("EnsureParams() unexpectedly rewrote legacy field:\n%s", got)
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

func TestValidateInputRequestToken(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "opaque token", value: "ireq_0123456789abcdef"},
		{name: "empty", wantErr: true},
		{name: "leading whitespace", value: " ireq_123", wantErr: true},
		{name: "internal whitespace", value: "rslot 123", wantErr: true},
		{name: "path separator", value: "rslot/123", wantErr: true},
		{name: "windows path separator", value: "rslot\\123", wantErr: true},
		{name: "control character", value: "ireq_\n123", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInputRequestToken(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateInputRequestToken(%q) error = %v, wantErr %v", tt.value, err, tt.wantErr)
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
			name:    "params snake_case placeholder",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  reply_policy: {reply_policy}\n---\n\nbody\n",
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
			name:    "explicit policy overrides snake_case placeholder",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: none\n  reply_policy: {reply_policy}\n---\n\nbody\n",
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
			name:    "explicit wins over snake_case placeholder",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: required\n  reply_policy: {reply_policy}\n---\n\nbody\n",
			want:    "required",
			wantOK:  true,
		},
		{
			name:    "last explicit snake_case wins over earlier explicit",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: none\n  reply_policy: required\n---\n\nbody\n",
			want:    "required",
			wantOK:  true,
		},
		{
			name:    "last explicit reply policy wins over earlier snake_case",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  reply_policy: required\n  replyPolicy: none\n---\n\nbody\n",
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
			name:    "explicit value wins over generated snake_case",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: required\n  reply_policy: __generated_reply_policy__\n---\n\nbody\n",
			want:    "required",
			wantOK:  true,
		},
		{
			name:    "expanded-only field is explicit",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: __generated_reply_policy__\n  reply_policy: none\n---\n\nbody\n",
			want:    "none",
			wantOK:  true,
		},
		{
			name:    "last expanded explicit snake_case wins",
			content: "---\nparams:\n  from: orchestrator\n  to: worker\n  replyPolicy: required\n  replyPolicy: none\n  reply_policy: required\n---\n\nbody\n",
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
