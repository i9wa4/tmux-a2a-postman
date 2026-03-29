package cli

import (
	"errors"
	"reflect"
	"testing"
)

func TestDispatch_ReadPrependsContextAndConfig(t *testing.T) {
	var gotArgs []string

	result := Dispatch(
		"read",
		[]string{"--archived"},
		Config{ContextID: "ctx-123", ConfigPath: "/tmp/postman.toml"},
		Handlers{
			Read: func(args []string) error {
				gotArgs = append([]string(nil), args...)
				return nil
			},
		},
	)

	if result.Err != nil {
		t.Fatalf("Dispatch returned error: %v", result.Err)
	}
	wantArgs := []string{"--config", "/tmp/postman.toml", "--context-id", "ctx-123", "--archived"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("read args = %#v, want %#v", gotArgs, wantArgs)
	}
}

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

func TestDispatch_UnknownCommandReturnsUsageError(t *testing.T) {
	result := Dispatch("mystery", nil, Config{}, Handlers{})
	if result.Err == nil {
		t.Fatal("Dispatch returned nil error for unknown command")
	}
	if result.Label != "postman" {
		t.Fatalf("label = %q, want %q", result.Label, "postman")
	}
	if !result.ShowUsage {
		t.Fatal("ShowUsage = false, want true")
	}
	if result.Err.Error() != `unknown command "mystery"` {
		t.Fatalf("error = %q, want %q", result.Err.Error(), `unknown command "mystery"`)
	}
}

func TestDispatch_BindPreservesArgsAndLabel(t *testing.T) {
	var gotArgs []string
	wantErr := errors.New("bind failed")

	result := Dispatch(
		"bind",
		[]string{"register", "--file", "bindings.toml"},
		Config{},
		Handlers{
			Bind: func(args []string) error {
				gotArgs = append([]string(nil), args...)
				return wantErr
			},
		},
	)

	if !errors.Is(result.Err, wantErr) {
		t.Fatalf("error = %v, want %v", result.Err, wantErr)
	}
	if result.Label != "postman bind" {
		t.Fatalf("label = %q, want %q", result.Label, "postman bind")
	}
	wantArgs := []string{"register", "--file", "bindings.toml"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("bind args = %#v, want %#v", gotArgs, wantArgs)
	}
}
