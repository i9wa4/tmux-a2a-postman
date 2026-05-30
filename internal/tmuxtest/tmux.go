package tmuxtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type Pane struct {
	ID             string
	SessionName    string
	Title          string
	ContextID      string
	CurrentCommand string
	Capture        string
	WindowIndex    string
	PaneIndex      string
	SessionID      string
}

type Command struct {
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
}

type Option func(*fakeTmuxConfig)

type FakeTmux struct {
	tb      testing.TB
	Dir     string
	BinPath string
	LogPath string
}

type fakeTmuxConfig struct {
	panes    []Pane
	commands []Command
}

func Install(tb testing.TB, opts ...Option) *FakeTmux {
	tb.Helper()

	cfg := fakeTmuxConfig{}
	for _, opt := range opts {
		opt(&cfg)
	}
	for idx := range cfg.panes {
		cfg.panes[idx] = normalizePane(cfg.panes[idx], idx)
	}

	dir := tb.TempDir()
	fake := &FakeTmux{
		tb:      tb,
		Dir:     dir,
		BinPath: filepath.Join(dir, "tmux"),
		LogPath: filepath.Join(dir, "tmux.log"),
	}

	commands := append([]Command(nil), cfg.commands...)
	commands = append(commands, defaultCommands(cfg.panes)...)
	if err := os.WriteFile(fake.BinPath, []byte(fakeScript(fake.LogPath, commands)), 0o755); err != nil {
		tb.Fatalf("WriteFile fake tmux: %v", err)
	}
	tb.Setenv("PATH", prependPath(dir, os.Getenv("PATH")))
	return fake
}

func InstallMissing(tb testing.TB) *FakeTmux {
	tb.Helper()

	dir := tb.TempDir()
	tb.Setenv("PATH", dir)
	return &FakeTmux{
		tb:      tb,
		Dir:     dir,
		BinPath: filepath.Join(dir, "tmux"),
		LogPath: filepath.Join(dir, "tmux.log"),
	}
}

func WithPane(pane Pane) Option {
	return func(cfg *fakeTmuxConfig) {
		cfg.panes = append(cfg.panes, pane)
	}
}

func WithCommand(command Command) Option {
	return func(cfg *fakeTmuxConfig) {
		cfg.commands = append(cfg.commands, command)
	}
}

func (f *FakeTmux) Invocations() []string {
	f.tb.Helper()

	data, err := os.ReadFile(f.LogPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		f.tb.Fatalf("ReadFile fake tmux log: %v", err)
	}
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func normalizePane(pane Pane, idx int) Pane {
	if pane.ID == "" {
		pane.ID = fmt.Sprintf("%%%d", 99+idx)
	}
	if pane.SessionName == "" {
		pane.SessionName = "test-session"
	}
	if pane.Title == "" {
		pane.Title = "worker"
	}
	if pane.CurrentCommand == "" {
		pane.CurrentCommand = "bash"
	}
	if pane.WindowIndex == "" {
		pane.WindowIndex = "0"
	}
	if pane.PaneIndex == "" {
		pane.PaneIndex = fmt.Sprintf("%d", idx)
	}
	if pane.SessionID == "" {
		pane.SessionID = fmt.Sprintf("$%d", idx)
	}
	return pane
}

func defaultCommands(panes []Pane) []Command {
	if len(panes) == 0 {
		return nil
	}

	commands := []Command{}
	first := panes[0]
	for _, query := range []struct {
		format string
		value  string
	}{
		{format: "#{session_name}", value: first.SessionName + "\n"},
		{format: "#{pane_title}", value: first.Title + "\n"},
		{format: "#{pane_id}", value: first.ID + "\n"},
		{format: "#{pane_current_command}", value: first.CurrentCommand + "\n"},
	} {
		commands = append(commands, Command{
			Args:   []string{"display-message", "-p", query.format},
			Stdout: query.value,
		})
	}

	for _, pane := range panes {
		for _, query := range []struct {
			format string
			value  string
		}{
			{format: "#{session_name}", value: pane.SessionName + "\n"},
			{format: "#{pane_title}", value: pane.Title + "\n"},
			{format: "#{pane_id}", value: pane.ID + "\n"},
			{format: "#{pane_current_command}", value: pane.CurrentCommand + "\n"},
		} {
			commands = append(commands, Command{
				Args:   []string{"display-message", "-t", pane.ID, "-p", query.format},
				Stdout: query.value,
			})
		}
		commands = append(
			commands,
			Command{Args: []string{"capture-pane", "-p", "-t", pane.ID}, Stdout: pane.Capture},
			Command{Args: []string{"capture-pane", "-p", "-t", pane.ID, "-S", "-100"}, Stdout: pane.Capture},
		)
	}

	for _, format := range []string{
		"#{pane_id}\t#{@a2a_context_id}\t#{session_name}\t#{pane_title}",
		"#{pane_id}\t#{@a2a_context_id}\t#{session_name}\t#{pane_title} ",
		"#{pane_id}\t#{pane_current_command}",
		"#{pane_id} #{session_name} #{pane_title}",
		"#{window_index}\t#{pane_index}\t#{pane_id}\t#{pane_title}\t#{pane_current_command}",
	} {
		commands = append(commands, Command{
			Args:   []string{"list-panes", "-a", "-F", format},
			Stdout: renderPaneList(panes, format, ""),
		})
	}

	sessionsSeen := map[string]bool{}
	for _, pane := range panes {
		if sessionsSeen[pane.SessionName] {
			continue
		}
		sessionsSeen[pane.SessionName] = true
		commands = append(commands, Command{
			Args:   []string{"list-panes", "-s", "-t", pane.SessionName, "-F", "#{pane_id} #{pane_title}"},
			Stdout: renderPaneList(panes, "#{pane_id} #{pane_title}", pane.SessionName),
		})
	}

	commands = append(commands, Command{
		Args:   []string{"list-sessions", "-F", "#{session_name}\t#{session_id}"},
		Stdout: renderSessions(panes),
	})
	return commands
}

func renderPaneList(panes []Pane, format, sessionName string) string {
	lines := []string{}
	for _, pane := range panes {
		if sessionName != "" && pane.SessionName != sessionName {
			continue
		}
		lines = append(lines, renderPaneFormat(format, pane))
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func renderSessions(panes []Pane) string {
	lines := []string{}
	seen := map[string]bool{}
	for _, pane := range panes {
		if seen[pane.SessionName] {
			continue
		}
		seen[pane.SessionName] = true
		lines = append(lines, renderPaneFormat("#{session_name}\t#{session_id}", pane))
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func renderPaneFormat(format string, pane Pane) string {
	replacements := map[string]string{
		"#{pane_id}":              pane.ID,
		"#{@a2a_context_id}":      pane.ContextID,
		"#{session_name}":         pane.SessionName,
		"#{pane_title}":           pane.Title,
		"#{pane_current_command}": pane.CurrentCommand,
		"#{window_index}":         pane.WindowIndex,
		"#{pane_index}":           pane.PaneIndex,
		"#{session_id}":           pane.SessionID,
	}
	for old, value := range replacements {
		format = strings.ReplaceAll(format, old, value)
	}
	return format
}

func fakeScript(logPath string, commands []Command) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("printf '%s\\n' \"$*\" >> " + shQuote(logPath) + "\n")
	for _, command := range commands {
		b.WriteString("if [ \"$*\" = " + shQuote(strings.Join(command.Args, " ")) + " ]; then\n")
		if command.Stdout != "" {
			b.WriteString("  printf '%s' " + shQuote(command.Stdout) + "\n")
		}
		if command.Stderr != "" {
			b.WriteString("  printf '%s' " + shQuote(command.Stderr) + " >&2\n")
		}
		fmt.Fprintf(&b, "  exit %d\n", command.ExitCode)
		b.WriteString("fi\n")
	}
	b.WriteString("case \"$1\" in\n")
	b.WriteString("  set-buffer|paste-buffer|send-keys|set-option) exit 0 ;;\n")
	b.WriteString("esac\n")
	b.WriteString("printf '%s\\n' \"unexpected tmux command: $*\" >&2\n")
	b.WriteString("exit 1\n")
	return b.String()
}

func shQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func prependPath(dir, path string) string {
	if path == "" {
		return dir
	}
	return dir + string(os.PathListSeparator) + path
}
