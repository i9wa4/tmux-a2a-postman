package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type SideEffectClass string

const (
	SideEffectReadOnly   SideEffectClass = "read-only"
	SideEffectIdempotent SideEffectClass = "idempotent"
	SideEffectMutating   SideEffectClass = "mutating"
)

type ReplayContract struct {
	Command              string
	CWD                  string
	EnvAllowlist         []string
	Timeout              time.Duration
	SideEffect           SideEffectClass
	ArtifactPath         string
	ExpectedArtifactHash string
}

func (c ReplayContract) Validate(root string) error {
	if err := c.ValidateShape(); err != nil {
		return err
	}
	if _, err := ContainedArtifactPath(root, c.ArtifactPath); err != nil {
		return err
	}
	return nil
}

func (c ReplayContract) ValidateShape() error {
	if strings.TrimSpace(c.Command) == "" {
		return fmt.Errorf("command is required")
	}
	if strings.TrimSpace(c.CWD) == "" {
		return fmt.Errorf("cwd is required")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if !validSideEffect(c.SideEffect) {
		return fmt.Errorf("side_effect_class must be one of read-only, idempotent, mutating")
	}
	for _, name := range c.EnvAllowlist {
		if strings.TrimSpace(name) == "" || strings.ContainsAny(name, "=\x00") {
			return fmt.Errorf("env allowlist contains invalid name %q", name)
		}
	}
	if strings.TrimSpace(c.ArtifactPath) == "" {
		return fmt.Errorf("artifact_path is required")
	}
	if err := validateSHA256Hash(c.ExpectedArtifactHash, "expected_artifact_hash"); err != nil {
		return err
	}
	return nil
}

func validateSHA256Hash(value, field string) error {
	if !strings.HasPrefix(value, "sha256:") {
		return fmt.Errorf("%s must use sha256:<hex>", field)
	}
	decoded, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("expected_artifact_hash must use sha256:<hex>")
	}
	return nil
}

func (c ReplayContract) AutoReplayAllowed() bool {
	return c.SideEffect == SideEffectReadOnly || c.SideEffect == SideEffectIdempotent
}

func (c ReplayContract) RequiresHumanConfirmation() bool {
	return c.SideEffect == SideEffectMutating
}

func VerifyArtifactHash(path, expected string) error {
	if expected == "" {
		return nil
	}
	if err := validateSHA256Hash(expected, "expected artifact hash"); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading artifact: %w", err)
	}
	sum := sha256.Sum256(data)
	got := "sha256:" + hex.EncodeToString(sum[:])
	if got != expected {
		return fmt.Errorf("artifact hash mismatch: got %s", got)
	}
	return nil
}

func ContainedArtifactPath(root, artifact string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("artifact root is required")
	}
	if strings.TrimSpace(artifact) == "" {
		return "", fmt.Errorf("artifact path is required")
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolving artifact root: %w", err)
	}
	rootEval, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("resolving artifact root symlinks: %w", err)
	}

	candidate := artifact
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(rootEval, candidate)
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("resolving artifact path: %w", err)
	}

	resolved, err := evalPathWithExistingParent(candidateAbs)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootEval, resolved)
	if err != nil {
		return "", fmt.Errorf("checking artifact containment: %w", err)
	}
	if rel == "." || rel == "" {
		return "", fmt.Errorf("artifact path must name a file below the artifact root")
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", fmt.Errorf("artifact path escapes artifact root")
	}
	return resolved, nil
}

func validSideEffect(value SideEffectClass) bool {
	return slices.Contains([]SideEffectClass{
		SideEffectReadOnly,
		SideEffectIdempotent,
		SideEffectMutating,
	}, value)
}

func evalPathWithExistingParent(path string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("resolving artifact symlinks: %w", err)
	}

	dir := filepath.Dir(path)
	base := filepath.Base(path)
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("resolving artifact parent symlinks: %w", err)
	}
	return filepath.Join(resolvedDir, base), nil
}
