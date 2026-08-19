package envelope

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

type Metadata struct {
	ContextID                string
	From                     string
	To                       string
	MessageID                string
	ReplyPolicy              string
	ReplyTo                  string
	MessageType              string
	Timestamp                string
	ThreadID                 string
	TaskID                   string
	RunID                    string
	InputRequestID           string
	FillsInputRequestID      string
	CommandHash              string
	InputRequestSetID        string
	Verdict                  string
	VerdictOf                string
	EvidenceCommand          string
	EvidenceCWD              string
	EvidenceEnvAllowlist     string
	EvidenceTimeoutSeconds   string
	EvidenceSideEffectClass  string
	EvidenceArtifact         string
	EvidenceHash             string
	BranchID                 string
	CompletionRule           string
	RuntimeContextID         string
	RuntimeContextScope      string
	RuntimeContextCapturedAt string
	RuntimeContextHash       string
	BlockedReportID          string
	BlockedScope             string
	BlockedScopeID           string
	BlockedReason            string
	Body                     string
}

const SenderBodyBoundarySentinel = "<!-- tmux-a2a-postman:sender-body-boundary -->"

func SenderBodyBoundaryForMessageID(messageID string) string {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return ""
	}
	return "<!-- tmux-a2a-postman:sender-body-boundary:" + messageID + " -->"
}

type frontmatterScan struct {
	frontmatter      string
	body             string
	frontmatterStart int
	closeStart       int
}

func ScanFrontmatter(content string) (frontmatter, body string, ok bool, err error) {
	scan, ok, err := scanFrontmatter(content)
	if !ok || err != nil {
		return "", "", ok, err
	}
	return scan.frontmatter, scan.body, true, nil
}

func scanFrontmatter(content string) (frontmatterScan, bool, error) {
	first := strings.Index(content, "---\n")
	if first < 0 {
		return frontmatterScan{}, false, nil
	}
	frontmatterStart := first + 4
	rest := content[frontmatterStart:]
	second := strings.Index(rest, "\n---")
	if second < 0 {
		return frontmatterScan{}, false, fmt.Errorf("frontmatter not closed")
	}
	closeStart := frontmatterStart + second
	return frontmatterScan{
		frontmatter:      content[frontmatterStart:closeStart],
		body:             content[closeStart+4:],
		frontmatterStart: frontmatterStart,
		closeStart:       closeStart,
	}, true, nil
}

func BodyFromContent(content string) string {
	body, ok := rawBodyFromContent(content)
	if !ok {
		return strings.TrimSpace(content)
	}
	return strings.TrimSpace(body)
}

func SenderBodyFromContent(content string) (string, bool) {
	frontmatter, body, ok, err := ScanFrontmatter(content)
	if !ok || err != nil {
		return strings.TrimSpace(content), false
	}
	metadata, err := DecodeEnvelopeMetadata(frontmatter, body)
	if err != nil {
		return strings.TrimSpace(body), false
	}
	boundary := SenderBodyBoundaryForMessageID(metadata.MessageID)
	if senderBody, ok := senderBodyAfterGeneratedEnvelopeSeparator(body, boundary); ok {
		return senderBody, true
	}
	return strings.TrimSpace(body), false
}

func SenderBodyFromTrustedContent(content, trustedMessageID string) (string, bool) {
	_, body, ok, err := ScanFrontmatter(content)
	if !ok || err != nil {
		return strings.TrimSpace(content), true
	}
	boundary := SenderBodyBoundaryForMessageID(trustedMessageID)
	if senderBody, ok := senderBodyAfterGeneratedEnvelopeSeparator(body, boundary); ok {
		return senderBody, true
	}
	if senderBody, ok := senderBodyAfterLegacyGeneratedEnvelopeSeparator(body); ok {
		return senderBody, true
	}
	return strings.TrimSpace(body), true
}

func rawBodyFromContent(content string) (string, bool) {
	_, body, ok, err := ScanFrontmatter(content)
	if err != nil {
		return "", false
	}
	return body, ok
}

func senderBodyAfterGeneratedEnvelopeSeparator(body, boundary string) (string, bool) {
	if boundary == "" {
		return "", false
	}
	offset := 0
	previousLine := ""
	for offset <= len(body) {
		lineEnd := len(body)
		newlineEnd := len(body)
		if idx := strings.IndexByte(body[offset:], '\n'); idx >= 0 {
			lineEnd = offset + idx
			newlineEnd = lineEnd + 1
		}
		line := strings.TrimRight(body[offset:lineEnd], "\r")
		if strings.TrimSpace(line) == "---" && previousLine == boundary {
			senderBody := body[newlineEnd:]
			if strings.HasPrefix(senderBody, "\r\n") {
				senderBody = senderBody[2:]
			} else if strings.HasPrefix(senderBody, "\n") {
				senderBody = senderBody[1:]
			}
			return senderBody, true
		}
		previousLine = strings.TrimSpace(line)
		if newlineEnd == len(body) {
			break
		}
		offset = newlineEnd
	}
	return "", false
}

func senderBodyAfterLegacyGeneratedEnvelopeSeparator(body string) (string, bool) {
	messageHeadingSeen := false
	senderHeadingSeen := false
	offset := 0
	for offset <= len(body) {
		lineEnd := len(body)
		newlineEnd := len(body)
		if idx := strings.IndexByte(body[offset:], '\n'); idx >= 0 {
			lineEnd = offset + idx
			newlineEnd = lineEnd + 1
		}
		line := strings.TrimSpace(strings.TrimRight(body[offset:lineEnd], "\r"))
		switch line {
		case "# Message":
			messageHeadingSeen = true
		case "## Sender Message":
			if messageHeadingSeen {
				senderHeadingSeen = true
			}
		case "---":
			if senderHeadingSeen {
				senderBody := body[newlineEnd:]
				if strings.HasPrefix(senderBody, "\r\n") {
					senderBody = senderBody[2:]
				} else if strings.HasPrefix(senderBody, "\n") {
					senderBody = senderBody[1:]
				}
				return senderBody, true
			}
		}
		if newlineEnd == len(body) {
			break
		}
		offset = newlineEnd
	}
	return "", false
}

func ParseMetadata(content string) (Metadata, error) {
	frontmatter, body, ok, err := ScanFrontmatter(content)
	if err != nil {
		return Metadata{}, err
	}
	if !ok {
		return Metadata{}, fmt.Errorf("no frontmatter block found")
	}
	return DecodeEnvelopeMetadata(frontmatter, body)
}

func DecodeEnvelopeMetadata(frontmatter, body string) (Metadata, error) {
	metadata := Metadata{Body: strings.TrimSpace(body)}
	lines := strings.Split(frontmatter, "\n")
	paramsIndex, paramsEnd := paramsBlockRange(lines)
	if paramsIndex >= 0 {
		childIndent := paramsChildIndent(lines, paramsIndex, paramsEnd)
		for idx := paramsIndex + 1; idx < paramsEnd; idx++ {
			key, value, ok := directParamsChild(lines[idx], childIndent)
			if !ok {
				continue
			}
			switch key {
			case "contextId", "context_id":
				metadata.ContextID = value
			case "from":
				metadata.From = value
			case "to":
				metadata.To = value
			case "messageId", "message_id":
				metadata.MessageID = value
			case "replyPolicy", "reply_policy":
				metadata.ReplyPolicy = value
			case "replyTo", "reply_to":
				metadata.ReplyTo = value
			case "messageType", "message_type":
				metadata.MessageType = value
			case "timestamp":
				metadata.Timestamp = value
			case "thread_id":
				metadata.ThreadID = value
			case "task_id":
				metadata.TaskID = value
			case "run_id":
				metadata.RunID = value
			case "input_request_id":
				metadata.InputRequestID = value
			case "fills_input_request_id":
				metadata.FillsInputRequestID = value
			case "command_hash":
				metadata.CommandHash = value
			case "input_request_set_id":
				metadata.InputRequestSetID = value
			case "verdict":
				metadata.Verdict = value
			case "verdictOf", "verdict_of":
				metadata.VerdictOf = value
			case "evidence_command":
				metadata.EvidenceCommand = decodeEvidenceParamValue(value)
			case "evidence_cwd":
				metadata.EvidenceCWD = decodeEvidenceParamValue(value)
			case "evidence_env_allowlist":
				metadata.EvidenceEnvAllowlist = decodeEvidenceParamValue(value)
			case "evidence_timeout_seconds":
				metadata.EvidenceTimeoutSeconds = decodeEvidenceParamValue(value)
			case "evidence_side_effect_class":
				metadata.EvidenceSideEffectClass = decodeEvidenceParamValue(value)
			case "evidence_artifact":
				metadata.EvidenceArtifact = decodeEvidenceParamValue(value)
			case "evidence_hash":
				metadata.EvidenceHash = decodeEvidenceParamValue(value)
			case "branch_id":
				metadata.BranchID = value
			case "completion_rule":
				metadata.CompletionRule = value
			case "runtimeContextId", "runtime_context_id":
				metadata.RuntimeContextID = value
			case "runtimeContextScope", "runtime_context_scope":
				metadata.RuntimeContextScope = value
			case "runtimeContextCapturedAt", "runtime_context_captured_at":
				metadata.RuntimeContextCapturedAt = value
			case "runtimeContextHash", "runtime_context_hash":
				metadata.RuntimeContextHash = value
			case "blocked_report_id":
				metadata.BlockedReportID = value
			case "blocked_scope":
				metadata.BlockedScope = value
			case "blocked_scope_id":
				metadata.BlockedScopeID = value
			case "blocked_reason":
				metadata.BlockedReason = value
			}
		}
	}

	if metadata.From == "" || metadata.To == "" {
		return Metadata{}, fmt.Errorf("missing from or to in params block")
	}
	return metadata, nil
}

func ValidateInputRequestToken(value string) error {
	if value == "" {
		return fmt.Errorf("must not be empty")
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("must not contain leading or trailing whitespace")
	}
	if strings.ContainsAny(value, "/\\") {
		return fmt.Errorf("must not contain path separators")
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("must not contain whitespace or control characters")
		}
	}
	return nil
}

func directParamsChild(line string, childIndent int) (string, string, bool) {
	line = strings.TrimRight(line, "\r")
	if childIndent <= 0 || leadingSpaces(line) != childIndent {
		return "", "", false
	}
	key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.HasPrefix(key, "#") {
		return "", "", false
	}
	return key, strings.TrimSpace(value), true
}

func ExplicitParamsReplyPolicy(content string) (string, bool) {
	return ExplicitParamsReplyPolicyIgnoringGenerated(content, "")
}

func ExplicitParamsReplyPolicyIgnoringGenerated(content, generatedValue string) (string, bool) {
	fields := paramsReplyPolicyFields(content)
	explicitPolicy := ""
	hasExplicitPolicy := false
	for _, field := range fields {
		if field.Value == "" || field.Value == "{reply_policy}" || field.Value == generatedValue {
			continue
		}
		explicitPolicy = field.Value
		hasExplicitPolicy = true
	}
	return explicitPolicy, hasExplicitPolicy
}

type paramsReplyPolicyField struct {
	Value string
}

func paramsReplyPolicyFields(content string) []paramsReplyPolicyField {
	frontmatter, _, ok, err := ScanFrontmatter(content)
	if !ok || err != nil {
		return nil
	}
	lines := strings.Split(frontmatter, "\n")
	paramsIndex, paramsEnd := paramsBlockRange(lines)
	if paramsIndex < 0 {
		return nil
	}
	childIndent := paramsChildIndent(lines, paramsIndex, paramsEnd)
	fields := []paramsReplyPolicyField{}
	for idx := paramsIndex + 1; idx < paramsEnd; idx++ {
		key, value, ok := directParamsChild(lines[idx], childIndent)
		if !ok {
			continue
		}
		fieldKey, ok := managedParamFieldKey(key)
		if !ok || fieldKey != "replyPolicy" {
			continue
		}
		fields = append(fields, paramsReplyPolicyField{Value: value})
	}
	return fields
}

func ResolveReplyPolicyFromContent(content string) string {
	if metadata, err := ParseMetadata(content); err == nil {
		return ResolveReplyPolicyFromMetadata(metadata)
	}
	if IsNoReplyBody(content) {
		return "none"
	}
	return "none"
}

func ResolveReplyPolicyFromMetadata(metadata Metadata) string {
	switch strings.ToLower(strings.TrimSpace(metadata.ReplyPolicy)) {
	case "none", "no_reply", "no-reply":
		return "none"
	case "required":
		return "required"
	}
	if strings.EqualFold(metadata.From, "postman") || strings.EqualFold(metadata.From, "daemon") {
		return "none"
	}
	switch strings.ToLower(strings.TrimSpace(metadata.MessageType)) {
	case "approval_request", "status_request", "reply_request":
		return "required"
	case "ping", "dead_letter_notification", "edge_violation_warning":
		return "none"
	case "status_update", "alert", "pane_hint":
		return "none"
	}
	if IsNoReplyBody(metadata.Body) {
		return "none"
	}
	return "none"
}

func ResolveReplyPolicyForSend(body string, noReply, replyRequired bool) string {
	if noReply {
		return "none"
	}
	if replyRequired {
		return "required"
	}
	if IsNoReplyBody(body) {
		return "none"
	}
	return "none"
}

func IsNoReplyBody(content string) bool {
	body := BodyFromContent(content)
	if body == "" {
		body = strings.TrimSpace(content)
	}
	firstLine := body
	if idx := strings.Index(firstLine, "\n"); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	switch strings.ToUpper(strings.TrimSpace(firstLine)) {
	case "ACK", "DONE", "PING", "HEARTBEAT_OK":
		return true
	default:
		return false
	}
}

func EnsureParams(content string, fields map[string]string) string {
	scan, ok, err := scanFrontmatter(content)
	if !ok || err != nil {
		return content
	}
	frontmatter := scan.frontmatter
	lines := strings.Split(frontmatter, "\n")
	paramsIndex, paramsEnd := paramsBlockRange(lines)
	if paramsIndex < 0 {
		return content
	}
	childIndent := paramsChildIndent(lines, paramsIndex, paramsEnd)
	if childIndent <= 0 {
		childIndent = 2
	}
	paramsIndent := strings.Repeat(" ", childIndent)

	existing := make(map[string]bool)
	changed := false
	for idx, line := range lines {
		if idx <= paramsIndex || idx >= paramsEnd {
			continue
		}
		key, _, ok := directParamsChild(line, childIndent)
		if !ok {
			continue
		}
		if fieldKey, ok := managedParamFieldKey(key); ok {
			existing[fieldKey] = true
			if value := managedParamFieldValue(fields, fieldKey); value != "" {
				updatedLine := paramsIndent + key + ": " + encodeManagedParamValue(fieldKey, value)
				if lines[idx] != updatedLine {
					lines[idx] = updatedLine
					changed = true
				}
			}
			continue
		}
		existing[key] = true
	}

	insert := []string{}
	for _, key := range []string{"messageId", "replyPolicy", "replyTo", "input_request_id", "fills_input_request_id", "thread_id", "command_hash", "input_request_set_id", "evidence_command", "evidence_cwd", "evidence_env_allowlist", "evidence_timeout_seconds", "evidence_side_effect_class", "evidence_artifact", "evidence_hash", "branch_id", "completion_rule", "runtimeContextId", "runtimeContextScope", "runtimeContextCapturedAt", "runtimeContextHash"} {
		value := managedParamFieldValue(fields, key)
		if value == "" || existing[key] {
			continue
		}
		insert = append(insert, paramsIndent+key+": "+encodeManagedParamValue(key, value))
	}
	if len(insert) == 0 {
		if !changed {
			return content
		}
		return content[:scan.frontmatterStart] + strings.Join(lines, "\n") + content[scan.closeStart:]
	}

	updated := make([]string, 0, len(lines)+len(insert))
	updated = append(updated, lines[:paramsIndex+1]...)
	updated = append(updated, insert...)
	updated = append(updated, lines[paramsIndex+1:]...)
	return content[:scan.frontmatterStart] + strings.Join(updated, "\n") + content[scan.closeStart:]
}

func encodeManagedParamValue(key, value string) string {
	if isEvidenceParamKey(key) {
		return strconv.Quote(value)
	}
	return value
}

func decodeEvidenceParamValue(value string) string {
	var decoded string
	if err := yaml.Unmarshal([]byte(value), &decoded); err == nil {
		return decoded
	}
	return value
}

func isEvidenceParamKey(key string) bool {
	switch key {
	case "evidence_command",
		"evidence_cwd",
		"evidence_env_allowlist",
		"evidence_timeout_seconds",
		"evidence_side_effect_class",
		"evidence_artifact",
		"evidence_hash":
		return true
	default:
		return false
	}
}

func ParamsReplyPolicyUsesPlaceholder(content string) bool {
	frontmatter, _, ok, err := ScanFrontmatter(content)
	if !ok || err != nil {
		return false
	}
	lines := strings.Split(frontmatter, "\n")
	paramsIndex, paramsEnd := paramsBlockRange(lines)
	if paramsIndex < 0 {
		return false
	}
	childIndent := paramsChildIndent(lines, paramsIndex, paramsEnd)
	foundPlaceholder := false
	for idx := paramsIndex + 1; idx < paramsEnd; idx++ {
		key, value, ok := directParamsChild(lines[idx], childIndent)
		if !ok {
			continue
		}
		fieldKey, ok := managedParamFieldKey(key)
		if !ok || fieldKey != "replyPolicy" {
			continue
		}
		switch value {
		case "{reply_policy}":
			foundPlaceholder = true
		case "":
			continue
		default:
			return false
		}
	}
	return foundPlaceholder
}

func paramsBlockRange(lines []string) (int, int) {
	paramsIndex := -1
	for idx, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "params:" {
			paramsIndex = idx
			break
		}
	}
	if paramsIndex < 0 {
		return -1, -1
	}
	end := len(lines)
	for idx := paramsIndex + 1; idx < len(lines); idx++ {
		line := strings.TrimRight(lines[idx], "\r")
		if line != "" && line[0] != ' ' {
			end = idx
			break
		}
	}
	return paramsIndex, end
}

func paramsChildIndent(lines []string, paramsIndex, paramsEnd int) int {
	childIndent := -1
	for idx := paramsIndex + 1; idx < paramsEnd; idx++ {
		line := strings.TrimRight(lines[idx], "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, _, ok := strings.Cut(trimmed, ":")
		if !ok || strings.TrimSpace(key) == "" {
			continue
		}
		indent := leadingSpaces(line)
		if indent == 0 {
			continue
		}
		if childIndent < 0 || indent < childIndent {
			childIndent = indent
		}
	}
	return childIndent
}

func leadingSpaces(line string) int {
	count := 0
	for count < len(line) && line[count] == ' ' {
		count++
	}
	return count
}

func managedParamFieldKey(key string) (string, bool) {
	switch key {
	case "messageId", "message_id":
		return "messageId", true
	case "replyPolicy", "reply_policy":
		return "replyPolicy", true
	case "replyTo", "reply_to":
		return "replyTo", true
	case "input_request_id":
		return "input_request_id", true
	case "fills_input_request_id":
		return "fills_input_request_id", true
	case "thread_id":
		return "thread_id", true
	case "command_hash":
		return "command_hash", true
	case "input_request_set_id":
		return "input_request_set_id", true
	case "evidence_command":
		return "evidence_command", true
	case "evidence_cwd":
		return "evidence_cwd", true
	case "evidence_env_allowlist":
		return "evidence_env_allowlist", true
	case "evidence_timeout_seconds":
		return "evidence_timeout_seconds", true
	case "evidence_side_effect_class":
		return "evidence_side_effect_class", true
	case "evidence_artifact":
		return "evidence_artifact", true
	case "evidence_hash":
		return "evidence_hash", true
	case "branch_id":
		return "branch_id", true
	case "completion_rule":
		return "completion_rule", true
	case "runtimeContextId", "runtime_context_id":
		return "runtimeContextId", true
	case "runtimeContextScope", "runtime_context_scope":
		return "runtimeContextScope", true
	case "runtimeContextCapturedAt", "runtime_context_captured_at":
		return "runtimeContextCapturedAt", true
	case "runtimeContextHash", "runtime_context_hash":
		return "runtimeContextHash", true
	default:
		return "", false
	}
}

func managedParamFieldValue(fields map[string]string, fieldKey string) string {
	for _, key := range managedParamFieldAliases(fieldKey) {
		value := fields[key]
		if strings.TrimSpace(value) == "" {
			continue
		}
		if isEvidenceParamKey(fieldKey) {
			return value
		}
		return strings.TrimSpace(value)
	}
	return ""
}

func managedParamFieldAliases(fieldKey string) []string {
	switch fieldKey {
	case "messageId":
		return []string{"messageId", "message_id"}
	case "replyPolicy":
		return []string{"replyPolicy", "reply_policy"}
	case "replyTo":
		return []string{"replyTo", "reply_to"}
	case "input_request_id":
		return []string{"input_request_id"}
	case "fills_input_request_id":
		return []string{"fills_input_request_id"}
	case "thread_id", "command_hash":
		return []string{fieldKey}
	case "input_request_set_id":
		return []string{"input_request_set_id"}
	case "evidence_command", "evidence_cwd", "evidence_env_allowlist", "evidence_timeout_seconds", "evidence_side_effect_class", "evidence_artifact", "evidence_hash", "branch_id", "completion_rule":
		return []string{fieldKey}
	case "runtimeContextId":
		return []string{"runtimeContextId", "runtime_context_id"}
	case "runtimeContextScope":
		return []string{"runtimeContextScope", "runtime_context_scope"}
	case "runtimeContextCapturedAt":
		return []string{"runtimeContextCapturedAt", "runtime_context_captured_at"}
	case "runtimeContextHash":
		return []string{"runtimeContextHash", "runtime_context_hash"}
	default:
		return []string{fieldKey}
	}
}
