package multiplexer

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	LayoutGroupKindWindow = "window"
	LayoutItemKindPane    = "pane"
)

type SessionLayout struct {
	Backend     BackendKind
	SessionName string
	Groups      []LayoutGroup
	NativeIDs   map[string]string
}

type LayoutGroup struct {
	Kind      string
	ID        ResourceID
	Order     int
	Items     []LayoutItem
	NativeIDs map[string]string
}

type LayoutItem struct {
	Kind           string
	ID             ResourceID
	Order          int
	LogicalName    string
	CurrentCommand string
	NativeIDs      map[string]string
}

type LayoutBackend interface {
	Kind() BackendKind
	SessionLayout(ctx context.Context, sessionName string) (SessionLayout, error)
}

func (b TmuxBackend) SessionLayout(_ context.Context, sessionName string) (SessionLayout, error) {
	layout := SessionLayout{
		Backend:     BackendKindTmux,
		SessionName: sessionName,
		NativeIDs: map[string]string{
			"session_name": sessionName,
		},
	}

	windowListOut, err := b.Runner.CombinedOutput(
		"list-windows",
		"-t",
		sessionName,
		"-F",
		"#{window_index}",
	)
	if err != nil {
		if tmuxLayoutMissingSession(string(windowListOut)) {
			return layout, nil
		}
		return SessionLayout{}, fmt.Errorf("listing windows for session %s: %w", sessionName, err)
	}

	for _, windowIndex := range strings.Split(strings.TrimSpace(string(windowListOut)), "\n") {
		if windowIndex == "" {
			continue
		}
		group, ok, err := b.tmuxWindowLayout(sessionName, windowIndex)
		if err != nil {
			return SessionLayout{}, err
		}
		if ok {
			layout.Groups = append(layout.Groups, group)
		}
	}

	sort.Slice(layout.Groups, func(i, j int) bool {
		if layout.Groups[i].Order != layout.Groups[j].Order {
			return layout.Groups[i].Order < layout.Groups[j].Order
		}
		return layout.Groups[i].ID.Native < layout.Groups[j].ID.Native
	})

	return layout, nil
}

func (b TmuxBackend) tmuxWindowLayout(sessionName, windowIndex string) (LayoutGroup, bool, error) {
	out, err := b.Runner.CombinedOutput(
		"list-panes",
		"-t",
		sessionName+":"+windowIndex,
		"-F",
		"#{window_index}\t#{pane_index}\t#{pane_id}\t#{pane_title}\t#{pane_current_command}",
	)
	if err != nil {
		output := string(out)
		if strings.Contains(output, "can't find window") {
			return LayoutGroup{}, false, nil
		}
		if strings.Contains(output, "no server running") {
			return LayoutGroup{}, false, nil
		}
		return LayoutGroup{}, false, fmt.Errorf("listing panes for session %s window %s: %w", sessionName, windowIndex, err)
	}

	windowOrder, _ := strconv.Atoi(windowIndex)
	group := LayoutGroup{
		Kind:  LayoutGroupKindWindow,
		ID:    ResourceID{Backend: BackendKindTmux, Kind: ResourceKindSession, Native: sessionName + ":" + windowIndex},
		Order: windowOrder,
		NativeIDs: map[string]string{
			"window_index": windowIndex,
		},
	}

	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		item, ok := parseTmuxLayoutPane(line)
		if ok {
			group.Items = append(group.Items, item)
		}
	}

	sort.Slice(group.Items, func(i, j int) bool {
		if group.Items[i].Order != group.Items[j].Order {
			return group.Items[i].Order < group.Items[j].Order
		}
		return group.Items[i].ID.Native < group.Items[j].ID.Native
	})

	return group, true, nil
}

func parseTmuxLayoutPane(line string) (LayoutItem, bool) {
	parts := strings.SplitN(line, "\t", 5)
	if len(parts) < 4 {
		return LayoutItem{}, false
	}
	paneOrder := 0
	if len(parts) > 1 {
		paneOrder, _ = strconv.Atoi(parts[1])
	}
	currentCommand := ""
	if len(parts) == 5 {
		currentCommand = parts[4]
	}
	return LayoutItem{
		Kind:           LayoutItemKindPane,
		ID:             TmuxPaneID(parts[2]),
		Order:          paneOrder,
		LogicalName:    parts[3],
		CurrentCommand: currentCommand,
		NativeIDs: map[string]string{
			"window_index": parts[0],
			"pane_index":   parts[1],
			"pane_id":      parts[2],
			"pane_title":   parts[3],
		},
	}, true
}

func tmuxLayoutMissingSession(output string) bool {
	return strings.Contains(output, "no server running") || strings.Contains(output, "can't find session")
}
