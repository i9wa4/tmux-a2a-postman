package tmuxrunner

import (
	"context"
	"fmt"
	"os/exec"
	"time"
)

// Runner runs a tmux subcommand and returns its combined output.
type Runner func(args ...string) ([]byte, error)

// Command runs tmux commands. The zero value executes the tmux binary with no
// timeout, preserving current exec.Command(...).CombinedOutput semantics.
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

// CombinedOutput executes the configured tmux command with the given arguments.
func (c Command) CombinedOutput(args ...string) ([]byte, error) {
	binary := c.Binary
	if binary == "" {
		binary = "tmux"
	}

	if c.Timeout <= 0 {
		return exec.Command(binary, args...).CombinedOutput()
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if ctx.Err() != nil {
		return out, fmt.Errorf("tmux command timed out after %s: %w", c.Timeout, ctx.Err())
	}
	return out, err
}

// Output executes the configured tmux command with the given arguments and
// returns stdout only.
func (c Command) Output(args ...string) ([]byte, error) {
	binary := c.Binary
	if binary == "" {
		binary = "tmux"
	}

	if c.Timeout <= 0 {
		return exec.Command(binary, args...).Output()
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, binary, args...).Output()
	if ctx.Err() != nil {
		return out, fmt.Errorf("tmux command timed out after %s: %w", c.Timeout, ctx.Err())
	}
	return out, err
}

// Run executes the configured tmux command with the given arguments.
func (c Command) Run(args ...string) error {
	binary := c.Binary
	if binary == "" {
		binary = "tmux"
	}

	if c.Timeout <= 0 {
		return exec.Command(binary, args...).Run()
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
	defer cancel()

	err := exec.CommandContext(ctx, binary, args...).Run()
	if ctx.Err() != nil {
		return fmt.Errorf("tmux command timed out after %s: %w", c.Timeout, ctx.Err())
	}
	return err
}
