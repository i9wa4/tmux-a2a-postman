package cliutil

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/ping"
)

func ValidateOutboundNodeName(label, nodeName string) error {
	if binding.ValidateNodeName(nodeName) {
		return nil
	}
	return fmt.Errorf("%s %q: invalid node name (must match %s)", label, nodeName, binding.NodeNamePattern)
}

func ValidateNodeAddress(label, address string) error {
	if err := nodeaddr.Validate(address); err != nil {
		return fmt.Errorf("%s %q: %w", label, address, err)
	}
	return nil
}

// ResolveInboxPath resolves the inbox path for the current node (#196).
func ResolveInboxPath(args []string) (string, error) {
	fs := flag.NewFlagSet("inbox-resolve", flag.ContinueOnError)
	contextID := fs.String("context-id", "", "context ID")
	configPath := fs.String("config", "", "path to config file")
	if err := fs.Parse(args); err != nil {
		return "", err
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		return "", fmt.Errorf("loading config: %w", err)
	}

	baseDir := config.ResolveBaseDir(cfg.BaseDir)

	nodeName := config.GetTmuxPaneName()
	if nodeName == "" {
		return "", fmt.Errorf("node name auto-detection failed: set tmux pane title")
	}
	if err := ValidateOutboundNodeName("auto-detected pane title", nodeName); err != nil {
		return "", err
	}

	sessionName := config.GetTmuxSessionName()
	if sessionName == "" {
		return "", fmt.Errorf("tmux session name required (run inside tmux)")
	}
	sessionName, err = config.ValidateSessionName(sessionName)
	if err != nil {
		return "", err
	}

	var resolvedContextID string
	if *contextID != "" {
		resolvedContextID, err = config.ResolveContextID(*contextID)
		if err != nil {
			return "", err
		}
	} else {
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
		if err != nil {
			return "", err
		}
	}

	inboxPath := filepath.Join(baseDir, resolvedContextID, sessionName, "inbox", nodeName)
	return inboxPath, nil
}

// FilterToUINode narrows nodes to the single entry whose simple name matches
// uiNode. If uiNode is empty, a shallow copy of nodes is returned.
// Returns an empty map when uiNode is set but not found.
// NOTE: always returns a new map — callers may mutate freely.
func FilterToUINode(nodes map[string]discovery.NodeInfo, uiNode string) map[string]discovery.NodeInfo {
	result := make(map[string]discovery.NodeInfo, len(nodes))
	for nodeName, info := range nodes {
		if uiNode == "" || ping.ExtractSimpleName(nodeName) == uiNode {
			result[nodeName] = info
		}
	}
	return result
}

// PrintDoubleDashDefaults prints flag defaults with -- prefix (POSIX style).
func PrintDoubleDashDefaults(fs *flag.FlagSet) {
	PrintDoubleDashDefaultsExcept(os.Stderr, fs, nil)
}

// PrintDoubleDashDefaultsExcept prints flag defaults with -- prefix while
// omitting any hidden flags.
func PrintDoubleDashDefaultsExcept(w io.Writer, fs *flag.FlagSet, hidden map[string]bool) {
	fs.VisitAll(func(f *flag.Flag) {
		if hidden != nil && hidden[f.Name] {
			return
		}
		typeName, usage := flag.UnquoteUsage(f)
		var line string
		if typeName == "" {
			line = fmt.Sprintf("  --%s", f.Name)
		} else {
			line = fmt.Sprintf("  --%s %s", f.Name, typeName)
		}
		fmt.Fprintf(w, "%s\n\t\t%s\n", line, usage)
	})
}

// SetUsageWithoutContextID hides the internal context override from
// command-specific help output while keeping the flag functional.
func SetUsageWithoutContextID(fs *flag.FlagSet) {
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage of %s:\n", fs.Name())
		PrintDoubleDashDefaultsExcept(fs.Output(), fs, map[string]bool{
			"config":     true,
			"context-id": true,
		})
	}
}
