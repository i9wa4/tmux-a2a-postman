package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunVersion_WritesJSON(t *testing.T) {
	var stdout bytes.Buffer

	if err := RunVersion(&stdout, nil); err != nil {
		t.Fatalf("RunVersion: %v", err)
	}

	var got versionOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("version output is not JSON: %v\n%s", err, stdout.String())
	}
	if got.Name != "tmux-a2a-postman" {
		t.Fatalf("name = %q, want tmux-a2a-postman", got.Name)
	}
	if got.Version == "" {
		t.Fatal("version is empty")
	}
	if got.Commit == "" {
		t.Fatal("commit is empty")
	}
}

func TestRunVersion_RejectsArguments(t *testing.T) {
	var stdout bytes.Buffer

	err := RunVersion(&stdout, []string{"unexpected"})
	if err == nil {
		t.Fatal("RunVersion returned nil error for unexpected argument")
	}
	if !strings.Contains(err.Error(), "version takes no arguments") {
		t.Fatalf("error = %q, want version argument error", err.Error())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}
