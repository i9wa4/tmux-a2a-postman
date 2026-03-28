package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
)

// runGetSessionStatusOneline shows all tmux sessions' pane status in one line.
// statusDot returns the status indicator string for a pane.
// When isTerminal is true, returns a lipgloss-styled ANSI dot.
// When isTerminal is false, returns a plain emoji suitable for tmux #() output.
// lipgloss's own color detection is intentionally bypassed here because #() contexts
// require plain text regardless of color capability. (Issue #275)
func statusDot(status string, isTerminal bool) string {
	if isTerminal {
		activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
		pendingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("51"))
		composingStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("33"))
		spinningStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
		staleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
		userInputStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
		switch status {
		case "ready", "active":
			return activeStyle.Render("●")
		case "pending":
			return pendingStyle.Render("●")
		case "composing":
			return composingStyle.Render("●")
		case "spinning", "idle":
			return spinningStyle.Render("●")
		case "user_input":
			return userInputStyle.Render("●")
		default:
			return staleStyle.Render("●")
		}
	}
	switch status {
	case "ready", "active":
		return "🟢"
	case "pending":
		return "🔷"
	case "composing":
		return "🔵"
	case "spinning", "idle":
		return "🟡"
	case "user_input":
		return "🟣"
	default:
		return "🔴"
	}
}

// isShellCommand returns true if cmd is a known interactive shell name.
// Used by runGetSessionStatusOneline to exclude panes with no AI running (Issue #312).
var shellCommands = map[string]bool{
	"bash": true, "zsh": true, "sh": true, "fish": true,
	"dash": true, "ksh": true, "csh": true, "tcsh": true, "nu": true,
}

func isShellCommand(cmd string) bool {
	return shellCommands[cmd]
}

// Output format: [0]window0_panes:window1_panes:... [1]window0_panes:...
// TTY output (interactive terminal): ANSI-colored dots (● green/blue/yellow/red)
// Non-TTY output (tmux #(), pipes): plain emoji (🟢/🔵/🟡/🔴)
// Pane status: active=green, composing=blue, idle/spinning=yellow, stale=red
// Issue #120: Refactored to use idle.go activity detection instead of #{pane_active}
// Issue #275: TTY detection so tmux status-right receives plain emoji, not ANSI codes
// Issue #312: Filter panes by pane_current_command; fix session index stability.
func runGetSessionStatusOneline(args []string) error {
	fs := flag.NewFlagSet("get-session-status-oneline", flag.ContinueOnError)
	// Options struct fields (--params scope): json
	// SYNC: schema get-session-status-oneline properties; alwaysExcludedParams map
	jsonOut := fs.Bool("json", false, `output json: {"status":"..."}`)
	paramsFlag := fs.String("params", "", "command parameters as JSON or shorthand (k=v,k=v)")
	commandName := fs.Name()
	// Step 1: parse flags
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Step 2: record explicitly-set flags (for --params precedence)
	explicitlySet := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) {
		explicitlySet[f.Name] = true
	})
	// Steps 3+4: parse and apply --params to non-explicit flags
	if explicitlySet["params"] {
		resolvedParams, err := parseParams(*paramsFlag)
		if err != nil {
			return err
		}
		if err := applyParams(fs, resolvedParams, explicitlySet, commandName); err != nil {
			return err
		}
	}

	// Load config to get base directory
	cfg, err := config.LoadConfig("")
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	// Find the most recently started live context for the current tmux session.
	// Context directories are named YYYYMMDD-HHMMSS-XXXX; lexicographic
	// descending sort gives newest first.
	statusPriority := map[string]int{"active": 2, "idle": 1, "stale": 0}
	paneActivity := make(map[string]string)

	contextDirs, _ := filepath.Glob(filepath.Join(baseDir, "[0-9]*"))
	sort.Sort(sort.Reverse(sort.StringSlice(contextDirs)))

	var liveStateFiles []string
	var liveCtxSessionPairs [][2]string // [ctxDir, sessionSubdir]
	paneActivityAdded := make(map[string]bool)
	for _, ctxDir := range contextDirs {
		fi, err := os.Stat(ctxDir)
		if err != nil || !fi.IsDir() {
			continue
		}
		ctxName := filepath.Base(ctxDir)
		// Scan all session subdirs for any live postman.pid.
		sessionEntries, _ := os.ReadDir(ctxDir)
		for _, se := range sessionEntries {
			if !se.IsDir() {
				continue
			}
			if config.IsSessionPIDAlive(baseDir, ctxName, se.Name()) {
				if !paneActivityAdded[ctxDir] {
					liveStateFiles = append(liveStateFiles, filepath.Join(ctxDir, "pane-activity.json"))
					paneActivityAdded[ctxDir] = true
				}
				liveCtxSessionPairs = append(liveCtxSessionPairs, [2]string{ctxDir, se.Name()})
				// NOTE: no break — collect ALL live session subdirs for waiting-file overlay (#285)
			}
		}
	}

	if len(liveStateFiles) == 0 {
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(struct {
				Status string `json:"status"`
			}{Status: ""})
		}
		return nil // no live context found
	}

	for _, liveStateFile := range liveStateFiles {
		stateData, err := os.ReadFile(liveStateFile)
		if err == nil {
			// Issue #123: Dual-format reader — supports both legacy map[string]string and
			// new map[string]PaneActivityExport formats.
			var rawMap map[string]json.RawMessage
			if jsonErr := json.Unmarshal(stateData, &rawMap); jsonErr == nil {
				for paneID, raw := range rawMap {
					var status string
					// Try legacy format: plain string value
					if err := json.Unmarshal(raw, &status); err != nil {
						// Try new format: PaneActivityExport struct
						var export idle.PaneActivityExport
						if err := json.Unmarshal(raw, &export); err != nil {
							continue // skip on schema mismatch
						}
						status = export.Status
					}
					if status == "" {
						continue
					}
					existing, exists := paneActivity[paneID]
					if !exists || statusPriority[status] > statusPriority[existing] {
						paneActivity[paneID] = status // higher priority wins on conflict
					}
				}
			}
		}
	}

	// Build edge node set and pane title map for filtering.
	// Issue #312: also capture pane_current_command to detect shell-only panes.
	// Edge node names are always single-word identifiers, so SplitN(4) safely
	// captures all four fields: pane_id, session_name, pane_title, pane_current_command.
	edgeNodes := config.GetEdgeNodeNames(cfg.Edges)
	paneTitleOutput, _ := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id} #{session_name} #{pane_title} #{pane_current_command}").Output()
	paneTitles := make(map[string]string)           // paneID -> paneTitle (for edge filter)
	sessionTitleToPaneID := make(map[string]string) // "sessionName:paneTitle" -> paneID (for waiting overlay, #285)
	paneCurrentCmd := make(map[string]string)       // paneID -> current command name (for shell filter, #312)
	for _, line := range strings.Split(strings.TrimSpace(string(paneTitleOutput)), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), " ", 4)
		if len(parts) >= 3 && parts[0] != "" && parts[2] != "" {
			paneID, sessionName, title := parts[0], parts[1], parts[2]
			paneTitles[paneID] = title
			sessionTitleToPaneID[sessionName+":"+title] = paneID
			if len(parts) == 4 {
				paneCurrentCmd[paneID] = parts[3]
			}
		}
	}

	// Overlay waiting-file states onto paneActivity (Issue #285).
	// waiting/*.md files carry "composing", "spinning", "stuck", "user_input" states
	// that are never present in pane-activity.json. This mirrors the TUI's
	// effectiveNodeState merge (tui.go:260).
	applyWaitingOverlay(liveCtxSessionPairs, sessionTitleToPaneID, paneActivity)
	applyPendingOverlay(liveCtxSessionPairs, sessionTitleToPaneID, paneActivity)

	// Get all tmux sessions sorted alphabetically by name to match the tmux
	// choose-tree default sort order (prefix-s uses choose-tree -Zs without -O,
	// so it defaults to name sort). The displayed index is the sequential
	// position in this sorted list INCLUDING hidden sessions (those with no
	// agent-tracked panes produce no output), so gaps like [1][3] are expected
	// and reflect the full chooser position. Index shifts when earlier sessions
	// are removed (Issue #312, #349). #{session_index} is unsupported on tmux 3.6a.
	sessionsOutput, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		// Check if no server running
		if strings.Contains(string(sessionsOutput), "no server running") {
			if *jsonOut {
				return json.NewEncoder(os.Stdout).Encode(struct {
					Status string `json:"status"`
				}{Status: ""})
			}
			return nil
		}
		return fmt.Errorf("listing sessions: %w", err)
	}

	type sessionEntry struct {
		name string
	}
	var sessions []sessionEntry
	for _, line := range strings.Split(strings.TrimSpace(string(sessionsOutput)), "\n") {
		if line == "" {
			continue
		}
		sessions = append(sessions, sessionEntry{name: line})
	}
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].name < sessions[j].name
	})
	if len(sessions) == 0 {
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(struct {
				Status string `json:"status"`
			}{Status: ""})
		}
		return nil
	}

	// Issue #275: Use plain emoji when stdout is not a TTY (e.g. tmux status-right #()).
	isTerminal := term.IsTerminal(os.Stdout.Fd())

	var output []string

	for i, sess := range sessions {
		sessionName := sess.name

		// Get all windows in this session
		windowsOutput, err := exec.Command("tmux", "list-windows", "-t", sessionName, "-F", "#{window_index}").Output()
		if err != nil {
			return fmt.Errorf("listing windows for session %s: %w", sessionName, err)
		}

		windows := strings.Split(strings.TrimSpace(string(windowsOutput)), "\n")
		var windowStatuses []string

		for _, windowIndex := range windows {
			if windowIndex == "" {
				continue
			}

			// Get all panes in this window with their IDs
			target := fmt.Sprintf("%s:%s", sessionName, windowIndex)
			panesOutput, err := exec.Command("tmux", "list-panes", "-t", target, "-F", "#{pane_id}").Output()
			if err != nil {
				return fmt.Errorf("listing panes for %s: %w", target, err)
			}

			panes := strings.Split(strings.TrimSpace(string(panesOutput)), "\n")
			var paneStatuses string

			for _, paneID := range panes {
				if paneID == "" {
					continue
				}
				// Edge filter: only show panes in edge list
				if !edgeNodes[paneTitles[paneID]] {
					continue
				}
				// #312: Skip panes with no running agent.
				// (a) Pane not in paneActivity: daemon hasn't captured it yet.
				// (b) Pane's foreground process is a shell: no AI launched there.
				if _, tracked := paneActivity[paneID]; !tracked {
					continue
				}
				if isShellCommand(paneCurrentCmd[paneID]) {
					continue
				}
				paneStatuses += statusDot(paneActivity[paneID], isTerminal)
			}

			if paneStatuses != "" {
				windowStatuses = append(windowStatuses, paneStatuses)
			}
		}

		// Build session status: [N]window0:window1:...
		if len(windowStatuses) > 0 {
			sessionStatus := fmt.Sprintf("[%d]%s", i, strings.Join(windowStatuses, ":"))
			output = append(output, sessionStatus)
		}
	}

	if len(output) > 0 {
		statusStr := strings.Join(output, " ")
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(struct {
				Status string `json:"status"`
			}{Status: statusStr})
		}
		fmt.Println(statusStr)
		return nil
	}
	if *jsonOut {
		return json.NewEncoder(os.Stdout).Encode(struct {
			Status string `json:"status"`
		}{Status: ""})
	}
	return nil
}

// applyWaitingOverlay scans waiting/ dirs in liveCtxSessionPairs and overlays
// their states onto paneActivity in-place (Issue #285).
// sessionTitleToPaneID maps "sessionName:paneTitle" -> paneID.
// Priority mirrors daemon.go:998-1003: higher rank = worse state = wins.
// waitingOverlayRank defines overlay priority for waiting/ and inbox/ state display.
// Higher rank = worse state = takes visual priority.
// "ready", "idle", "stale" are absent (default 0); any rank >= 1 overrides them.
var waitingOverlayRank = map[string]int{
	"user_input": 0,
	"pending":    1,
	"composing":  2,
	"spinning":   3,
	"stalled":    4,
}

func applyWaitingOverlay(
	liveCtxSessionPairs [][2]string,
	sessionTitleToPaneID map[string]string,
	paneActivity map[string]string,
) {
	for _, pair := range liveCtxSessionPairs {
		ctxDir, sessionSubdir := pair[0], pair[1]
		waitingDir := filepath.Join(ctxDir, sessionSubdir, "waiting")
		entries, err := os.ReadDir(waitingDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			fileInfo, parseErr := message.ParseMessageFilename(entry.Name())
			if parseErr != nil {
				continue // malformed filename: skip silently (mirrors daemon.go:1032-1034)
			}
			content, readErr := os.ReadFile(filepath.Join(waitingDir, entry.Name()))
			if readErr != nil {
				continue
			}
			cs := string(content)
			var fileState string
			switch {
			case strings.Contains(cs, "state: stalled"), strings.Contains(cs, "state: stuck"):
				fileState = "stalled"
			case strings.Contains(cs, "state: spinning"):
				fileState = "spinning"
			case strings.Contains(cs, "state: composing"):
				fileState = "composing"
			case strings.Contains(cs, "state: user_input"):
				fileState = "user_input"
			default:
				continue
			}
			// sessionSubdir is the tmux session name; fileInfo.To is the recipient node name.
			// Color the RECIPIENT's dot — the node expected to reply.
			recipientKey := nodeaddr.Full(fileInfo.To, sessionSubdir)
			paneID, ok := sessionTitleToPaneID[recipientKey]
			if !ok {
				continue
			}
			if waitingOverlayRank[fileState] >= waitingOverlayRank[paneActivity[paneID]] {
				paneActivity[paneID] = fileState
			}
		}
	}
}

// applyPendingOverlay overlays "pending" state onto paneActivity
// for any node that has unarchived messages in its inbox/ subdirectory.
// Mirrors applyWaitingOverlay signature for composability.
func applyPendingOverlay(
	liveCtxSessionPairs [][2]string,
	sessionTitleToPaneID map[string]string,
	paneActivity map[string]string,
) {
	for _, pair := range liveCtxSessionPairs {
		ctxDir, sessionSubdir := pair[0], pair[1]
		inboxBase := filepath.Join(ctxDir, sessionSubdir, "inbox")
		nodeDirs, err := os.ReadDir(inboxBase)
		if err != nil {
			continue
		}
		for _, nodeDir := range nodeDirs {
			if !nodeDir.IsDir() {
				continue
			}
			nodeName := nodeDir.Name()
			entries, err := os.ReadDir(filepath.Join(inboxBase, nodeName))
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if !strings.HasSuffix(entry.Name(), ".md") {
					continue
				}
				recipientKey := sessionSubdir + ":" + nodeName
				paneID, ok := sessionTitleToPaneID[recipientKey]
				if !ok {
					break
				}
				if waitingOverlayRank["pending"] >= waitingOverlayRank[paneActivity[paneID]] {
					paneActivity[paneID] = "pending"
				}
				break // one message is enough to mark pending
			}
		}
	}
}
