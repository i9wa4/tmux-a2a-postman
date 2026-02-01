package main

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// SECURITY NOTE: Template expansion is a trust boundary.
// Templates are loaded exclusively from config files (user-controlled).
// Templates are NEVER sourced from message content or external input.
// Shell command execution via $(...) is permitted only in config-defined templates.

var (
	// Matches {variable} patterns
	variablePattern = regexp.MustCompile(`\{([^}]+)\}`)

	// Matches $(command) patterns (outermost only, no nested support)
	shellCommandPattern = regexp.MustCompile(`\$\(([^)]+)\)`)
)

// ExpandVariables replaces {variable} patterns with values from the vars map.
// Undefined variables remain as-is in the output.
func ExpandVariables(template string, vars map[string]string) string {
	return variablePattern.ReplaceAllStringFunc(template, func(match string) string {
		// Extract variable name (without braces)
		varName := match[1 : len(match)-1]
		if value, ok := vars[varName]; ok {
			return value
		}
		// Undefined variable: keep as-is
		return match
	})
}

// ExpandShellCommands executes $(command) patterns via sh -c.
// Each command runs with the given timeout. On error or timeout,
// the expansion is replaced with an empty string.
// Trailing newlines are stripped from command output.
func ExpandShellCommands(template string, timeout time.Duration) string {
	return shellCommandPattern.ReplaceAllStringFunc(template, func(match string) string {
		// Extract command (without $(...))
		cmd := match[2 : len(match)-1]

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		out, err := exec.CommandContext(ctx, "sh", "-c", cmd).Output()
		if err != nil {
			// Command failed or timed out: return empty string
			return ""
		}

		// Strip trailing newlines
		return strings.TrimRight(string(out), "\n")
	})
}

// ExpandTemplate performs full template expansion:
// 1. Execute shell commands $(...)
// 2. Expand variables {variable}
func ExpandTemplate(template string, vars map[string]string, timeout time.Duration) string {
	// First expand shell commands
	expanded := ExpandShellCommands(template, timeout)
	// Then expand variables
	return ExpandVariables(expanded, vars)
}
