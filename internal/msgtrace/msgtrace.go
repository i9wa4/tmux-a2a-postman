package msgtrace

import (
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
)

const Component = "message_lifecycle"

type Fields struct {
	MessageID             string
	MessagePath           string
	Sender                string
	Recipient             string
	ContextID             string
	TmuxSession           string
	InputRequestID        string
	ReplyTo               string
	DeliveryAttempt       int
	DaemonSubmitRequestID string
	DaemonSubmitCommand   string
	SubmitPath            string
	Result                string
	Reason                string
}

func FromContent(filename, messagePath, tmuxSession, content string) Fields {
	fields := Fields{
		MessageID:   filename,
		MessagePath: messagePath,
		TmuxSession: tmuxSession,
	}
	if metadata, err := envelope.ParseMetadata(content); err == nil {
		if metadata.MessageID != "" {
			fields.MessageID = metadata.MessageID
		}
		fields.Sender = metadata.From
		fields.Recipient = metadata.To
		fields.ContextID = metadata.ContextID
		fields.InputRequestID = metadata.InputRequestID
		fields.ReplyTo = metadata.ReplyTo
	}
	if fields.MessageID == "" && messagePath != "" {
		fields.MessageID = filepath.Base(messagePath)
	}
	return fields
}

func Log(event string, fields Fields) {
	log.Print(Line(event, fields))
}

func HasMessageContext(fields Fields) bool {
	return fields.MessageID != "" || fields.MessagePath != "" || fields.Sender != "" || fields.Recipient != "" || fields.ContextID != "" || fields.InputRequestID != "" || fields.ReplyTo != ""
}

func Line(event string, fields Fields) string {
	parts := []string{
		"postman:",
		"component=" + Component,
		"event=" + value(event),
	}
	appendField := func(key, raw string) {
		if raw != "" {
			parts = append(parts, key+"="+value(raw))
		}
	}
	appendField("message_id", fields.MessageID)
	appendField("message_path", fields.MessagePath)
	appendField("sender", fields.Sender)
	appendField("recipient", fields.Recipient)
	appendField("context_id", fields.ContextID)
	appendField("tmux_session", fields.TmuxSession)
	appendField("input_request_id", fields.InputRequestID)
	appendField("reply_to", fields.ReplyTo)
	if fields.DeliveryAttempt > 0 {
		parts = append(parts, fmt.Sprintf("delivery_attempt=%d", fields.DeliveryAttempt))
	}
	appendField("daemon_submit_request_id", fields.DaemonSubmitRequestID)
	appendField("daemon_submit_command", fields.DaemonSubmitCommand)
	appendField("submit_path", fields.SubmitPath)
	appendField("result", fields.Result)
	appendField("reason", fields.Reason)
	return strings.Join(parts, " ")
}

func value(raw string) string {
	if raw == "" {
		return `""`
	}
	if strings.ContainsAny(raw, " \t\r\n\"\\") {
		return strconv.Quote(raw)
	}
	return raw
}
