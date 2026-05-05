package envelope

import (
	"fmt"
	"strings"
	"unicode"
)

type Metadata struct {
	From                  string
	To                    string
	MessageID             string
	ReplyPolicy           string
	ReplyTo               string
	MessageType           string
	Timestamp             string
	ThreadID              string
	ReplySlotID           string
	FillsReplySlotID      string
	ReplySetID            string
	ObligationID          string
	SatisfiesObligationID string
	ObligationGroupID     string
	BranchID              string
	CompletionRule        string
	Body                  string
}

func BodyFromContent(content string) string {
	first := strings.Index(content, "---\n")
	if first < 0 {
		return strings.TrimSpace(content)
	}
	rest := content[first+4:]
	second := strings.Index(rest, "\n---")
	if second < 0 {
		return strings.TrimSpace(content)
	}
	return strings.TrimSpace(rest[second+4:])
}

func ParseMetadata(content string) (Metadata, error) {
	first := strings.Index(content, "---\n")
	if first < 0 {
		return Metadata{}, fmt.Errorf("no frontmatter block found")
	}
	rest := content[first+4:]
	second := strings.Index(rest, "\n---")
	if second < 0 {
		return Metadata{}, fmt.Errorf("frontmatter not closed")
	}
	frontmatter := rest[:second]

	metadata := Metadata{Body: strings.TrimSpace(rest[second+4:])}
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
			case "from":
				metadata.From = value
			case "to":
				metadata.To = value
			case "messageId", "message_id":
				metadata.MessageID = value
			case "replyPolicy", "reply_policy":
				metadata.ReplyPolicy = value
			case "replyObligation", "reply_obligation":
				metadata.ReplyPolicy = value
			case "replyTo", "reply_to":
				metadata.ReplyTo = value
			case "messageType", "message_type":
				metadata.MessageType = value
			case "timestamp":
				metadata.Timestamp = value
			case "thread_id":
				metadata.ThreadID = value
			case "reply_slot_id", "reply_request_id", "obligation_id":
				if err := metadata.setReplySlotID(value, key); err != nil {
					return Metadata{}, err
				}
			case "fills_reply_slot_id", "satisfies_reply_request_id", "satisfies_obligation_id":
				if err := metadata.setFillsReplySlotID(value, key); err != nil {
					return Metadata{}, err
				}
			case "reply_set_id", "reply_request_group_id", "obligation_group_id":
				if err := metadata.setReplySetID(value, key); err != nil {
					return Metadata{}, err
				}
			case "branch_id":
				metadata.BranchID = value
			case "completion_rule":
				metadata.CompletionRule = value
			}
		}
	}

	if metadata.From == "" || metadata.To == "" {
		return Metadata{}, fmt.Errorf("missing from or to in params block")
	}
	return metadata, nil
}

func (m *Metadata) setReplySlotID(value, alias string) error {
	if err := setAliasValue(&m.ReplySlotID, value, "reply_slot_id", alias); err != nil {
		return err
	}
	if value != "" {
		m.ObligationID = m.ReplySlotID
	}
	return nil
}

func (m *Metadata) setFillsReplySlotID(value, alias string) error {
	if err := setAliasValue(&m.FillsReplySlotID, value, "fills_reply_slot_id", alias); err != nil {
		return err
	}
	if value != "" {
		m.SatisfiesObligationID = m.FillsReplySlotID
	}
	return nil
}

func (m *Metadata) setReplySetID(value, alias string) error {
	if err := setAliasValue(&m.ReplySetID, value, "reply_set_id", alias); err != nil {
		return err
	}
	if value != "" {
		m.ObligationGroupID = m.ReplySetID
	}
	return nil
}

func setAliasValue(current *string, value, canonicalName, alias string) error {
	if value == "" {
		return nil
	}
	if *current != "" && *current != value {
		return fmt.Errorf("conflicting %s aliases: %s differs", canonicalName, alias)
	}
	*current = value
	return nil
}

func ValidateReplySlotToken(value string) error {
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

func ValidateObligationToken(value string) error {
	return ValidateReplySlotToken(value)
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
	first := strings.Index(content, "---\n")
	if first < 0 {
		return nil
	}
	rest := content[first+4:]
	second := strings.Index(rest, "\n---")
	if second < 0 {
		return nil
	}
	lines := strings.Split(rest[:second], "\n")
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
	first := strings.Index(content, "---\n")
	if first < 0 {
		return content
	}
	rest := content[first+4:]
	second := strings.Index(rest, "\n---")
	if second < 0 {
		return content
	}
	frontmatter := rest[:second]
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
				updatedLine := paramsIndent + key + ": " + value
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
	for _, key := range []string{"messageId", "replyPolicy", "replyTo", "reply_slot_id", "fills_reply_slot_id", "reply_set_id", "branch_id", "completion_rule"} {
		value := managedParamFieldValue(fields, key)
		if value == "" || existing[key] {
			continue
		}
		insert = append(insert, paramsIndent+key+": "+value)
	}
	if len(insert) == 0 {
		if !changed {
			return content
		}
		return content[:first+4] + strings.Join(lines, "\n") + rest[second:]
	}

	updated := make([]string, 0, len(lines)+len(insert))
	updated = append(updated, lines[:paramsIndex+1]...)
	updated = append(updated, insert...)
	updated = append(updated, lines[paramsIndex+1:]...)
	return content[:first+4] + strings.Join(updated, "\n") + rest[second:]
}

func ParamsReplyPolicyUsesPlaceholder(content string) bool {
	first := strings.Index(content, "---\n")
	if first < 0 {
		return false
	}
	rest := content[first+4:]
	second := strings.Index(rest, "\n---")
	if second < 0 {
		return false
	}
	lines := strings.Split(rest[:second], "\n")
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
	case "replyPolicy", "reply_policy", "replyObligation", "reply_obligation":
		return "replyPolicy", true
	case "replyTo", "reply_to":
		return "replyTo", true
	case "reply_slot_id", "reply_request_id", "obligation_id":
		return "reply_slot_id", true
	case "fills_reply_slot_id", "satisfies_reply_request_id", "satisfies_obligation_id":
		return "fills_reply_slot_id", true
	case "reply_set_id", "reply_request_group_id", "obligation_group_id":
		return "reply_set_id", true
	case "branch_id":
		return "branch_id", true
	case "completion_rule":
		return "completion_rule", true
	default:
		return "", false
	}
}

func managedParamFieldValue(fields map[string]string, fieldKey string) string {
	for _, key := range managedParamFieldAliases(fieldKey) {
		if value := strings.TrimSpace(fields[key]); value != "" {
			return value
		}
	}
	return ""
}

func managedParamFieldAliases(fieldKey string) []string {
	switch fieldKey {
	case "messageId":
		return []string{"messageId", "message_id"}
	case "replyPolicy":
		return []string{"replyPolicy", "reply_policy", "replyObligation", "reply_obligation"}
	case "replyTo":
		return []string{"replyTo", "reply_to"}
	case "reply_slot_id":
		return []string{"reply_slot_id", "reply_request_id", "obligation_id"}
	case "fills_reply_slot_id":
		return []string{"fills_reply_slot_id", "satisfies_reply_request_id", "satisfies_obligation_id"}
	case "reply_set_id":
		return []string{"reply_set_id", "reply_request_group_id", "obligation_group_id"}
	case "branch_id", "completion_rule":
		return []string{fieldKey}
	default:
		return []string{fieldKey}
	}
}
