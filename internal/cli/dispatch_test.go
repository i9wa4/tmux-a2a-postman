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

func TestDispatch_SendHeredocUsesCanonicalNameOnly(t *testing.T) {
	var gotArgs []string

	result := Dispatch(
		"send-heredoc",
		[]string{"--to", "worker"},
		Config{ContextID: "ctx-123", ConfigPath: "/tmp/postman.toml"},
		Handlers{
			SendHeredoc: func(args []string) error {
				gotArgs = append([]string(nil), args...)
				return nil
			},
		},
	)

	if result.Err != nil {
		t.Fatalf("Dispatch returned error: %v", result.Err)
	}
	if result.Label != "postman send-heredoc" {
		t.Fatalf("label = %q, want %q", result.Label, "postman send-heredoc")
	}
	wantArgs := []string{"--config", "/tmp/postman.toml", "--context-id", "ctx-123", "--to", "worker"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("send-heredoc args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestDispatch_PopPrependsContextAndConfig(t *testing.T) {
	var gotArgs []string

	result := Dispatch(
		"pop",
		nil,
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
	wantArgs := []string{"--config", "/tmp/postman.toml", "--context-id", "ctx-123"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("pop args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestDispatch_HealthCommandsArePublic(t *testing.T) {
	t.Run("get-health", func(t *testing.T) {
		var gotArgs []string

		result := Dispatch(
			"get-health",
			nil,
			Config{ContextID: "ctx-123", ConfigPath: "/tmp/postman.toml"},
			Handlers{
				GetSessionHealth: func(args []string) error {
					gotArgs = append([]string(nil), args...)
					return nil
				},
			},
		)

		if result.Err != nil {
			t.Fatalf("Dispatch returned error: %v", result.Err)
		}
		if result.Label != "postman get-health" {
			t.Fatalf("label = %q, want %q", result.Label, "postman get-health")
		}
		wantArgs := []string{"--config", "/tmp/postman.toml", "--context-id", "ctx-123"}
		if !reflect.DeepEqual(gotArgs, wantArgs) {
			t.Fatalf("get-health args = %#v, want %#v", gotArgs, wantArgs)
		}
	})

	t.Run("get-health-oneline", func(t *testing.T) {
		var gotArgs []string

		result := Dispatch(
			"get-health-oneline",
			nil,
			Config{ContextID: "ctx-123", ConfigPath: "/tmp/postman.toml"},
			Handlers{
				GetSessionStatusOneline: func(args []string) error {
					gotArgs = append([]string(nil), args...)
					return nil
				},
			},
		)

		if result.Err != nil {
			t.Fatalf("Dispatch returned error: %v", result.Err)
		}
		if result.Label != "postman get-health-oneline" {
			t.Fatalf("label = %q, want %q", result.Label, "postman get-health-oneline")
		}
		wantArgs := []string{"--config", "/tmp/postman.toml", "--context-id", "ctx-123"}
		if !reflect.DeepEqual(gotArgs, wantArgs) {
			t.Fatalf("get-health-oneline args = %#v, want %#v", gotArgs, wantArgs)
		}
	})
}

func TestDispatch_InspectInputPrependsContextAndConfig(t *testing.T) {
	var gotArgs []string

	result := Dispatch(
		"inspect-input",
		[]string{"--id", "ireq_123"},
		Config{ContextID: "ctx-123", ConfigPath: "/tmp/postman.toml"},
		Handlers{
			InspectInput: func(args []string) error {
				gotArgs = append([]string(nil), args...)
				return nil
			},
		},
	)

	if result.Err != nil {
		t.Fatalf("Dispatch returned error: %v", result.Err)
	}
	if result.Label != "postman inspect-input" {
		t.Fatalf("label = %q, want %q", result.Label, "postman inspect-input")
	}
	wantArgs := []string{"--config", "/tmp/postman.toml", "--context-id", "ctx-123", "--id", "ireq_123"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("inspect-input args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestDispatch_StopPrependsConfigOnly(t *testing.T) {
	var gotArgs []string

	result := Dispatch(
		"stop",
		nil,
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
	wantArgs := []string{"--config", "/tmp/postman.toml"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("stop args = %#v, want %#v", gotArgs, wantArgs)
	}
}

func TestDispatch_VersionCallsVersionHandler(t *testing.T) {
	called := false

	result := Dispatch(
		"version",
		nil,
		Config{},
		Handlers{
			Version: func(args []string) error {
				called = true
				if len(args) != 0 {
					t.Fatalf("version args = %#v, want empty", args)
				}
				return nil
			},
		},
	)

	if result.Err != nil {
		t.Fatalf("Dispatch returned error: %v", result.Err)
	}
	if result.Label != "postman version" {
		t.Fatalf("label = %q, want %q", result.Label, "postman version")
	}
	if !called {
		t.Fatal("version handler was not called")
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

func TestDispatch_SubcommandHelpCallsHelpTopic(t *testing.T) {
	for _, tc := range []struct {
		command string
		args    []string
	}{
		{command: "send", args: []string{"--help"}},
		{command: "send", args: []string{"-h"}},
		{command: "version", args: []string{"--help"}},
		{command: "version", args: []string{"-h"}},
	} {
		t.Run(tc.command+"_"+strings.Join(tc.args, "_"), func(t *testing.T) {
			var gotArgs []string

			result := Dispatch(
				tc.command,
				tc.args,
				Config{ContextID: "ctx-123", ConfigPath: "/tmp/postman.toml"},
				Handlers{
					Help: func(args []string) {
						gotArgs = append([]string(nil), args...)
					},
				},
			)

			if result.Err != nil {
				t.Fatalf("Dispatch returned error: %v", result.Err)
			}
			wantArgs := []string{tc.command}
			if !reflect.DeepEqual(gotArgs, wantArgs) {
				t.Fatalf("help args = %#v, want %#v", gotArgs, wantArgs)
			}
		})
	}
}

func TestDispatch_SubcommandHelpWordIsPlainArgument(t *testing.T) {
	t.Run("send", func(t *testing.T) {
		var gotArgs []string

		result := Dispatch(
			"send",
			[]string{"help"},
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
		wantArgs := []string{"--config", "/tmp/postman.toml", "--context-id", "ctx-123", "help"}
		if !reflect.DeepEqual(gotArgs, wantArgs) {
			t.Fatalf("send args = %#v, want %#v", gotArgs, wantArgs)
		}
	})

	t.Run("version", func(t *testing.T) {
		var gotArgs []string

		result := Dispatch(
			"version",
			[]string{"help"},
			Config{},
			Handlers{
				Version: func(args []string) error {
					gotArgs = append([]string(nil), args...)
					return nil
				},
			},
		)

		if result.Err != nil {
			t.Fatalf("Dispatch returned error: %v", result.Err)
		}
		wantArgs := []string{"help"}
		if !reflect.DeepEqual(gotArgs, wantArgs) {
			t.Fatalf("version args = %#v, want %#v", gotArgs, wantArgs)
		}
	})
}

func TestDispatch_UnknownCommandReturnsUsageError(t *testing.T) {
	assertUnknownCommand(t, "mystery")
}

func TestDispatch_RetiredCommandsReturnUsageError(t *testing.T) {
	for _, command := range []string{
		"status",
		"read",
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

	result := Dispatch(command, []string{"--bogus"}, Config{ContextID: "ctx-123", ConfigPath: "/tmp/postman.toml"}, Handlers{})
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
