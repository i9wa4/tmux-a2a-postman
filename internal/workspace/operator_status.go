package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/binding"
)

// OperatorNode is a human overview row, not an authenticated agent caller.
// Session is the numeric native tmux session ID ($N), not a display index.
type OperatorNode struct {
	Session, Window, Top, Left int
	Pane                       string
	Status                     NodeStatus
}

type operatorPane struct {
	Owner                      Owner
	Root, Role, PID            string
	Session, Window, Top, Left int
	Dead, Linked               bool
	RootError                  string
}

func (t Tmux) operatorPanes(ctx context.Context) ([]operatorPane, error) {
	if !filepath.IsAbs(t.Socket) || !filepath.IsAbs(t.Home) {
		return nil, ErrUnknown
	}
	const marker = "postman-operator-frame\t"
	serverFormat := marker + "#{socket_path}\t#{pid}\t#{start_time}"
	// Sanitizing an invalid title inserts '_', which cannot be a configured
	// role. It cannot turn an unrelated title into a valid declared role, and
	// avoids tabs/newlines in unrelated UI titles corrupting every other row.
	format := "#{session_id}\t#{window_id}\t#{window_index}\t#{window_linked}\t#{pane_id}\t#{pane_pid}\t#{s/[^a-zA-Z0-9-]/_/g:pane_title}\t#{pane_dead}\t#{pane_top}\t#{pane_left}\t#{session_path}"
	out, err := tmuxOutputLimited(ctx, 256*1024, t.Socket, "display-message", "-p", serverFormat, ";", "list-panes", "-a", "-F", format, ";", "display-message", "-p", serverFormat)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) < 2 || len(lines) > 1026 || lines[0] != lines[len(lines)-1] || !strings.HasPrefix(lines[0], marker) {
		return nil, errors.New("native server changed or operator frame exceeded its bound")
	}
	server := strings.Split(strings.TrimPrefix(lines[0], marker), "\t")
	if len(server) != 3 {
		return nil, ErrUnknown
	}
	socket, incarnation, err := serverIdentity(t.Socket, server[0], server[1], server[2])
	if err != nil {
		return nil, err
	}
	var panes []operatorPane
	for _, line := range lines[1 : len(lines)-1] {
		f := strings.SplitN(line, "\t", 11)
		if len(f) != 11 || !nativeID(f[0], '$') || !nativeID(f[1], '@') || !nativeID(f[4], '%') || !positivePID(f[5]) ||
			(f[3] != "0" && f[3] != "1") || (f[7] != "0" && f[7] != "1") {
			return nil, errors.New("malformed native operator pane row")
		}
		values := make([]int, 4)
		for i, value := range []string{f[0][1:], f[2], f[8], f[9]} {
			values[i], err = strconv.Atoi(value)
			if err != nil || values[i] < 0 {
				return nil, ErrUnknown
			}
		}
		root, rootErr := CanonicalRoot(f[10], t.Home)
		pane := operatorPane{
			Owner: Owner{socket, incarnation, f[0], f[4]}, Root: root, Role: f[6], PID: f[5],
			Session: values[0], Window: values[1], Top: values[2], Left: values[3], Dead: f[7] == "1", Linked: f[3] == "1",
		}
		if rootErr != nil {
			pane.RootError = rootErr.Error()
		}
		panes = append(panes, pane)
	}
	return panes, nil
}

func readOperatorRuntime(ws *os.Root, owner Owner) (runtimeRecord, error) {
	var saved runtimeRecord
	dir, err := privateDir(ws, "runtime", false)
	if err != nil {
		return saved, err
	}
	defer func() { _ = dir.Close() }()
	if readJSON(dir, "binding.json", &saved) != nil || saved.Version != 1 || !saved.Owner.valid() ||
		ValidateBindings(saved.Bindings) != nil || !sameRuntime(saved.Owner, owner) {
		return saved, ErrUnknown
	}
	for _, binding := range saved.Bindings {
		if !sameRuntime(saved.Owner, binding.Owner) {
			return saved, ErrUnknown
		}
	}
	return saved, nil
}

// OperatorStatus reuses nodeSummary and readPaneTimes without manufacturing an
// agent caller. Only an exact saved binding verified against the live native
// pane can supply known mail counts. Unbound configured candidates stay '?'.
// All storage access is read-only; this never creates or updates bindings.
func (m Mail) OperatorStatus(ctx context.Context, rt Tmux) ([]OperatorNode, error) {
	panes, err := rt.operatorPanes(ctx)
	if err != nil {
		return nil, err
	}
	roles := map[string]bool{}
	for _, role := range rt.Roles {
		if !binding.ValidateNodeName(role) || roles[role] {
			return nil, ErrUnknown
		}
		roles[role] = true
	}
	rows := []OperatorNode{}
	identity := map[int]operatorPane{}
	groups := map[int][]int{}
	sessionPanes := map[int][]operatorPane{}
	add := func(pane operatorPane, role, reason string) error {
		if len(rows) >= 256 {
			return errors.New("operator overview exceeds 256 configured pane candidates")
		}
		i := len(rows)
		rows = append(rows, OperatorNode{pane.Session, pane.Window, pane.Top, pane.Left, pane.Owner.Pane, NodeStatus{Node: role, Reason: reason}})
		identity[i] = pane
		groups[pane.Session] = append(groups[pane.Session], i)
		return nil
	}
	for _, pane := range panes {
		sessionPanes[pane.Session] = append(sessionPanes[pane.Session], pane)
		if roles[pane.Role] {
			if err := add(pane, pane.Role, "native pane has no verified saved binding"); err != nil {
				return nil, err
			}
		}
	}
	var sessions []int
	for session := range sessionPanes {
		sessions = append(sessions, session)
	}
	sort.Ints(sessions)
	for _, session := range sessions {
		first := sessionPanes[session][0]
		if first.RootError != "" {
			for _, i := range groups[session] {
				rows[i].Status.Reason = first.RootError
			}
			continue
		}
		readErr := m.Store.withReadWorkspace(ctx, first.Root, func(ws *os.Root) error {
			saved, err := readOperatorRuntime(ws, first.Owner)
			if err != nil {
				return err
			}
			// A renamed live registered pane stays visible as unknown. Its identity
			// comes from the saved/native binding, never a guessed agent name.
			for _, pane := range sessionPanes[session] {
				if roles[pane.Role] {
					continue
				}
				for _, candidate := range saved.Bindings {
					if roles[candidate.Role] && candidate.Owner == pane.Owner && candidate.PanePID == pane.PID {
						if err := add(pane, candidate.Role, "native title no longer matches the registered role"); err != nil {
							return err
						}
					}
				}
			}
			duplicates := map[string]int{}
			for _, i := range groups[session] {
				duplicates[rows[i].Status.Node]++
			}
			for _, i := range groups[session] {
				pane := identity[i]
				role := rows[i].Status.Node
				if err := ctx.Err(); err != nil {
					rows[i].Status.Reason = err.Error()
					continue
				}
				if pane.Root != first.Root || pane.Dead || pane.Linked || duplicates[role] != 1 || pane.Role != role {
					rows[i].Status.Reason = "dead, linked, renamed or ambiguous native role pane"
					continue
				}
				current := Binding{Role: role, Owner: pane.Owner, PanePID: pane.PID}
				bound := false
				for _, candidate := range saved.Bindings {
					if candidate == current {
						bound = true
					}
				}
				if !bound {
					continue
				}
				status := nodeSummary(ctx, ws, role)
				status.LastCaptureAt, status.LastScreenChangeAt = readPaneTimes(ws, Snapshot{Root: pane.Root, Bindings: []Binding{current}}, role)
				rows[i].Status = status
			}
			return nil
		})
		if readErr != nil {
			for _, i := range groups[session] {
				rows[i].Status = NodeStatus{Node: rows[i].Status.Node, Reason: readErr.Error()}
			}
		}
	}
	last, lastErr := rt.operatorPanes(ctx)
	for i := range rows {
		original := identity[i]
		matched := false
		for _, pane := range last {
			if pane.Owner == original.Owner && pane.PID == original.PID && pane.Root == original.Root && pane.Role == original.Role && pane.Dead == original.Dead && pane.Linked == original.Linked {
				matched = true
				break
			}
		}
		if lastErr != nil || !matched {
			reason := "native status changed during verification"
			if lastErr != nil {
				reason = "native status could not be reverified: " + lastErr.Error()
			}
			rows[i].Status = NodeStatus{Node: rows[i].Status.Node, Reason: reason}
		}
	}
	return rows, nil
}
