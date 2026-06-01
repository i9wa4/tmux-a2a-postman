package cli

import (
	"fmt"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

type startPreflightDecision string

const (
	startPreflightProceed startPreflightDecision = "proceed"
	startPreflightRefuse  startPreflightDecision = "refuse"
	startPreflightWarn    startPreflightDecision = "warn"
)

type startPreflightInput struct {
	BaseDir         string
	ContextID       string
	SessionName     string
	TmuxSessionName string

	FindCurrentUserDaemon          func(string) (string, string, bool)
	IsSessionPIDOwnedByCurrentUser func(string, string, string) bool
	IsSessionPIDAlive              func(string, string, string) bool
}

type startPreflightDiagnostic struct {
	Code            string
	Decision        startPreflightDecision
	Provenance      string
	ContextID       string
	SessionName     string
	TmuxSessionName string
	OwnerContext    string
	OwnerSession    string
}

type startPreflightPlan struct {
	Decision    startPreflightDecision
	Diagnostics []startPreflightDiagnostic
	Err         error
}

func planStartPreflight(in startPreflightInput) startPreflightPlan {
	in = in.withDefaults()
	plan := startPreflightPlan{Decision: startPreflightProceed}

	if ownerContext, ownerSession, ok := in.FindCurrentUserDaemon(in.BaseDir); ok {
		return startPreflightPlan{
			Decision: startPreflightRefuse,
			Diagnostics: []startPreflightDiagnostic{{
				Code:            "current_user_daemon_running",
				Decision:        startPreflightRefuse,
				Provenance:      "config.FindCurrentUserDaemon",
				ContextID:       in.ContextID,
				SessionName:     in.SessionName,
				TmuxSessionName: in.TmuxSessionName,
				OwnerContext:    ownerContext,
				OwnerSession:    ownerSession,
			}},
			Err: fmt.Errorf(
				"a postman daemon is already running for this user in tmux session %q (context: %s); TUI and no-TUI daemon modes are exclusive; use `tmux-a2a-postman watch-status` to observe the running daemon or `tmux-a2a-postman stop` before switching modes",
				ownerSession, ownerContext,
			),
		}
	}

	if in.TmuxSessionName == "" {
		plan.Decision = startPreflightWarn
		plan.Diagnostics = append(plan.Diagnostics, startPreflightDiagnostic{
			Code:        "tmux_session_unknown",
			Decision:    startPreflightWarn,
			Provenance:  "config.GetTmuxSessionName",
			ContextID:   in.ContextID,
			SessionName: in.SessionName,
		})
		return plan
	}

	if in.IsSessionPIDOwnedByCurrentUser(in.BaseDir, in.ContextID, in.TmuxSessionName) {
		return startPreflightPlan{
			Decision: startPreflightRefuse,
			Diagnostics: []startPreflightDiagnostic{{
				Code:            "current_user_session_pid_running",
				Decision:        startPreflightRefuse,
				Provenance:      "config.IsSessionPIDOwnedByCurrentUser",
				ContextID:       in.ContextID,
				SessionName:     in.SessionName,
				TmuxSessionName: in.TmuxSessionName,
				OwnerContext:    in.ContextID,
				OwnerSession:    in.TmuxSessionName,
			}},
			Err: fmt.Errorf(
				"a postman daemon is already running in tmux session %q (context: %s); TUI and no-TUI daemon modes are exclusive; use `tmux-a2a-postman watch-status` to observe the running daemon or `tmux-a2a-postman stop` before switching modes",
				in.TmuxSessionName, in.ContextID,
			),
		}
	}

	if in.IsSessionPIDAlive(in.BaseDir, in.ContextID, in.TmuxSessionName) {
		plan.Decision = startPreflightWarn
		plan.Diagnostics = append(plan.Diagnostics, startPreflightDiagnostic{
			Code:            "foreign_session_pid_running",
			Decision:        startPreflightWarn,
			Provenance:      "config.IsSessionPIDAlive",
			ContextID:       in.ContextID,
			SessionName:     in.SessionName,
			TmuxSessionName: in.TmuxSessionName,
			OwnerContext:    in.ContextID,
			OwnerSession:    in.TmuxSessionName,
		})
	}

	return plan
}

func (in startPreflightInput) withDefaults() startPreflightInput {
	if in.FindCurrentUserDaemon == nil {
		in.FindCurrentUserDaemon = config.FindCurrentUserDaemon
	}
	if in.IsSessionPIDOwnedByCurrentUser == nil {
		in.IsSessionPIDOwnedByCurrentUser = config.IsSessionPIDOwnedByCurrentUser
	}
	if in.IsSessionPIDAlive == nil {
		in.IsSessionPIDAlive = config.IsSessionPIDAlive
	}
	return in
}
