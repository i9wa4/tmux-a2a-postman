package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// DiscoverNodes scans tmux panes and returns a map of node name -> pane ID.
// Only panes that have A2A_NODE env var set are included.
func DiscoverNodes() (map[string]string, error) {
	out, err := exec.Command("tmux", "list-panes", "-s", "-F", "#{pane_pid} #{pane_id}").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w: %s", err, out)
	}

	nodes := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		pid, paneID := parts[0], parts[1]
		if node := getNodeFromProcessOS(pid); node != "" {
			nodes[node] = paneID
		}
	}
	return nodes, nil
}
