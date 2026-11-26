package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDaemonPublicChildStreamsAndExit(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "tmux-a2a-postman")
	script := "#!/bin/sh\nprintf 'out<>$\\n'\nprintf 'err\\r\\n' >&2\nexit 7\n"
	if err := os.WriteFile(child, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args, err := json.Marshal([]string{"daemon", "--every", "1s", "--", "heartbeat"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("POSTMAN_TEST_PUBLIC_ARGS", string(args))
	cmd := exec.Command(self, "-test.run=^TestIssue937PublicCLIChild$")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("exit=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
	}
	if stdout.String() != "out<>$\n" || stderr.String() != "err\r\n" {
		t.Fatalf("child streams changed: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}
