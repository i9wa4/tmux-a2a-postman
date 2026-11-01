package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"
)

// ArchiveContext separates generated recipient instructions from literal sender
// Markdown. Durable intents retain Markdown; the archive header stores only its
// descriptor, followed by the complete text exactly once before the sender body.
type ArchiveContext struct {
	Version  int    `json:"version"`
	Bytes    int    `json:"bytes"`
	SHA256   string `json:"sha256"`
	Markdown string `json:"markdown,omitempty"`
}

// MessageContextBuilder is prepared outside mailbox locks. The callback renders
// only already-loaded configuration against the verified send snapshot.
type MessageContextBuilder func(context.Context, Snapshot, Message) (string, error)

func (m Mail) addMessageContext(ctx context.Context, snapshot Snapshot, msg *Message) error {
	if m.Context == nil {
		return nil
	}
	text, err := m.Context(ctx, snapshot, *msg)
	if err != nil {
		return err
	}
	msg.Context = &ArchiveContext{Version: 1, Bytes: len(text), SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(text))), Markdown: text}
	return nil
}

func validateMessageContext(msg Message) error {
	c := msg.Context
	if c == nil {
		return nil
	}
	if c.Version != 1 || c.Bytes <= 0 || c.Bytes > 48*1024 || c.Bytes != len(c.Markdown) || !utf8.ValidString(c.Markdown) ||
		c.SHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(c.Markdown))) {
		return errors.New("invalid or oversized generated recipient context")
	}
	return nil
}

// marshalMessage never serializes the full generated text twice in an archive.
func marshalMessage(msg Message) ([]byte, error) {
	metadata := msg
	text := msg.Body + messageGuidance(msg)
	if msg.Context != nil {
		descriptor := *msg.Context
		descriptor.Markdown = ""
		metadata.Context = &descriptor
		text = msg.Context.Markdown + msg.Body
	}
	header, err := json.Marshal(metadata)
	if err != nil {
		return nil, err
	}
	return append(append([]byte("<!-- postman "), header...), []byte(" -->\n\n"+text)...), nil
}

// ReplyGuidance is generated from immutable message identity and kind. New
// context renderers place it before the literal sender body; legacy archives
// retain their historical trailing guidance without being rewritten.
func ReplyGuidance(msg Message) string { return messageGuidance(msg) }
