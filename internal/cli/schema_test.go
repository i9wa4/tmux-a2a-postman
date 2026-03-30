package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunSchema_SendMessageOutput(t *testing.T) {
	var stdout bytes.Buffer

	if err := runSchema(&stdout, []string{"send-message"}); err != nil {
		t.Fatalf("runSchema: %v", err)
	}
	if !strings.Contains(stdout.String(), `"title": "send-message options"`) {
		t.Fatalf("stdout missing schema title: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"to"`) {
		t.Fatalf("stdout missing to property: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"body"`) {
		t.Fatalf("stdout missing body property: %q", stdout.String())
	}
}
