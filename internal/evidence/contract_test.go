package evidence

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestContainedArtifactPathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	if _, err := ContainedArtifactPath(root, "../outside.txt"); err == nil {
		t.Fatal("ContainedArtifactPath() error = nil, want traversal rejection")
	}
}

func TestContainedArtifactPathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}

	if _, err := ContainedArtifactPath(root, filepath.Join("escape", "artifact.txt")); err == nil {
		t.Fatal("ContainedArtifactPath() error = nil, want symlink escape rejection")
	}
}

func TestReplayContractValidationAndAutoReplayClasses(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "artifact.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	contract := ReplayContract{
		Command:              "go test ./...",
		CWD:                  root,
		EnvAllowlist:         []string{"PATH", "HOME"},
		Timeout:              time.Minute,
		SideEffect:           SideEffectIdempotent,
		ArtifactPath:         "artifact.txt",
		ExpectedArtifactHash: "sha256:2689367b205c16ce32ed4200942b8b8b1e262dfc70d9bc9fbc77c49699a4f1df",
	}
	if err := contract.Validate(root); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !contract.AutoReplayAllowed() {
		t.Fatal("AutoReplayAllowed() = false, want true for idempotent")
	}

	contract.SideEffect = SideEffectMutating
	if contract.AutoReplayAllowed() {
		t.Fatal("AutoReplayAllowed() = true, want false for mutating")
	}
	if !contract.RequiresHumanConfirmation() {
		t.Fatal("RequiresHumanConfirmation() = false, want true for mutating")
	}
}
