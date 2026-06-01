package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

type watchStatusTarget struct {
	contextID  string
	baseDir    string
	configPath string
}

type watchStatusRunOptions struct {
	Interval      time.Duration
	Format        string
	Severity      bool
	NoColor       bool
	NoClear       bool
	Collector     func() (status.AllSessionStatus, error)
	Now           func() time.Time
	IsTTY         func() bool
	MaxIterations int
}

type watchStatusTextOptions struct {
	Severity bool
	Color    bool
}

// RunWatchStatus renders a live, read-only all-session status view.
func RunWatchStatus(stdout io.Writer, args []string) error {
	fs := flag.NewFlagSet("watch-status", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from the active daemon)")
	configPath := fs.String("config", "", "Config file path")
	interval := fs.Duration("interval", 2*time.Second, "Refresh interval")
	outputFormat := fs.String("format", "text", "Output format: text or jsonl")
	severity := fs.Bool("severity", false, "Print compact contextual severity tokens in the text view")
	noColor := fs.Bool("no-color", false, "Disable ANSI color")
	noClear := fs.Bool("no-clear", false, "Append snapshots instead of clearing the terminal")
	if err := fs.Parse(args); err != nil {
		return err
	}

	target, err := resolveWatchStatusTarget(*contextID, *configPath)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return runWatchStatus(ctx, stdout, watchStatusRunOptions{
		Interval:  *interval,
		Format:    *outputFormat,
		Severity:  *severity,
		NoColor:   *noColor,
		NoClear:   *noClear,
		Collector: target.collect,
	})
}

func resolveWatchStatusTarget(contextIDFlag, configPath string) (watchStatusTarget, error) {
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return watchStatusTarget{}, fmt.Errorf("loading config: %w", err)
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	if contextIDFlag != "" {
		contextID, err := config.ResolveContextID(contextIDFlag)
		if err != nil {
			return watchStatusTarget{}, err
		}
		if ownerSession := config.FindContextSessionName(baseDir, contextID); ownerSession == "" {
			return watchStatusTarget{}, fmt.Errorf("no active postman daemon found for context %q in %s", contextID, baseDir)
		}
		return watchStatusTarget{contextID: contextID, baseDir: baseDir, configPath: configPath}, nil
	}

	if contextID, _, ok := config.FindCurrentUserDaemon(baseDir); ok {
		return watchStatusTarget{contextID: contextID, baseDir: baseDir, configPath: configPath}, nil
	}

	if sessionName := config.GetTmuxSessionName(); sessionName != "" {
		contextID, err := config.ResolveContextIDFromSession(baseDir, sessionName)
		if err == nil {
			return watchStatusTarget{contextID: contextID, baseDir: baseDir, configPath: configPath}, nil
		}
	}

	return watchStatusTarget{}, fmt.Errorf("no active postman daemon found in %s; start the daemon first", baseDir)
}

func (target watchStatusTarget) collect() (status.AllSessionStatus, error) {
	if ownerSession := config.FindContextSessionName(target.baseDir, target.contextID); ownerSession == "" {
		return status.AllSessionStatus{}, fmt.Errorf("no active postman daemon found for context %q in %s", target.contextID, target.baseDir)
	}

	snapshot, _, ok, err := collectAllSessionStatus(target.contextID, "", target.configPath)
	if err != nil {
		return status.AllSessionStatus{}, err
	}
	if !ok {
		return status.AllSessionStatus{}, fmt.Errorf("no active postman daemon found for context %q in %s", target.contextID, target.baseDir)
	}
	if snapshot.DaemonOwner == nil {
		return status.AllSessionStatus{}, fmt.Errorf("no active postman daemon found for context %q in %s", target.contextID, target.baseDir)
	}
	return snapshot, nil
}

func runWatchStatus(ctx context.Context, stdout io.Writer, opts watchStatusRunOptions) error {
	if opts.Collector == nil {
		return fmt.Errorf("watch-status collector is not configured")
	}
	if opts.Interval <= 0 {
		return fmt.Errorf("--interval must be greater than zero")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.IsTTY == nil {
		opts.IsTTY = func() bool { return outputIsTTY(stdout) }
	}
	switch opts.Format {
	case "text", "jsonl":
	default:
		return fmt.Errorf("--format must be text or jsonl")
	}

	isTTY := opts.IsTTY()
	useClear := opts.Format == "text" && isTTY && !opts.NoClear
	useColor := opts.Format == "text" && isTTY && !opts.NoColor

	for iteration := 0; ; iteration++ {
		snapshot, err := opts.Collector()
		if err != nil {
			return err
		}
		if opts.Format == "jsonl" {
			if err := json.NewEncoder(stdout).Encode(snapshot); err != nil {
				return err
			}
		} else {
			if useClear {
				if _, err := io.WriteString(stdout, "\033[H\033[2J"); err != nil {
					return err
				}
			} else if iteration > 0 {
				if _, err := io.WriteString(stdout, "\n"); err != nil {
					return err
				}
			}
			text := formatWatchStatusText(snapshot, opts.Now(), watchStatusTextOptions{
				Severity: opts.Severity,
				Color:    useColor,
			})
			if _, err := io.WriteString(stdout, text); err != nil {
				return err
			}
		}

		if opts.MaxIterations > 0 && iteration+1 >= opts.MaxIterations {
			return nil
		}

		timer := time.NewTimer(opts.Interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil
		case <-timer.C:
		}
	}
}

func outputIsTTY(w io.Writer) bool {
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func formatWatchStatusText(snapshot status.AllSessionStatus, now time.Time, opts watchStatusTextOptions) string {
	var b strings.Builder
	owner := "unknown"
	if snapshot.DaemonOwner != nil {
		owner = snapshot.DaemonOwner.ContextID + "/" + snapshot.DaemonOwner.SessionName
	}
	fmt.Fprintf(
		&b,
		"postman watch-status observed=%s context=%s daemon_owner=%s sessions=%d\n",
		now.UTC().Format(time.RFC3339),
		snapshot.ContextID,
		owner,
		len(snapshot.Sessions),
	)

	for idx, sessionStatus := range snapshot.Sessions {
		token := sessionStatus.Compact
		if opts.Severity && sessionStatus.CompactSeverity != "" {
			token = sessionStatus.CompactSeverity
		}
		if token == "" {
			token = "-"
		}
		severity := sessionStatus.Severity
		if severity == "" {
			severity = "ok"
		}
		token = colorizeWatchSeverity(token, severity, opts.Color)

		fmt.Fprintf(
			&b,
			"[%d] %s token=%s state=%s severity=%s nodes=%d queues=post:%d inbox:%d dead_letter:%d",
			idx,
			sessionStatus.SessionName,
			token,
			emptyDash(sessionStatus.VisibleState),
			severity,
			sessionStatus.NodeCount,
			sessionStatus.Queues.PostCount,
			sessionStatus.Queues.InboxCount,
			sessionStatus.Queues.DeadLetterCount,
		)
		if sessionStatus.Delivery != nil {
			fmt.Fprintf(&b, " delivery=%s", emptyDash(sessionStatus.Delivery.State))
			if sessionStatus.Delivery.OldestPostAgeSeconds > 0 {
				fmt.Fprintf(&b, " oldest_post_age=%ds", sessionStatus.Delivery.OldestPostAgeSeconds)
			}
		}
		b.WriteByte('\n')

		nodeLines := watchStatusNodeLines(sessionStatus.Nodes)
		if len(nodeLines) == 0 {
			b.WriteString("  nodes ok\n")
			continue
		}
		for _, line := range nodeLines {
			fmt.Fprintf(&b, "  - %s\n", line)
		}
	}
	return b.String()
}

func watchStatusNodeLines(nodes []status.NodeStatus) []string {
	lines := make([]string, 0, len(nodes))
	for _, node := range nodes {
		if !watchStatusShouldShowNode(node) {
			continue
		}
		severity := node.Severity
		if severity == "" {
			severity = "ok"
		}
		blockedCount := 0
		flowState := "-"
		if node.Flow != nil {
			flowState = emptyDash(node.Flow.State)
			blockedCount = node.Flow.Blocked.OpenCount
		}
		localState := "-"
		if node.NodeLocal != nil {
			localState = emptyDash(node.NodeLocal.State)
		}
		screenState := "-"
		if node.ScreenProgress != nil {
			screenState = emptyDash(node.ScreenProgress.EvidenceState)
		}
		lines = append(lines, fmt.Sprintf(
			"%s state=%s severity=%s inbox=%d input_required=%d waiting_on_input=%d blocked=%d flow=%s local=%s screen=%s",
			node.Name,
			emptyDash(node.VisibleState),
			severity,
			node.InboxCount,
			node.InputRequiredCount,
			node.WaitingOnInputCount,
			blockedCount,
			flowState,
			localState,
			screenState,
		))
	}
	return lines
}

func watchStatusShouldShowNode(node status.NodeStatus) bool {
	if node.InputRequiredCount > 0 || node.WaitingOnInputCount > 0 || node.InfoUnreadCount > 0 {
		return true
	}
	if status.StateRank(node.VisibleState) > status.StateRank("ready") {
		return true
	}
	if node.Severity != "" && status.SeverityRank(node.Severity) > status.SeverityRank("working") {
		return true
	}
	if node.Flow != nil && (node.Flow.State != "" && node.Flow.State != "idle" || node.Flow.Blocked.OpenCount > 0) {
		return true
	}
	if node.NodeLocal != nil && node.NodeLocal.State == "stale" {
		return true
	}
	return node.ScreenProgress != nil && node.ScreenProgress.EvidenceState == "stale"
}

func colorizeWatchSeverity(text, severity string, color bool) string {
	if !color {
		return text
	}
	switch {
	case status.SeverityRank(severity) >= status.SeverityRank("delivery_stuck"):
		return "\033[31m" + text + "\033[0m"
	case status.SeverityRank(severity) >= status.SeverityRank("needs_action"):
		return "\033[33m" + text + "\033[0m"
	case status.SeverityRank(severity) >= status.SeverityRank("working"):
		return "\033[36m" + text + "\033[0m"
	default:
		return "\033[32m" + text + "\033[0m"
	}
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}
