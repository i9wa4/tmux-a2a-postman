package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestWorkspacePublicCommandInputsRejectRemovedAndAmbiguousForms(t *testing.T) {
	f := newPublicApprovalFixture(t)
	for _, args := range [][]string{
		{"pop", "m-00000000000000000000000000000000"},
		{"get-status", "worker"},
		{"pop", "--postman-md", f.config},
		{"pop", "--capture-related-panes"},
		{"pop", "--config", f.config, "--config", f.config},
		{"send-heredoc", "--to", "peer", "--to", "worker"},
		{"send-heredoc", "--to", "peer", "--no-reply"},
		{"send-heredoc", "--to", "peer", "--reply-required"},
		{"send-heredoc", "--to", "peer", "--reply-to", "m-00000000000000000000000000000000"},
		{"send-heredoc", "--to", "m-00000000000000000000000000000000,m-00000000000000000000000000000000"},
		{"execute-heredoc", "--command", "true"},
		{"execute-heredoc", "--reason", "why", "--reason", "different"},
		{"approve-execution", "--to", "m-00000000000000000000000000000000", "--reason", "why", "--approve=true"},
		{"decline-execution", "--to", "m-00000000000000000000000000000000", "--reason", "why", "--decision", "approved"},
	} {
		t.Run(strings.Join(args[:1], ""), func(t *testing.T) {
			got, err := f.run(f.worker, "literal body", args...)
			if err == nil || got.Success || got.Reason == "" {
				t.Fatal("accepted malformed/removed syntax", args, got, err)
			}
		})
	}
	if _, err := os.Stat(f.stateRoot()); !os.IsNotExist(err) {
		t.Fatal("parser failures initialized workspace", err)
	}
	for _, command := range []string{"get-status", "pop", "send-heredoc", "execute-heredoc", "approve-execution", "decline-execution"} {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		cmd := f.child(ctx, f.worker, command, "--help")
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil || !bytes.Contains(out, []byte(command)) {
			t.Fatal(command, err, string(out))
		}
	}
	if _, err := os.Stat(f.stateRoot()); !os.IsNotExist(err) {
		t.Fatal("help initialized workspace", err)
	}
}

func TestWorkspacePublicCompactOutputHasNoMetadataOrBodyEcho(t *testing.T) {
	f := newPublicApprovalFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := f.child(ctx, f.peer, "send-heredoc", "--config", f.config, "--to", "worker")
	cmd.Stdin = strings.NewReader("private literal protocol payload")
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"status", "message", "body", "pop_receipt_absolute_path", "capture", "notice"} {
		if _, ok := fields[key]; ok {
			t.Fatal("obsolete/verbose field", key, string(raw))
		}
	}
	if bytes.Contains(raw, []byte("private literal protocol payload")) {
		t.Fatal("body disclosed")
	}
	for _, key := range []string{"workspace", "as_of", "success", "self"} {
		if _, ok := fields[key]; !ok {
			t.Fatal("missing common field", key)
		}
	}
	cmd = f.child(ctx, f.worker, "pop", "--config", f.config)
	raw, err = cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["markdown_absolute_path"]; !ok {
		t.Fatal("POP missing final path")
	}
	var self map[string]json.RawMessage
	if err := json.Unmarshal(fields["self"], &self); err != nil {
		t.Fatal(err)
	}
	if len(self) != 9 {
		t.Fatalf("unexpected self fields: %s", fields["self"])
	}
	for _, key := range []string{"node", "unread_count", "inbound_count", "outbound_count", "draft_count", "failed_count", "last_pop_at", "last_capture_at", "last_screen_change_at"} {
		if _, ok := self[key]; !ok {
			t.Fatal("missing common node field", key)
		}
	}
}
