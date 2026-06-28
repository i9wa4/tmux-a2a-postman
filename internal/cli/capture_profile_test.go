package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/runtimeprofile"
)

func TestRunCaptureProfileRequiresExplicitOutput(t *testing.T) {
	err := runCaptureProfileWithContext(commandContext{}, []string{"--type", "goroutine"})
	if err == nil {
		t.Fatal("runCaptureProfileWithContext() error = nil, want explicit output error")
	}
	if !strings.Contains(err.Error(), "--output is required") {
		t.Fatalf("error = %q, want explicit output guidance", err)
	}
}

func TestRunCaptureProfileStdoutUsesExplicitDaemonSubmitRequest(t *testing.T) {
	baseDir := t.TempDir()
	installFakeTmuxForCLI(t, baseDir, "review", "worker")

	var stdout bytes.Buffer
	var gotSessionDir string
	var gotRequest projection.DaemonSubmitRequest
	ctx := commandContext{
		stdout: &stdout,
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{BaseDir: baseDir, TmuxTimeout: 1}, nil
		},
		contextOwnsSession: func(baseDirArg, contextID, sessionName string) bool {
			return baseDirArg == baseDir && contextID == "ctx-prof" && sessionName == "review"
		},
		roundTripDaemonSubmit: func(sessionDir string, request projection.DaemonSubmitRequest, timeout time.Duration) (projection.DaemonSubmitResponse, error) {
			gotSessionDir = sessionDir
			gotRequest = request
			return projection.DaemonSubmitResponse{
				RequestID: "req-profile",
				Command:   projection.DaemonSubmitRuntimeProfile,
				HandledAt: time.Now().UTC().Format(time.RFC3339Nano),
				RuntimeProfile: &projection.RuntimeProfileCapture{
					Kind:          runtimeprofile.KindGoroutine,
					Destination:   "stdout",
					Encoding:      "base64",
					ContentBase64: base64.StdEncoding.EncodeToString([]byte("profile-bytes")),
					Bytes:         len("profile-bytes"),
					MaxBytes:      64,
				},
			}, nil
		},
	}

	err := runCaptureProfileWithContext(ctx, []string{
		"--context-id", "ctx-prof",
		"--type", "goroutine",
		"--output", "-",
		"--max-bytes", "64",
	})
	if err != nil {
		t.Fatalf("runCaptureProfileWithContext(): %v", err)
	}
	if stdout.String() != "profile-bytes" {
		t.Fatalf("stdout = %q, want raw profile bytes", stdout.String())
	}
	if gotSessionDir != filepath.Join(baseDir, "ctx-prof", "review") {
		t.Fatalf("sessionDir = %q", gotSessionDir)
	}
	if gotRequest.Command != projection.DaemonSubmitRuntimeProfile ||
		gotRequest.ProfileKind != runtimeprofile.KindGoroutine ||
		gotRequest.ProfileDestination != "stdout" ||
		gotRequest.ProfileOutputPath != "" ||
		gotRequest.ProfileMaxBytes != 64 {
		t.Fatalf("daemon-submit request = %#v", gotRequest)
	}
}

func TestRunCaptureProfileFileOutputPrintsMetadataWithoutProfileContent(t *testing.T) {
	baseDir := t.TempDir()
	installFakeTmuxForCLI(t, baseDir, "review", "worker")
	outputPath := filepath.Join(t.TempDir(), "heap.pprof")

	var stdout bytes.Buffer
	ctx := commandContext{
		stdout: &stdout,
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{BaseDir: baseDir, TmuxTimeout: 1}, nil
		},
		contextOwnsSession: func(baseDirArg, contextID, sessionName string) bool {
			return baseDirArg == baseDir && contextID == "ctx-prof" && sessionName == "review"
		},
		roundTripDaemonSubmit: func(_ string, request projection.DaemonSubmitRequest, _ time.Duration) (projection.DaemonSubmitResponse, error) {
			if request.ProfileDestination != "file" {
				t.Fatalf("ProfileDestination = %q, want file", request.ProfileDestination)
			}
			if request.ProfileOutputPath != outputPath {
				t.Fatalf("ProfileOutputPath = %q, want %q", request.ProfileOutputPath, outputPath)
			}
			return projection.DaemonSubmitResponse{
				RequestID: "req-profile",
				Command:   projection.DaemonSubmitRuntimeProfile,
				HandledAt: time.Now().UTC().Format(time.RFC3339Nano),
				RuntimeProfile: &projection.RuntimeProfileCapture{
					Kind:          runtimeprofile.KindHeap,
					Destination:   "file",
					ContentBase64: "secret-mailbox-body-pane-capture",
					Bytes:         123,
					MaxBytes:      runtimeprofile.DefaultMaxBytes,
					OutputPath:    outputPath,
				},
			}, nil
		},
	}

	err := runCaptureProfileWithContext(ctx, []string{
		"--context-id", "ctx-prof",
		"--type", "heap",
		"--output", outputPath,
	})
	if err != nil {
		t.Fatalf("runCaptureProfileWithContext(): %v", err)
	}
	var got captureProfileOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal stdout: %v\n%s", err, stdout.String())
	}
	if got.Status != "written" || got.Kind != runtimeprofile.KindHeap || got.Destination != "file" || got.OutputPath != outputPath {
		t.Fatalf("metadata = %#v", got)
	}
	if strings.Contains(stdout.String(), "secret-mailbox-body") || strings.Contains(stdout.String(), "pane-capture") {
		t.Fatalf("stdout leaked profile content: %q", stdout.String())
	}
}

func TestRunCaptureProfileFileForcePassesThroughRequest(t *testing.T) {
	baseDir := t.TempDir()
	installFakeTmuxForCLI(t, baseDir, "review", "worker")
	outputPath := filepath.Join(t.TempDir(), "heap.pprof")

	var gotRequest projection.DaemonSubmitRequest
	ctx := commandContext{
		stdout: &bytes.Buffer{},
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{BaseDir: baseDir, TmuxTimeout: 1}, nil
		},
		contextOwnsSession: func(baseDirArg, contextID, sessionName string) bool {
			return baseDirArg == baseDir && contextID == "ctx-prof" && sessionName == "review"
		},
		roundTripDaemonSubmit: func(_ string, request projection.DaemonSubmitRequest, _ time.Duration) (projection.DaemonSubmitResponse, error) {
			gotRequest = request
			return projection.DaemonSubmitResponse{
				RequestID: "req-profile-force",
				Command:   projection.DaemonSubmitRuntimeProfile,
				HandledAt: time.Now().UTC().Format(time.RFC3339Nano),
				RuntimeProfile: &projection.RuntimeProfileCapture{
					Kind:        runtimeprofile.KindHeap,
					Destination: "file",
					Bytes:       10,
					MaxBytes:    runtimeprofile.DefaultMaxBytes,
					OutputPath:  outputPath,
				},
			}, nil
		},
	}

	if err := runCaptureProfileWithContext(ctx, []string{
		"--context-id", "ctx-prof",
		"--type", "heap",
		"--output", outputPath,
		"--force",
	}); err != nil {
		t.Fatalf("runCaptureProfileWithContext(): %v", err)
	}
	if !gotRequest.ProfileForce {
		t.Fatal("ProfileForce = false, want true when --force is passed")
	}
}

func TestRunCaptureProfileFileNoForceByDefault(t *testing.T) {
	baseDir := t.TempDir()
	installFakeTmuxForCLI(t, baseDir, "review", "worker")
	outputPath := filepath.Join(t.TempDir(), "heap.pprof")

	var gotRequest projection.DaemonSubmitRequest
	ctx := commandContext{
		stdout: &bytes.Buffer{},
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{BaseDir: baseDir, TmuxTimeout: 1}, nil
		},
		contextOwnsSession: func(baseDirArg, contextID, sessionName string) bool {
			return baseDirArg == baseDir && contextID == "ctx-prof" && sessionName == "review"
		},
		roundTripDaemonSubmit: func(_ string, request projection.DaemonSubmitRequest, _ time.Duration) (projection.DaemonSubmitResponse, error) {
			gotRequest = request
			return projection.DaemonSubmitResponse{
				RequestID: "req-profile-no-force",
				Command:   projection.DaemonSubmitRuntimeProfile,
				HandledAt: time.Now().UTC().Format(time.RFC3339Nano),
				RuntimeProfile: &projection.RuntimeProfileCapture{
					Kind:        runtimeprofile.KindHeap,
					Destination: "file",
					Bytes:       10,
					MaxBytes:    runtimeprofile.DefaultMaxBytes,
					OutputPath:  outputPath,
				},
			}, nil
		},
	}

	if err := runCaptureProfileWithContext(ctx, []string{
		"--context-id", "ctx-prof",
		"--type", "heap",
		"--output", outputPath,
	}); err != nil {
		t.Fatalf("runCaptureProfileWithContext(): %v", err)
	}
	if gotRequest.ProfileForce {
		t.Fatal("ProfileForce = true, want false when --force is not passed")
	}
}
