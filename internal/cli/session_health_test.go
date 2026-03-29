package cli

import (
	"os"
	"strings"
	"testing"
)

func TestRunGetSessionHealth(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErrSub string
	}{
		{
			name:       "valid session name no error on flag parse",
			args:       []string{"--session", "my-session"},
			wantErrSub: "",
		},
		{
			name:       "path traversal rejected",
			args:       []string{"--session", "../bad"},
			wantErrSub: "invalid value",
		},
		{
			name:       "underscore session name accepted",
			args:       []string{"--session", "bad_name"},
			wantErrSub: "",
		},
		{
			name:       "dot session name rejected",
			args:       []string{"--session", "."},
			wantErrSub: "invalid value",
		},
		{
			name:       "double dot",
			args:       []string{"--session", ".."},
			wantErrSub: "invalid value",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("POSTMAN_HOME", tmpDir)
			err := RunGetSessionHealth(tc.args)
			if tc.wantErrSub == "" {
				if err != nil && (strings.Contains(err.Error(), "flag provided but not defined") ||
					strings.Contains(err.Error(), "invalid value")) {
					t.Errorf("unexpected validation error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSub)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("error = %q; want to contain %q", err.Error(), tc.wantErrSub)
			}
		})
	}
}

func TestRunGetSessionHealth_UsesTMUXSessionWhenSessionFlagMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	t.Setenv("TMUX_PANE", "%77")

	scriptDir := t.TempDir()
	scriptPath := scriptDir + string(os.PathSeparator) + "tmux"
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *\"#{session_name}\"*) printf '%s\\n' \"tmux-session\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fake tmux: %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := RunGetSessionHealth(nil)
	if err != nil && (strings.Contains(err.Error(), "flag provided but not defined") ||
		strings.Contains(err.Error(), "session name required")) {
		t.Fatalf("RunGetSessionHealth should use tmux session fallback, got: %v", err)
	}
}
