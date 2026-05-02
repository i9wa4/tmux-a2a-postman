package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestDispatch_StartCallsStartHandler(t *testing.T) {
	called := false

	result := Dispatch(
		"start",
		nil,
		Config{
			ContextID:   "ctx-start",
			ConfigPath:  "/tmp/postman.toml",
			LogFilePath: "/tmp/postman.log",
			NoTUI:       true,
		},
		Handlers{
			Start: func(contextID, configPath, logFilePath string, noTUI bool) error {
				called = true
				if contextID != "ctx-start" {
					t.Fatalf("contextID = %q, want %q", contextID, "ctx-start")
				}
				if configPath != "/tmp/postman.toml" {
					t.Fatalf("configPath = %q, want %q", configPath, "/tmp/postman.toml")
				}
				if logFilePath != "/tmp/postman.log" {
					t.Fatalf("logFilePath = %q, want %q", logFilePath, "/tmp/postman.log")
				}
				if !noTUI {
					t.Fatal("noTUI = false, want true")
				}
				return nil
			},
		},
	)

	if result.Err != nil {
		t.Fatalf("Dispatch returned error: %v", result.Err)
	}
	if !called {
		t.Fatal("start handler was not called")
	}
}

func TestDispatch_SendUsesCanonicalNameOnly(t *testing.T) {
	var gotArgs []string

	result := Dispatch(
		"send",
		[]string{"--to", "worker", "--body", "hello"},
		Config{ContextID: "ctx-123", ConfigPath: "/tmp/postman.toml"},
		Handlers{
			SendMessage: func(args []string) error {
				gotArgs = append([]string(nil), args...)
				return nil
			},
		},
	)

	if result.Err != nil {
		t.Fatalf("Dispatch returned error: %v", result.Err)
	}
	if result.Label != "postman send" {
		t.Fatalf("label = %q, want %q", result.Label, "postman send")
	}
	wantArgs := []string{"--config", "/tmp/postman.toml", "--context-id", "ctx-123", "--to", "worker", "--body", "hello"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("send args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestDispatch_PopPrependsContextAndConfig(t *testing.T) {
	var gotArgs []string

	result := Dispatch(
		"pop",
		[]string{"--json"},
		Config{ContextID: "ctx-123", ConfigPath: "/tmp/postman.toml"},
		Handlers{
			Pop: func(args []string) error {
				gotArgs = append([]string(nil), args...)
				return nil
			},
		},
	)

	if result.Err != nil {
		t.Fatalf("Dispatch returned error: %v", result.Err)
	}
	if result.Label != "postman pop" {
		t.Fatalf("label = %q, want %q", result.Label, "postman pop")
	}
	wantArgs := []string{"--config", "/tmp/postman.toml", "--context-id", "ctx-123", "--json"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("pop args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestDispatch_StatusPrependsContextAndConfig(t *testing.T) {
	var gotArgs []string

	result := Dispatch(
		"status",
		[]string{"--json"},
		Config{ContextID: "ctx-123", ConfigPath: "/tmp/postman.toml"},
		Handlers{
			Status: func(args []string) error {
				gotArgs = append([]string(nil), args...)
				return nil
			},
		},
	)

	if result.Err != nil {
		t.Fatalf("Dispatch returned error: %v", result.Err)
	}
	if result.Label != "postman status" {
		t.Fatalf("label = %q, want %q", result.Label, "postman status")
	}
	wantArgs := []string{"--config", "/tmp/postman.toml", "--context-id", "ctx-123", "--json"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("status args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestDispatch_StopPrependsConfigOnly(t *testing.T) {
	var gotArgs []string

	result := Dispatch(
		"stop",
		[]string{"--timeout", "2"},
		Config{ContextID: "ctx-123", ConfigPath: "/tmp/postman.toml"},
		Handlers{
			Stop: func(args []string) error {
				gotArgs = append([]string(nil), args...)
				return nil
			},
		},
	)

	if result.Err != nil {
		t.Fatalf("Dispatch returned error: %v", result.Err)
	}
	if result.Label != "postman stop" {
		t.Fatalf("label = %q, want %q", result.Label, "postman stop")
	}
	wantArgs := []string{"--config", "/tmp/postman.toml", "--timeout", "2"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("stop args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestDispatch_HelpCallsHelpHandler(t *testing.T) {
	var gotArgs []string

	result := Dispatch(
		"help",
		[]string{"messaging"},
		Config{},
		Handlers{
			Help: func(args []string) {
				gotArgs = append([]string(nil), args...)
			},
		},
	)

	if result.Err != nil {
		t.Fatalf("Dispatch returned error: %v", result.Err)
	}
	wantArgs := []string{"messaging"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("help args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestDispatch_UnknownCommandReturnsUsageError(t *testing.T) {
	assertUnknownCommand(t, "mystery")
}

func TestDispatch_RetiredCommandsReturnUsageError(t *testing.T) {
	for _, command := range []string{
		"read",
		"get-health",
		"get-health-oneline",
		"get-session-health",
		"get-session-status-oneline",
		"timeline",
		"replay",
		"get-context-id",
		"supervisor-drain",
		"send-message",
		"todo",
		"bind",
		"schema",
	} {
		t.Run(command, func(t *testing.T) {
			assertUnknownCommand(t, command)
		})
	}
}

func assertUnknownCommand(t *testing.T, command string) {
	t.Helper()

	result := Dispatch(command, []string{"--json"}, Config{ContextID: "ctx-123", ConfigPath: "/tmp/postman.toml"}, Handlers{})
	if result.Err == nil {
		t.Fatal("Dispatch returned nil error for unknown command")
	}
	if result.Label != "postman" {
		t.Fatalf("label = %q, want %q", result.Label, "postman")
	}
	if !result.ShowUsage {
		t.Fatal("ShowUsage = false, want true")
	}
	want := `unknown command "` + command + `"`
	if !strings.Contains(result.Err.Error(), want) {
		t.Fatalf("error = %q, want to contain %q", result.Err.Error(), want)
	}
}
