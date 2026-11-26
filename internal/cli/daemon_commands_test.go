package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseDaemonArgsAllowlist(t *testing.T) {
	for _, tc := range []struct {
		args []string
		ok   bool
	}{
		{[]string{"--", "heartbeat"}, true},
		{[]string{"--every", "10s", "--", "heartbeat"}, true},
		{[]string{"--every", "1s", "--", "heartbeat"}, true},
		{[]string{"--every", "0s", "--", "heartbeat"}, false},
		{[]string{"--every", "1us", "--", "heartbeat"}, false},
		{[]string{"--config", "/tmp/postman.md", "--", "heartbeat"}, true},
		{[]string{"--config", "", "--", "heartbeat"}, false},
		{[]string{"heartbeat"}, false},
		{[]string{"--", "github-sync", "https://github.com/o/r/issues/1"}, false},
		{[]string{"--", "sh", "-c", "true"}, false},
		{[]string{"--every", "-1s", "--", "heartbeat"}, false},
		{[]string{"--config", "relative.md", "--", "heartbeat"}, false},
	} {
		_, err := parseDaemonArgs(tc.args)
		if (err == nil) != tc.ok {
			t.Errorf("args %q: error %v, want success %t", tc.args, err, tc.ok)
		}
	}
}

func TestDaemonPreservesChildExitAndStreams(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux-a2a-postman")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'out\\n'\nprintf 'err\\n' >&2\nexit 7\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutWriter, stderrWriter
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	err = RunDaemon([]string{"--every", "1s", "--", "heartbeat"})
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	stdout, outErr := io.ReadAll(stdoutReader)
	if outErr != nil {
		t.Fatal(outErr)
	}
	stderr, readErr := io.ReadAll(stderrReader)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var exit interface{ ExitCode() int }
	if !errors.As(err, &exit) || exit.ExitCode() != 7 || string(stdout) != "out\n" || string(stderr) != "err\n" {
		t.Fatalf("exit=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
}

func TestDaemonNextDueRejectsOversizedScheduleResponse(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "tmux-a2a-postman")
	script := "#!/bin/sh\ni=0\nwhile [ \"$i\" -lt 1000 ]; do printf 12345678; i=$((i+1)); done\n"
	if err := os.WriteFile(child, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	_, err := daemonNextDue(context.Background(), daemonOptions{Task: "heartbeat"})
	if err == nil || !strings.Contains(err.Error(), "exceeds 4096 bytes") {
		t.Fatalf("oversized schedule response: %v", err)
	}
}

func TestDaemonNextDueRebasesElapsedSchedule(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "tmux-a2a-postman")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"command\":\"heartbeat\",\"every\":\"1s\",\"next_due_at\":\"2000-01-01T00:00:00Z\"}'\n"
	if err := os.WriteFile(child, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	start := time.Now()
	due, err := daemonNextDue(context.Background(), daemonOptions{Task: "heartbeat"})
	if err != nil || due.Sub(start) < 900*time.Millisecond {
		t.Fatalf("elapsed schedule was not rebased by its interval: due=%v err=%v", due, err)
	}
}

func TestBoundedDaemonDuePreservesLaterScheduleAndClampsNearFuture(t *testing.T) {
	now := time.Date(2026, 11, 26, 3, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name             string
		due, every, want time.Duration
	}{
		{"elapsed", -time.Second, time.Hour, time.Hour},
		{"future 50ms", 50 * time.Millisecond, time.Hour, time.Second},
		{"future 500ms", 500 * time.Millisecond, time.Hour, time.Second},
		{"exact minimum", time.Second, time.Hour, time.Second},
		{"later schedule", 2 * time.Second, time.Hour, 2 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := boundedDaemonDue(now, now.Add(tc.due), tc.every)
			if !got.Equal(now.Add(tc.want)) {
				t.Fatalf("due=%v every=%v: got=%v want=%v", tc.due, tc.every, got, now.Add(tc.want))
			}
		})
	}
}

func TestDaemonLoopWaitsForChildAndStopsOnFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runs, active := 0, false
	err := daemonLoop(ctx, func(context.Context) (time.Time, error) {
		if active {
			t.Fatal("scheduled while child active")
		}
		return time.Now().Add(-time.Second), nil
	}, func(context.Context) error {
		if active {
			t.Fatal("overlapping child")
		}
		active = true
		defer func() { active = false }()
		runs++
		if runs == 2 {
			return errors.New("child failed")
		}
		return nil
	})
	if runs != 2 || err == nil || !strings.Contains(err.Error(), "child failed") {
		t.Fatalf("runs=%d err=%v", runs, err)
	}
}

func TestDaemonLoopCancellationBeforeDue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	called := false
	cancel()
	err := daemonLoop(ctx, func(context.Context) (time.Time, error) {
		return time.Now().Add(time.Hour), nil
	}, func(context.Context) error { called = true; return nil })
	if err != nil || called {
		t.Fatalf("err=%v called=%t", err, called)
	}
}

func TestDaemonRunChildResolvesFreshExecutable(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	result := filepath.Join(t.TempDir(), "runs")
	for dir, label := range map[string]string{first: "old", second: "new"} {
		path := filepath.Join(dir, "tmux-a2a-postman")
		script := "#!/bin/sh\nprintf '%s:%s\\n' '" + label + "' \"$*\" >> \"$POSTMAN_TEST_RUNS\"\n"
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("POSTMAN_TEST_RUNS", result)
	t.Setenv("PATH", first)
	if err := daemonRunChild(context.Background(), daemonOptions{Task: "heartbeat"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", second)
	if err := daemonRunChild(context.Background(), daemonOptions{Task: "heartbeat"}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old:heartbeat\nnew:heartbeat\n" {
		t.Fatalf("runs: %q", got)
	}
}
