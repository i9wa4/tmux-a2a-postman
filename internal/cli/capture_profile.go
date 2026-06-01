package cli

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/runtimeprofile"
)

type captureProfileOutput struct {
	Status      string `json:"status"`
	Kind        string `json:"kind"`
	Destination string `json:"destination"`
	Bytes       int    `json:"bytes"`
	MaxBytes    int64  `json:"max_bytes"`
	OutputPath  string `json:"output_path,omitempty"`
}

func RunCaptureProfile(args []string) error {
	return runCaptureProfileWithContext(defaultCommandContext(), args)
}

func runCaptureProfileWithContext(ctx commandContext, args []string) error {
	ctx = ctx.withDefaults()
	fs := flag.NewFlagSet("capture-profile", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from tmux session)")
	configPath := fs.String("config", "", "Config file path")
	profileType := fs.String("type", "", "Profile type: heap or goroutine")
	output := fs.String("output", "", "Output destination: - for stdout or an explicit file path")
	maxBytes := fs.Int64("max-bytes", runtimeprofile.DefaultMaxBytes, "Maximum profile bytes to return or write")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *profileType == "" {
		return fmt.Errorf("--type is required (heap or goroutine)")
	}
	if _, err := runtimeprofile.NormalizeKind(*profileType); err != nil {
		return err
	}
	if *output == "" {
		return fmt.Errorf("--output is required; use --output - for stdout or provide a file path")
	}
	if *maxBytes <= 0 {
		return fmt.Errorf("--max-bytes must be positive")
	}

	target, err := resolveCaptureProfileTarget(ctx, *contextID, *configPath)
	if err != nil {
		return err
	}
	sessionDir := filepath.Join(target.baseDir, target.contextID, target.sessionName)
	destination := "file"
	var outputPath string
	displayOutputPath := *output
	if *output == "-" {
		destination = "stdout"
		outputPath = ""
		displayOutputPath = ""
	} else {
		abs, err := filepath.Abs(*output)
		if err != nil {
			return fmt.Errorf("resolving output path: %w", err)
		}
		outputPath = abs
	}

	response, err := ctx.roundTripDaemonSubmit(sessionDir, projection.DaemonSubmitRequest{
		Command:            projection.DaemonSubmitRuntimeProfile,
		ProfileKind:        *profileType,
		ProfileDestination: destination,
		ProfileOutputPath:  outputPath,
		ProfileMaxBytes:    *maxBytes,
	}, daemonSubmitTimeout(target.cfg.TmuxTimeout))
	if err != nil {
		return fmt.Errorf("daemon submit runtime-profile: %w", err)
	}
	if response.RuntimeProfile == nil {
		return fmt.Errorf("daemon runtime-profile response missing payload")
	}
	if destination == "stdout" {
		if response.RuntimeProfile.Encoding != "base64" || response.RuntimeProfile.ContentBase64 == "" {
			return fmt.Errorf("daemon runtime-profile stdout response missing base64 payload")
		}
		data, err := base64.StdEncoding.DecodeString(response.RuntimeProfile.ContentBase64)
		if err != nil {
			return fmt.Errorf("decoding daemon runtime-profile payload: %w", err)
		}
		_, err = ctx.stdout.Write(data)
		return err
	}

	enc := json.NewEncoder(ctx.stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(captureProfileOutput{
		Status:      "written",
		Kind:        response.RuntimeProfile.Kind,
		Destination: response.RuntimeProfile.Destination,
		Bytes:       response.RuntimeProfile.Bytes,
		MaxBytes:    response.RuntimeProfile.MaxBytes,
		OutputPath:  displayOutputPath,
	})
}

type captureProfileTarget struct {
	cfg         *config.Config
	baseDir     string
	contextID   string
	sessionName string
}

func resolveCaptureProfileTarget(ctx commandContext, contextIDFlag, configPath string) (captureProfileTarget, error) {
	cfg, err := ctx.loadConfig(configPath)
	if err != nil {
		return captureProfileTarget{}, fmt.Errorf("loading config: %w", err)
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)
	sessionName := config.GetTmuxSessionName()
	if sessionName == "" {
		return captureProfileTarget{}, fmt.Errorf("tmux session name required (run inside tmux)")
	}
	sessionName, err = config.ValidateSessionName(sessionName)
	if err != nil {
		return captureProfileTarget{}, err
	}

	resolvedContextID := contextIDFlag
	if resolvedContextID != "" {
		resolvedContextID, err = config.ResolveContextID(resolvedContextID)
	} else {
		resolvedContextID, err = config.ResolveContextIDFromSession(baseDir, sessionName)
	}
	if err != nil {
		return captureProfileTarget{}, err
	}
	if !ctx.contextOwnsSession(baseDir, resolvedContextID, sessionName) {
		return captureProfileTarget{}, fmt.Errorf("runtime profile requires an active daemon context for session %q", sessionName)
	}
	return captureProfileTarget{
		cfg:         cfg,
		baseDir:     baseDir,
		contextID:   resolvedContextID,
		sessionName: sessionName,
	}, nil
}
