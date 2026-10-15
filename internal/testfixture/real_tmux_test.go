package testfixture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWaitPIDGoneAfterReapedProcess(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	proc, err := os.FindProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	err = proc.Signal(syscall.Signal(0))
	t.Logf("signal(0) after Wait: %T %v; ESRCH=%t; ErrProcessDone=%t", err, err, errors.Is(err, syscall.ESRCH), errors.Is(err, os.ErrProcessDone))
	if !waitPIDGone(cmd.Process.Pid, 50*time.Millisecond) {
		t.Fatal("waitPIDGone did not recognize a reaped child")
	}
}

func TestRealTmuxOwnsPrivateServerAndSubprocessEnvironment(t *testing.T) {
	f, err := NewRealTmux(t)
	if err != nil {
		t.Fatal(err)
	}
	r := f.Resources()
	if r.ServerPID <= 0 || len(r.Sessions) != 1 || r.Sessions[0].Name != "fixture" || r.Sessions[0].PaneID == "" {
		t.Fatalf("incomplete owned-resource ledger: %+v", r)
	}
	if got := filepath.Dir(r.Socket); got != r.Root {
		t.Fatalf("socket parent = %q, want %q", got, r.Root)
	}
	if _, err := os.Stat(f.ConfigPath()); err != nil {
		t.Fatalf("fixed config snapshot: %v", err)
	}
	env := f.Env()
	for key, want := range map[string]string{
		"HOME":            filepath.Join(r.Root, "home"),
		"POSTMAN_HOME":    filepath.Join(r.Root, "postman"),
		"XDG_CONFIG_HOME": filepath.Join(r.Root, "config"),
		"XDG_STATE_HOME":  filepath.Join(r.Root, "state"),
		"XDG_CACHE_HOME":  filepath.Join(r.Root, "cache"),
		"TMUX":            fmt.Sprintf("%s,%d,0", r.Socket, r.ServerPID),
		"TMUX_PANE":       r.Sessions[0].PaneID,
	} {
		if got := envGet(env, key); got != want {
			t.Errorf("Env %s = %q, want %q", key, got, want)
		}
	}
	if got := os.Getenv("POSTMAN_HOME"); got == filepath.Join(r.Root, "postman") {
		t.Fatal("fixture changed the process environment")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if out, err := f.Tmux(ctx, "list-sessions", "-F", "#{session_name}"); err != nil || strings.TrimSpace(out) != "fixture" {
		t.Fatalf("private tmux list-sessions = %q, %v", out, err)
	}
	helperPID, err := f.StartHelper("controlled", "sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	if got := f.Resources().Helpers; len(got) != 1 || got[0] != helperPID {
		t.Fatalf("helper ledger = %v, want [%d]", got, helperPID)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("idempotent Close: %v", err)
	}
	assertFixtureGone(t, r.Root, r.ServerPID)
	if !waitPIDGone(helperPID, time.Second) {
		t.Fatalf("helper pid %d survived cleanup", helperPID)
	}
}

func TestRealTmuxCloseWaitsForStartedHelperRegistration(t *testing.T) {
	f, err := NewRealTmux(t)
	if err != nil {
		t.Fatal(err)
	}
	r := f.Resources()
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	started := make(chan int, 1)
	type startResult struct {
		pid int
		err error
	}
	startDone := make(chan startResult, 1)
	go func() {
		pid, err := f.startHelper("racing", "sleep", func(pid int) {
			started <- pid
			<-release
		}, "30")
		startDone <- startResult{pid: pid, err: err}
	}()
	helperPID := <-started
	if f.mu.TryLock() {
		f.mu.Unlock()
		t.Fatal("start released ledger lock before helper registration")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- f.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close finished before helper registration: %v", err)
	default:
	}
	close(release)
	if got := <-startDone; got.err != nil || got.pid != helperPID {
		t.Fatalf("StartHelper = %+v, want pid %d", got, helperPID)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("idempotent Close: %v", err)
	}
	assertFixtureGone(t, r.Root, r.ServerPID)
	if !waitPIDGone(helperPID, time.Second) {
		t.Fatalf("helper pid %d survived cleanup", helperPID)
	}
	if pid, err := f.StartHelper("late", "sleep", "30"); pid != 0 || err == nil || !strings.Contains(err.Error(), "closing") {
		t.Fatalf("post-close StartHelper = (%d, %v), want admission error without spawn", pid, err)
	}
}

func TestRealTmuxCloseCancelsAdmittedMutatingCommand(t *testing.T) {
	sentinel, err := NewRealTmux(t)
	if err != nil {
		t.Fatal(err)
	}
	sentinelResources := sentinel.Resources()
	f, err := NewRealTmux(t)
	if err != nil {
		t.Fatal(err)
	}
	r := f.Resources()
	admitted := make(chan struct{})
	f.mu.Lock()
	f.beforeExec = func(ctx context.Context) {
		close(admitted)
		<-ctx.Done()
	}
	f.mu.Unlock()
	commandDone := make(chan error, 1)
	go func() {
		_, err := f.Tmux(context.Background(), "new-session", "-d", "-s", "replacement")
		commandDone <- err
	}()
	select {
	case <-admitted:
	case <-time.After(3 * time.Second):
		t.Fatal("mutating command was not admitted")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- f.Close() }()
	select {
	case err := <-commandDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("admitted command error = %v, want cancellation before execution", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("admitted command did not drain")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Close did not finish after draining command")
	}
	assertFixtureGone(t, r.Root, r.ServerPID)
	if _, err := os.Lstat(r.Socket); !os.IsNotExist(err) {
		t.Fatalf("replacement server socket remains: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if _, err := f.Tmux(context.Background(), "new-session", "-d", "-s", "late"); err == nil || !strings.Contains(err.Error(), "closing") {
		t.Fatalf("post-close mutating command = %v, want admission rejection", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if out, err := sentinel.Tmux(ctx, "list-sessions", "-F", "#{session_name}"); err != nil || strings.TrimSpace(out) != "fixture" {
		t.Fatalf("unrelated sentinel changed: %q, %v", out, err)
	}
	if err := sentinel.Close(); err != nil {
		t.Fatal(err)
	}
	assertFixtureGone(t, sentinelResources.Root, sentinelResources.ServerPID)
}

func TestRealTmuxCloseRetriesAfterUndrainedCommand(t *testing.T) {
	f, err := NewRealTmux(t)
	if err != nil {
		t.Fatal(err)
	}
	r := f.Resources()
	admitted := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	f.mu.Lock()
	f.beforeExec = func(context.Context) {
		close(admitted)
		<-release
	}
	f.mu.Unlock()
	commandDone := make(chan error, 1)
	go func() {
		_, err := f.Tmux(context.Background(), "new-session", "-d", "-s", "replacement")
		commandDone <- err
	}()
	select {
	case <-admitted:
	case <-time.After(3 * time.Second):
		t.Fatal("mutating command was not admitted")
	}
	start := time.Now()
	err = f.Close()
	if err == nil || !strings.Contains(err.Error(), "draining") {
		t.Fatalf("first Close = %v, want bounded drain failure", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Close drain took %v, want bounded failure", elapsed)
	}
	if _, err := os.Stat(r.Root); err != nil {
		t.Fatalf("failed Close removed owned root: %v", err)
	}
	if _, err := f.Tmux(context.Background(), "new-session", "-d", "-s", "late"); err == nil || !strings.Contains(err.Error(), "closing") {
		t.Fatalf("operation after failed Close = %v, want admission rejection", err)
	}
	close(release)
	select {
	case err := <-commandDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("drained command error = %v, want cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("released command did not drain")
	}
	if err := f.Close(); err != nil {
		t.Fatalf("retry Close: %v", err)
	}
	assertFixtureGone(t, r.Root, r.ServerPID)
	if err := f.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
}

func TestRealTmuxPartialSetupFailureStillCleansServer(t *testing.T) {
	f, err := newRealTmux(t, realTmuxHooks{afterServer: func() error { return errors.New("injected setup failure") }})
	if err == nil || !strings.Contains(err.Error(), "injected setup failure") {
		t.Fatalf("setup error = %v", err)
	}
	if f == nil || f.Resources().ServerPID <= 0 {
		t.Fatalf("partial setup lost server ownership: %+v", f)
	}
	r := f.Resources()
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	assertFixtureGone(t, r.Root, r.ServerPID)
}

func TestRealTmuxPreRecordFailureStillCleansServer(t *testing.T) {
	for _, stage := range []string{"after-start", "record-error", "record-timeout"} {
		t.Run(stage, func(t *testing.T) {
			sentinel, err := NewRealTmux(t)
			if err != nil {
				t.Fatal(err)
			}
			sentinelResources := sentinel.Resources()
			var startedPID int
			capturePID := func(ctx context.Context, f *RealTmux) error {
				out, err := f.tmux(ctx, "display-message", "-p", "#{pid}")
				if err != nil {
					return err
				}
				startedPID, err = strconv.Atoi(strings.TrimSpace(out))
				return err
			}
			hooks := realTmuxHooks{}
			switch stage {
			case "after-start":
				hooks.afterStart = func(f *RealTmux) error {
					if err := capturePID(context.Background(), f); err != nil {
						return err
					}
					return errors.New("injected failure before PID record")
				}
			case "record-error", "record-timeout":
				hooks.recordServer = func(ctx context.Context, f *RealTmux) error {
					if err := capturePID(ctx, f); err != nil {
						return err
					}
					if stage == "record-timeout" {
						return context.DeadlineExceeded
					}
					return errors.New("injected PID record failure")
				}
			}
			f, err := newRealTmux(t, hooks)
			if err == nil || startedPID <= 0 {
				t.Fatalf("injected setup error = %v, started PID = %d", err, startedPID)
			}
			if got := f.Resources().ServerPID; got != 0 {
				t.Fatalf("PID recorded before injected failure: %d", got)
			}
			root := f.Root()
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			if got := f.Resources().ServerPID; got != startedPID {
				t.Fatalf("adopted PID = %d, want %d", got, startedPID)
			}
			assertFixtureGone(t, root, startedPID)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if out, err := sentinel.Tmux(ctx, "list-sessions", "-F", "#{session_name}"); err != nil || strings.TrimSpace(out) != "fixture" {
				t.Fatalf("unrelated sentinel changed: %q, %v", out, err)
			}
			if err := sentinel.Close(); err != nil {
				t.Fatal(err)
			}
			assertFixtureGone(t, sentinelResources.Root, sentinelResources.ServerPID)
		})
	}
}

func TestRealTmuxConcurrentRunsDoNotTouchSentinel(t *testing.T) {
	type result struct {
		f   *RealTmux
		err error
	}
	ready := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			f, err := NewRealTmux(t)
			ready <- result{f: f, err: err}
		}()
	}
	a, b := <-ready, <-ready
	if a.err != nil || b.err != nil {
		t.Fatalf("concurrent setup: %v, %v", a.err, b.err)
	}
	ra, rb := a.f.Resources(), b.f.Resources()
	if ra.Root == rb.Root || ra.Socket == rb.Socket || ra.ServerPID == rb.ServerPID {
		t.Fatalf("concurrent fixture collision: %+v %+v", ra, rb)
	}
	if ra.Sessions[0].Name != rb.Sessions[0].Name {
		t.Fatalf("same fixture name was not reused: %+v %+v", ra, rb)
	}
	if err := a.f.Close(); err != nil {
		t.Fatal(err)
	}
	assertFixtureGone(t, ra.Root, ra.ServerPID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if out, err := b.f.Tmux(ctx, "list-sessions", "-F", "#{session_name}"); err != nil || strings.TrimSpace(out) != "fixture" {
		t.Fatalf("unrelated sentinel was changed: %q, %v", out, err)
	}
	if err := b.f.Close(); err != nil {
		t.Fatal(err)
	}
	assertFixtureGone(t, rb.Root, rb.ServerPID)
}

func TestRealTmuxScenarioFailureStillRunsCleanup(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-test.run=^TestRealTmuxScenarioFailureHelper$", "-test.v")
	cmd.Env = append(os.Environ(), "POSTMAN_878_INTENTIONAL_FAILURE=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("intentional failing scenario unexpectedly passed:\n%s", out)
	}
	match := regexp.MustCompile(`FIXTURE_ROOT=([^ ]+) PID=([0-9]+) HELPER=([0-9]+)`).FindStringSubmatch(string(out))
	if len(match) != 4 {
		t.Fatalf("failing scenario omitted ownership evidence:\n%s", out)
	}
	pid, err := strconv.Atoi(match[2])
	if err != nil {
		t.Fatal(err)
	}
	assertFixtureGone(t, match[1], pid)
	helperPID, err := strconv.Atoi(match[3])
	if err != nil {
		t.Fatal(err)
	}
	if !waitPIDGone(helperPID, time.Second) {
		t.Fatalf("failed-test helper pid %d survived cleanup", helperPID)
	}
}

func TestRealTmuxScenarioFailureHelper(t *testing.T) {
	if os.Getenv("POSTMAN_878_INTENTIONAL_FAILURE") != "1" {
		return
	}
	f, err := NewRealTmux(t)
	if err != nil {
		t.Fatal(err)
	}
	r := f.Resources()
	helperPID, err := f.StartHelper("failed-test", "sleep", "30")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("FIXTURE_ROOT=%s PID=%d HELPER=%d\n", r.Root, r.ServerPID, helperPID)
	t.Fatal("intentional scenario failure")
}

func envGet(env []string, key string) string {
	for _, entry := range env {
		k, value, ok := strings.Cut(entry, "=")
		if ok && k == key {
			return value
		}
	}
	return ""
}

func assertFixtureGone(t *testing.T, root string, pid int) {
	t.Helper()
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("fixture root %s remains: %v", root, err)
	}
	if !waitPIDGone(pid, time.Second) {
		t.Fatalf("fixture server pid %d survived cleanup", pid)
	}
}
