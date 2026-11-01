package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Run the real public main in a child process so its ExitCode forwarding and
// stdout/stderr contract are observed across an OS process boundary.
func TestIssue937PublicCLIChild(t *testing.T) {
	raw := os.Getenv("POSTMAN_TEST_PUBLIC_ARGS")
	if raw == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"postman"}, args...)
	main()
	os.Exit(0)
}

func TestWorkspaceExecuteHeredocProcessPreservesExitAndStreams(t *testing.T) {
	f := newPublicApprovalFixture(t)
	f.writeConfig(false)
	// Historical approval-looking records cannot authorize or affect a fresh run.
	legacy := filepath.Join(f.stateHome, "legacy", "approved.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"decision":"approved","command":"old"}`)
	if err := os.WriteFile(legacy, original, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := f.execution(ctx, "printf protocol-output; printf diagnostic >&2; exit 7")
	var out, diagnostic bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &diagnostic
	err := cmd.Run()
	assertExit(t, err, 7)
	result := decodeExecutionOutput(t, diagnostic.Bytes())
	if out.String() != "protocol-output" || !bytes.HasPrefix(diagnostic.Bytes(), []byte("diagnostic\n{")) || result.Success || result.Executed == nil || !*result.Executed || result.ExitCode == nil || *result.ExitCode != 7 || result.Approval != nil {
		t.Fatal(result, out.String(), diagnostic.String())
	}
	raw, err := os.ReadFile(legacy)
	if err != nil || !bytes.Equal(raw, original) {
		t.Fatal("historical approval changed", err)
	}
}
