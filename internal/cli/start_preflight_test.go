package cli

import (
	"strings"
	"testing"
)

func TestPlanStartPreflightRefusesCurrentUserDaemon(t *testing.T) {
	plan := planStartPreflight(startPreflightInput{
		BaseDir:         "/state",
		ContextID:       "ctx-new",
		SessionName:     "main",
		TmuxSessionName: "main",
		FindCurrentUserDaemon: func(baseDir string) (string, string, bool) {
			if baseDir != "/state" {
				t.Fatalf("FindCurrentUserDaemon baseDir = %q, want /state", baseDir)
			}
			return "ctx-owner", "daemon-pane", true
		},
	})

	if plan.Decision != startPreflightRefuse {
		t.Fatalf("plan.Decision = %q, want %q", plan.Decision, startPreflightRefuse)
	}
	if plan.Err == nil {
		t.Fatal("plan.Err = nil, want refusal")
	}
	if !strings.Contains(plan.Err.Error(), "already running for this user") {
		t.Fatalf("plan.Err = %q, want current-user wording", plan.Err)
	}
	if !strings.Contains(plan.Err.Error(), "TUI and no-TUI daemon modes are exclusive") ||
		!strings.Contains(plan.Err.Error(), "tmux-a2a-postman watch-status") {
		t.Fatalf("plan.Err = %q, want exclusive-mode watch-status guidance", plan.Err)
	}
	if len(plan.Diagnostics) != 1 {
		t.Fatalf("len(plan.Diagnostics) = %d, want 1", len(plan.Diagnostics))
	}
	diagnostic := plan.Diagnostics[0]
	if diagnostic.Code != "current_user_daemon_running" {
		t.Fatalf("diagnostic.Code = %q, want current_user_daemon_running", diagnostic.Code)
	}
	if diagnostic.Provenance != "config.FindCurrentUserDaemon" {
		t.Fatalf("diagnostic.Provenance = %q, want config.FindCurrentUserDaemon", diagnostic.Provenance)
	}
	if diagnostic.OwnerContext != "ctx-owner" || diagnostic.OwnerSession != "daemon-pane" {
		t.Fatalf("diagnostic owner = %q/%q, want ctx-owner/daemon-pane", diagnostic.OwnerContext, diagnostic.OwnerSession)
	}
}

func TestPlanStartPreflightRefusesCurrentUserSessionPID(t *testing.T) {
	plan := planStartPreflight(startPreflightInput{
		BaseDir:         "/state",
		ContextID:       "ctx-current",
		SessionName:     "main",
		TmuxSessionName: "main",
		FindCurrentUserDaemon: func(string) (string, string, bool) {
			return "", "", false
		},
		IsSessionPIDOwnedByCurrentUser: func(baseDir, contextID, sessionName string) bool {
			if baseDir != "/state" || contextID != "ctx-current" || sessionName != "main" {
				t.Fatalf("IsSessionPIDOwnedByCurrentUser args = %q/%q/%q", baseDir, contextID, sessionName)
			}
			return true
		},
	})

	if plan.Decision != startPreflightRefuse {
		t.Fatalf("plan.Decision = %q, want %q", plan.Decision, startPreflightRefuse)
	}
	if plan.Err == nil {
		t.Fatal("plan.Err = nil, want refusal")
	}
	if !strings.Contains(plan.Err.Error(), `already running in tmux session "main"`) {
		t.Fatalf("plan.Err = %q, want same-session wording", plan.Err)
	}
	if !strings.Contains(plan.Err.Error(), "TUI and no-TUI daemon modes are exclusive") ||
		!strings.Contains(plan.Err.Error(), "tmux-a2a-postman watch-status") {
		t.Fatalf("plan.Err = %q, want exclusive-mode watch-status guidance", plan.Err)
	}
	if len(plan.Diagnostics) != 1 {
		t.Fatalf("len(plan.Diagnostics) = %d, want 1", len(plan.Diagnostics))
	}
	diagnostic := plan.Diagnostics[0]
	if diagnostic.Code != "current_user_session_pid_running" {
		t.Fatalf("diagnostic.Code = %q, want current_user_session_pid_running", diagnostic.Code)
	}
	if diagnostic.Provenance != "config.IsSessionPIDOwnedByCurrentUser" {
		t.Fatalf("diagnostic.Provenance = %q, want config.IsSessionPIDOwnedByCurrentUser", diagnostic.Provenance)
	}
}

func TestPlanStartPreflightStalePIDProceeds(t *testing.T) {
	plan := planStartPreflight(startPreflightInput{
		BaseDir:         "/state",
		ContextID:       "ctx-current",
		SessionName:     "main",
		TmuxSessionName: "main",
		FindCurrentUserDaemon: func(string) (string, string, bool) {
			return "", "", false
		},
		IsSessionPIDOwnedByCurrentUser: func(string, string, string) bool {
			return false
		},
		IsSessionPIDAlive: func(string, string, string) bool {
			return false
		},
	})

	if plan.Decision != startPreflightProceed {
		t.Fatalf("plan.Decision = %q, want %q", plan.Decision, startPreflightProceed)
	}
	if plan.Err != nil {
		t.Fatalf("plan.Err = %v, want nil", plan.Err)
	}
	if len(plan.Diagnostics) != 0 {
		t.Fatalf("len(plan.Diagnostics) = %d, want 0", len(plan.Diagnostics))
	}
}

func TestPlanStartPreflightWarnsForForeignLiveSessionPID(t *testing.T) {
	plan := planStartPreflight(startPreflightInput{
		BaseDir:         "/state",
		ContextID:       "ctx-current",
		SessionName:     "main",
		TmuxSessionName: "main",
		FindCurrentUserDaemon: func(string) (string, string, bool) {
			return "", "", false
		},
		IsSessionPIDOwnedByCurrentUser: func(string, string, string) bool {
			return false
		},
		IsSessionPIDAlive: func(baseDir, contextID, sessionName string) bool {
			if baseDir != "/state" || contextID != "ctx-current" || sessionName != "main" {
				t.Fatalf("IsSessionPIDAlive args = %q/%q/%q", baseDir, contextID, sessionName)
			}
			return true
		},
	})

	if plan.Decision != startPreflightWarn {
		t.Fatalf("plan.Decision = %q, want %q", plan.Decision, startPreflightWarn)
	}
	if plan.Err != nil {
		t.Fatalf("plan.Err = %v, want nil", plan.Err)
	}
	if len(plan.Diagnostics) != 1 {
		t.Fatalf("len(plan.Diagnostics) = %d, want 1", len(plan.Diagnostics))
	}
	diagnostic := plan.Diagnostics[0]
	if diagnostic.Code != "foreign_session_pid_running" {
		t.Fatalf("diagnostic.Code = %q, want foreign_session_pid_running", diagnostic.Code)
	}
	if diagnostic.Provenance != "config.IsSessionPIDAlive" {
		t.Fatalf("diagnostic.Provenance = %q, want config.IsSessionPIDAlive", diagnostic.Provenance)
	}
}

func TestPlanStartPreflightWarnsWhenTmuxSessionUnknown(t *testing.T) {
	plan := planStartPreflight(startPreflightInput{
		BaseDir:     "/state",
		ContextID:   "ctx-current",
		SessionName: "default",
		FindCurrentUserDaemon: func(string) (string, string, bool) {
			return "", "", false
		},
	})

	if plan.Decision != startPreflightWarn {
		t.Fatalf("plan.Decision = %q, want %q", plan.Decision, startPreflightWarn)
	}
	if plan.Err != nil {
		t.Fatalf("plan.Err = %v, want nil", plan.Err)
	}
	if len(plan.Diagnostics) != 1 {
		t.Fatalf("len(plan.Diagnostics) = %d, want 1", len(plan.Diagnostics))
	}
	diagnostic := plan.Diagnostics[0]
	if diagnostic.Code != "tmux_session_unknown" {
		t.Fatalf("diagnostic.Code = %q, want tmux_session_unknown", diagnostic.Code)
	}
	if diagnostic.Provenance != "config.GetTmuxSessionName" {
		t.Fatalf("diagnostic.Provenance = %q, want config.GetTmuxSessionName", diagnostic.Provenance)
	}
}
