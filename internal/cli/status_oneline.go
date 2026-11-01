package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

// Capture freshness is independent of the 1/5/15-minute screen-change bands.
// This foreground-only observer has no evidence of activity between captures.
const operatorCaptureFreshness = 2 * time.Minute

// RunWorkspaceGetStatusOneline is an explicit human operator view of one
// established tmux server. It requires no agent pane/caller identity.
func RunWorkspaceGetStatusOneline(args []string) (retErr error) {
	var rows []workspace.OperatorNode
	defer func() {
		line, diagnostics := renderStatusOneline(rows, time.Now().UTC())
		_, outputErr := fmt.Fprintln(os.Stdout, line)
		for _, diagnostic := range diagnostics {
			_, _ = fmt.Fprintln(os.Stderr, diagnostic)
		}
		if outputErr != nil {
			retErr = outputErr
		}
	}()
	f := newWorkspaceFlags("get-status-oneline")
	path, err := f.parse(args)
	if err != nil {
		return err
	}
	if f.fs.NArg() != 0 {
		return errors.New("get-status-oneline takes no positional arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	topology, err := config.LoadWorkspaceTopology(ctx, path)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	stateRoot, err := workspace.DefaultStateRoot(home, os.Getenv("XDG_STATE_HOME"))
	if err != nil {
		return err
	}
	socket, _, ok := strings.Cut(os.Getenv("TMUX"), ",")
	if !ok || !filepath.IsAbs(socket) {
		return errors.New("operator overview requires an explicit native TMUX socket; pass #{socket_path} in the tmux status invocation")
	}
	mail := workspace.Mail{Store: workspace.Store{StateRoot: stateRoot}}
	rows, err = mail.OperatorStatus(ctx, workspace.Tmux{Socket: socket, Home: home, Roles: topology.Nodes})
	return err
}

func statusGlyph(node workspace.NodeStatus, now time.Time) (string, string) {
	for _, count := range []*int{node.UnreadCount, node.InboundCount, node.OutboundCount} {
		if count == nil || *count < 0 {
			return "?", "mail counts unavailable or invalid"
		}
	}
	shape := 0
	if *node.UnreadCount > 0 || *node.InboundCount > 0 {
		shape = 1
	} else if *node.OutboundCount > 0 {
		shape = 2
	}
	palette := [3][5]string{
		{"🟢", "🟡", "🟠", "🔴", "⚫"},
		{"🟩", "🟨", "🟧", "🟥", "⬛"},
		{"💚", "💛", "🧡", "❤️", "🖤"},
	}
	unknown := func(reason string) (string, string) { return palette[shape][4], reason }
	if node.LastCaptureAt == nil {
		return unknown("capture unavailable")
	}
	captured, err := time.Parse(time.RFC3339Nano, *node.LastCaptureAt)
	if err != nil || captured.After(now) {
		return unknown("invalid or future capture timestamp")
	}
	if now.Sub(captured) >= operatorCaptureFreshness {
		return unknown("capture is at least two minutes old")
	}
	if node.LastScreenChangeAt == nil {
		return unknown("no comparable observed screen change")
	}
	changed, err := time.Parse(time.RFC3339Nano, *node.LastScreenChangeAt)
	if err != nil || changed.After(captured) || changed.After(now) {
		return unknown("invalid, future or inconsistent screen-change timestamp")
	}
	age := now.Sub(changed)
	color := 3
	switch {
	case age < time.Minute:
		color = 0
	case age < 5*time.Minute:
		color = 1
	case age < 15*time.Minute:
		color = 2
	}
	return palette[shape][color], ""
}

func renderStatusOneline(input []workspace.OperatorNode, now time.Time) (string, []string) {
	nodes := append([]workspace.OperatorNode(nil), input...)
	sort.Slice(nodes, func(i, j int) bool {
		a, b := nodes[i], nodes[j]
		if a.Session != b.Session {
			return a.Session < b.Session
		}
		if a.Window != b.Window {
			return a.Window < b.Window
		}
		if a.Top != b.Top {
			return a.Top < b.Top
		}
		if a.Left != b.Left {
			return a.Left < b.Left
		}
		return a.Pane < b.Pane
	})
	var output strings.Builder
	var diagnostics []string
	previousSession, previousWindow := -1, -1
	for _, node := range nodes {
		if node.Session != previousSession {
			if previousSession >= 0 {
				output.WriteByte(' ')
			}
			fmt.Fprintf(&output, "[%d]", node.Session)
		} else if node.Window != previousWindow {
			output.WriteByte(':')
		}
		glyph, reason := statusGlyph(node.Status, now)
		output.WriteString(glyph)
		for _, message := range []string{node.Status.Reason, reason} {
			if message != "" {
				diagnostics = append(diagnostics, fmt.Sprintf("session $%d pane %s (%s): %s", node.Session, node.Pane, node.Status.Node, message))
			}
		}
		previousSession, previousWindow = node.Session, node.Window
	}
	return output.String(), diagnostics
}
