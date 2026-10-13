package tmuxrunner

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// DefaultTimeout bounds every tmux invocation whose Command.Timeout is unset.
// A hung tmux server must not block a caller (notably the daemon's single
// dispatcher loop) indefinitely (#871).
//
// It is a variable only so tests can shrink it; production code must not
// assign to it.
var DefaultTimeout = 10 * time.Second

// Runner runs a tmux subcommand and returns its combined output.
type Runner func(args ...string) ([]byte, error)

// Command runs tmux commands. A non-positive Timeout means DefaultTimeout.
type Command struct {
	Binary  string
	Timeout time.Duration
}

// CombinedOutput executes tmux with the given arguments.
func CombinedOutput(args ...string) ([]byte, error) {
	return Command{}.CombinedOutput(args...)
}

// Output executes tmux with the given arguments and returns stdout only.
func Output(args ...string) ([]byte, error) {
	return Command{}.Output(args...)
}

// Run executes tmux with the given arguments and returns only the command error.
func Run(args ...string) error {
	return Command{}.Run(args...)
}

func (c Command) binary() string {
	if c.Binary == "" {
		return "tmux"
	}
	return c.Binary
}

func (c Command) effectiveTimeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return DefaultTimeout
}

// CombinedOutput executes the configured tmux command with the given arguments.
func (c Command) CombinedOutput(args ...string) ([]byte, error) {
	timeout := c.effectiveTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, c.binary(), args...).CombinedOutput()
	if ctx.Err() != nil {
		return out, fmt.Errorf("tmux command timed out after %s: %w", timeout, ctx.Err())
	}
	return out, err
}

// Output executes the configured tmux command with the given arguments and
// returns stdout only.
func (c Command) Output(args ...string) ([]byte, error) {
	timeout := c.effectiveTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, c.binary(), args...).Output()
	if ctx.Err() != nil {
		return out, fmt.Errorf("tmux command timed out after %s: %w", timeout, ctx.Err())
	}
	return out, err
}

// Run executes the configured tmux command with the given arguments.
func (c Command) Run(args ...string) error {
	timeout := c.effectiveTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err := exec.CommandContext(ctx, c.binary(), args...).Run()
	if ctx.Err() != nil {
		return fmt.Errorf("tmux command timed out after %s: %w", timeout, ctx.Err())
	}
	return err
}
