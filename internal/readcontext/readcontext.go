package readcontext

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var supportedPieces = map[string]bool{
	"time":    true,
	"node":    true,
	"cwd":     true,
	"git":     true,
	"add_dir": true,
}

type Entry struct {
	Name  string
	Value string
}

type Options struct {
	Now          time.Time
	NodeName     string
	CWD          string
	CommandLines []string
}

func IsSupportedPiece(name string) bool {
	return supportedPieces[name]
}

func CurrentOptions(nodeName string) Options {
	cwd, _ := os.Getwd()
	return Options{
		Now:          time.Now(),
		NodeName:     nodeName,
		CWD:          cwd,
		CommandLines: ancestorCommandLines(),
	}
}

func BuildBlock(heading string, pieces []string, opts Options) string {
	entries := Collect(pieces, opts)
	if len(entries) == 0 {
		return ""
	}
	if heading == "" {
		heading = "Local Runtime Context"
	}
	var lines []string
	lines = append(lines, "## "+heading)
	lines = append(lines, "")
	for _, entry := range entries {
		lines = append(lines, "- "+entry.Name+": "+entry.Value)
	}
	return strings.Join(lines, "\n")
}

func Collect(pieces []string, opts Options) []Entry {
	var entries []Entry
	for _, piece := range pieces {
		value := collectPiece(piece, opts)
		if value == "" {
			continue
		}
		entries = append(entries, Entry{Name: piece, Value: value})
	}
	return entries
}

func collectPiece(piece string, opts Options) string {
	switch piece {
	case "time":
		now := opts.Now
		if now.IsZero() {
			now = time.Now()
		}
		return now.Format(time.RFC3339)
	case "node":
		return strings.TrimSpace(opts.NodeName)
	case "cwd":
		return strings.TrimSpace(opts.CWD)
	case "git":
		return gitSummary(opts.CWD)
	case "add_dir":
		return strings.Join(extractAddDirs(opts.CommandLines), ", ")
	default:
		return ""
	}
}

func gitSummary(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	if err := exec.Command("git", "-C", cwd, "rev-parse", "--is-inside-work-tree").Run(); err != nil {
		return ""
	}
	branchOut, err := exec.Command("git", "-C", cwd, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	branch := strings.TrimSpace(string(branchOut))
	if branch == "" {
		return ""
	}
	statusOut, err := exec.Command("git", "-C", cwd, "status", "--porcelain").Output()
	if err != nil {
		return ""
	}
	state := "clean"
	if strings.TrimSpace(string(statusOut)) != "" {
		state = "dirty"
	}
	return branch + " (" + state + ")"
}

func extractAddDirs(commandLines []string) []string {
	var dirs []string
	seen := make(map[string]bool)
	for _, line := range commandLines {
		fields := strings.Fields(line)
		for i := 0; i < len(fields); i++ {
			field := fields[i]
			if field == "--add-dir" {
				if i+1 >= len(fields) {
					continue
				}
				dir := strings.TrimSpace(fields[i+1])
				if dir != "" && !seen[dir] {
					seen[dir] = true
					dirs = append(dirs, dir)
				}
				i++
				continue
			}
			if strings.HasPrefix(field, "--add-dir=") {
				dir := strings.TrimSpace(strings.TrimPrefix(field, "--add-dir="))
				if dir != "" && !seen[dir] {
					seen[dir] = true
					dirs = append(dirs, dir)
				}
			}
		}
	}
	return dirs
}

func ancestorCommandLines() []string {
	var lines []string
	seen := make(map[int]bool)
	pid := os.Getpid()
	for pid > 1 && !seen[pid] {
		seen[pid] = true
		nextPID, commandLine, ok := readProcessCommandLine(pid)
		if !ok {
			break
		}
		if strings.TrimSpace(commandLine) != "" {
			lines = append(lines, commandLine)
		}
		pid = nextPID
	}
	return lines
}

func readProcessCommandLine(pid int) (int, string, bool) {
	out, err := exec.Command("ps", "-o", "pid=,ppid=,command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, "", false
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return 0, "", false
	}
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return 0, "", false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, "", false
	}
	command := strings.Join(fields[2:], " ")
	return ppid, command, true
}
