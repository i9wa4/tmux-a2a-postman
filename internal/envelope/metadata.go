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
	inParams := false
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "params:" {
			inParams = true
			continue
		}
		if !inParams {
			continue
		}
		if len(line) > 0 && line[0] != ' ' {
			inParams = false
			continue
		}
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
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

	if metadata.From == "" || metadata.To == "" {
		return Metadata{}, fmt.Errorf("missing from or to in params block")
	}
	return metadata, nil
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
	paramsIndex := -1
	existing := make(map[string]bool)
	changed := false
	for idx, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "params:" {
			paramsIndex = idx
			continue
		}
		if paramsIndex >= 0 && strings.HasPrefix(line, "  ") {
			key, _, ok := strings.Cut(strings.TrimSpace(line), ":")
			if ok {
				if fieldKey, ok := managedParamFieldKey(key); ok {
					existing[fieldKey] = true
					if value := strings.TrimSpace(fields[fieldKey]); value != "" {
						updatedLine := "  " + key + ": " + value
						if lines[idx] != updatedLine {
							lines[idx] = updatedLine
							changed = true
						}
					}
					continue
				}
				existing[key] = true
			}
		}
	}
	if paramsIndex < 0 {
		return content
	}

	insert := []string{}
	for _, key := range []string{"messageId", "replyPolicy", "replyTo"} {
		value := strings.TrimSpace(fields[key])
		if value == "" || existing[key] {
			continue
		}
		insert = append(insert, "  "+key+": "+value)
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
