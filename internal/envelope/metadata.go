package envelope

import (
	"fmt"
	"strings"
)

type Metadata struct {
	From        string
	To          string
	MessageID   string
	ReplyPolicy string
	ReplyTo     string
	MessageType string
	Timestamp   string
	ThreadID    string
	Body        string
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
			}
		}
	}

	if metadata.From == "" || metadata.To == "" {
		return Metadata{}, fmt.Errorf("missing from or to in params block")
	}
	return metadata, nil
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
	fields := paramsReplyPolicyFields(content)
	explicitPolicy := ""
	hasExplicitPolicy := false
	for _, field := range fields {
		if field.Value == "" || field.Value == "{reply_policy}" {
			continue
		}
		explicitPolicy = field.Value
		hasExplicitPolicy = true
	}
	return explicitPolicy, hasExplicitPolicy
}

func ExplicitParamsReplyPolicyFromExpandedTemplate(templateContent, expandedContent string) (string, bool) {
	templateFields := paramsReplyPolicyFields(templateContent)
	expandedFields := paramsReplyPolicyFields(expandedContent)
	explicitPolicy := ""
	hasExplicitPolicy := false
	for idx, templateField := range templateFields {
		if templateField.Value == "" || templateField.Value == "{reply_policy}" {
			continue
		}
		explicitPolicy = templateField.Value
		if idx < len(expandedFields) {
			explicitPolicy = expandedFields[idx].Value
		}
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
			if value := strings.TrimSpace(fields[fieldKey]); value != "" {
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
	for _, key := range []string{"messageId", "replyPolicy", "replyTo"} {
		value := strings.TrimSpace(fields[key])
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
	default:
		return "", false
	}
}
