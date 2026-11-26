package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	minimumDaemonInterval        = time.Second
	maximumScheduleResponseBytes = 4096
)

type daemonOptions struct {
	Config string
	Every  time.Duration
	Task   string
}

// The child has already written its own stderr. The public CLI must forward
// only its exit status, without adding a daemon-owned error line.
type daemonChildExitError struct{ commandExitError }

func (daemonChildExitError) Reported() bool { return true }

func parseDaemonArgs(args []string) (daemonOptions, error) {
	var options daemonOptions
	separator := -1
	for i, arg := range args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		return options, errors.New("daemon requires -- heartbeat")
	}
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&options.Config, "config", "", "selected postman.md")
	fs.DurationVar(&options.Every, "every", 0, "scheduling override")
	if err := ValidateFlagDuplicates(fs, args[:separator]); err != nil {
		return options, err
	}
	if err := fs.Parse(args[:separator]); err != nil || fs.NArg() != 0 {
		return options, errors.New("invalid daemon options")
	}
	configProvided := false
	everyProvided := false
	fs.Visit(func(option *flag.Flag) {
		if option.Name == "config" {
			configProvided = true
		}
		if option.Name == "every" {
			everyProvided = true
		}
	})
	if configProvided && options.Config == "" {
		return options, errors.New("--config requires an absolute file path")
	}
	if len(args[separator+1:]) != 1 || args[separator+1] != "heartbeat" {
		return options, errors.New("daemon currently allows only heartbeat after --")
	}
	if everyProvided && options.Every < minimumDaemonInterval {
		return options, errors.New("--every must be at least 1s")
	}
	if options.Config != "" && !filepath.IsAbs(options.Config) {
		return options, errors.New("--config must be absolute")
	}
	options.Task = "heartbeat"
	return options, nil
}

func daemonChildArgs(options daemonOptions, command string) []string {
	args := []string{}
	if options.Config != "" {
		args = append(args, "--config", options.Config)
	}
	return append(args, command, options.Task)
}

func daemonNextDue(ctx context.Context, options daemonOptions) (time.Time, error) {
	if options.Every > 0 {
		return time.Now().Add(options.Every), nil
	}
	path, err := exec.LookPath("tmux-a2a-postman")
	if err != nil {
		return time.Time{}, err
	}
	cmd := exec.CommandContext(ctx, path, daemonChildArgs(options, "schedule-next")...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return time.Time{}, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return time.Time{}, err
	}
	output, readErr := io.ReadAll(io.LimitReader(stdout, maximumScheduleResponseBytes+1))
	if len(output) > maximumScheduleResponseBytes {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return time.Time{}, errors.New("schedule-next response exceeds 4096 bytes")
	}
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return time.Time{}, readErr
	}
	if err := cmd.Wait(); err != nil {
		return time.Time{}, err
	}
	var result scheduleResult
	if err := json.Unmarshal(output, &result); err != nil || result.Command != options.Task {
		return time.Time{}, errors.New("invalid schedule-next response")
	}
	every, err := time.ParseDuration(result.Every)
	if err != nil || every < minimumDaemonInterval {
		return time.Time{}, errors.New("schedule-next interval must be at least 1s")
	}
	due, err := time.Parse(time.RFC3339Nano, result.NextDueAt)
	if err != nil {
		return time.Time{}, errors.New("invalid schedule-next due time")
	}
	return boundedDaemonDue(time.Now(), due, every), nil
}

// A current CLI may return a near-future due time. Keep its later schedule,
// but never let an old resident daemon start two work children too closely.
func boundedDaemonDue(now, due time.Time, every time.Duration) time.Time {
	if !due.After(now) {
		// A slow query can consume the whole interval. Rebase instead of spin.
		return now.Add(every)
	}
	if minimum := now.Add(minimumDaemonInterval); due.Before(minimum) {
		return minimum
	}
	return due
}

func daemonRunChild(ctx context.Context, options daemonOptions) error {
	path, err := exec.LookPath("tmux-a2a-postman")
	if err != nil {
		return err
	}
	args := []string{}
	if options.Config != "" {
		args = append(args, "--config", options.Config)
	}
	args = append(args, options.Task)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = strings.NewReader("") // current CLI loads the selected default
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return daemonChildExitError{commandExitError{code: exit.ExitCode()}}
		}
		return err
	}
	return nil
}

func daemonLoop(ctx context.Context, next func(context.Context) (time.Time, error), run func(context.Context) error) error {
	for {
		due, err := next(ctx)
		if err != nil {
			return err
		}
		wait := time.Until(due)
		if wait < 0 {
			wait = 0
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if err := run(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
}

// RunDaemon is intentionally only a foreground scheduler. It never opens the
// config, mail store, GitHub client or agent runtime, and never interprets the
// child command's output.
func RunDaemon(args []string) error {
	options, err := parseDaemonArgs(args)
	if err != nil {
		return err
	}
	ctx, stop := defaultInterruptContext()
	defer stop()
	return daemonLoop(ctx, func(ctx context.Context) (time.Time, error) {
		return daemonNextDue(ctx, options)
	}, func(ctx context.Context) error {
		return daemonRunChild(ctx, options)
	})
}
