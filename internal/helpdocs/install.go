package helpdocs

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Songmu/skillsmith"
)

// InstallOptions intentionally exposes first-time install/export only. There
// is no update/uninstall/force path or instruction-file rewrite.
type InstallOptions struct {
	Client    string
	Scope     string
	Project   string
	ExportDir string
	Names     []string
	Version   string
}

type InstallAction struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Outcome string `json:"outcome"`
}

type InstallResult struct {
	Target  string          `json:"target"`
	Actions []InstallAction `json:"actions"`
}

func EntryNames() []string {
	entries, _ := fs.ReadDir(Entries(), ".")
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names
}

type selectedEntries struct {
	fs.FS
	names map[string]bool
}

func (s selectedEntries) Open(name string) (fs.File, error) {
	if name != "." && !s.names[strings.Split(name, "/")[0]] {
		return nil, fs.ErrNotExist
	}
	return s.FS.Open(name)
}

func (s selectedEntries) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." && !s.names[strings.Split(name, "/")[0]] {
		return nil, fs.ErrNotExist
	}
	entries, err := fs.ReadDir(s.FS, name)
	if err != nil {
		return nil, err
	}
	if name != "." {
		return entries, nil
	}
	var selected []fs.DirEntry
	for _, entry := range entries {
		if s.names[entry.Name()] {
			selected = append(selected, entry)
		}
	}
	return selected, nil
}

func installTarget(opts InstallOptions) (string, error) {
	if opts.ExportDir != "" {
		if opts.Client != "" || opts.Scope != "" || opts.Project != "" || !filepath.IsAbs(opts.ExportDir) {
			return "", errors.New("export requires one absolute --to and no client/scope/project options")
		}
		// Resolve declared outer paths (including macOS /var aliases), but never
		// turn a target symlink into permission to overwrite its destination.
		if info, err := os.Lstat(opts.ExportDir); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("export target is a symlink")
		}
		outer, err := filepath.EvalSymlinks(filepath.Dir(opts.ExportDir))
		if err != nil {
			return "", err
		}
		return filepath.Join(outer, filepath.Base(opts.ExportDir)), nil
	}
	if opts.Client != "claude" && opts.Client != "codex" {
		return "", errors.New("install requires --client claude or --client codex")
	}
	var base string
	switch opts.Scope {
	case "user":
		var err error
		base, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if opts.Project != "" {
			return "", errors.New("--project is only valid with --scope project")
		}
	case "project":
		base = opts.Project
	default:
		return "", errors.New("install requires --scope user or --scope project")
	}
	if !filepath.IsAbs(base) {
		return "", errors.New("installation scope requires an absolute home/project directory")
	}
	base, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", err
	}
	directory := ".agents" // Codex's native path, unlike Claude's dedicated path.
	if opts.Client == "claude" {
		directory = ".claude"
	}
	return filepath.Join(base, directory, "skills"), nil
}

func checkedInstallRoot(target string) (*os.Root, error) {
	if !filepath.IsAbs(target) || target == "/nix/store" || strings.HasPrefix(target, "/nix/store/") {
		return nil, errors.New("refusing an invalid or Nix-owned installation target")
	}
	// Check every existing managed path component before creating descendants.
	// Resolve only the caller-selected outer scope, never an installed Skill link.
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(filepath.Clean(target), current), current) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) {
			if err := os.Mkdir(current, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
				return nil, err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("installation path %q is not a real directory; use declarative installation for managed trees", current)
		}
	}
	return os.OpenRoot(target)
}

func existingEntry(root *os.Root, name string, want []byte) (bool, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("skill %q conflicts with an existing or Nix-owned entry", name)
	}
	file := filepath.Join(name, "SKILL.md")
	info, err = root.Lstat(file)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("skill %q is customized, incomplete or symlink-managed", name)
	}
	data, err := root.ReadFile(file)
	if err != nil || string(data) != string(want) {
		return false, fmt.Errorf("skill %q already exists with different contents; no overwrite is permitted", name)
	}
	return true, nil
}

// Install uses skillsmith v0.2.0 only inside an owned private staging tree.
// Its general update/force operations never receive a user's installation root.
func Install(ctx context.Context, opts InstallOptions) (InstallResult, error) {
	result := InstallResult{Actions: []InstallAction{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	known := map[string]bool{}
	for _, name := range EntryNames() {
		known[name] = true
	}
	selected := map[string]bool{}
	if len(opts.Names) == 0 {
		return result, errors.New("select at least one embedded Skill by name")
	}
	for _, name := range opts.Names {
		if !known[name] || selected[name] {
			return result, fmt.Errorf("unknown or duplicate Skill %q", name)
		}
		selected[name] = true
	}
	target, err := installTarget(opts)
	if err != nil {
		return result, err
	}
	result.Target = target
	if opts.Client == "codex" && opts.ExportDir == "" {
		legacy := filepath.Join(filepath.Dir(filepath.Dir(target)), ".codex", "skills")
		for _, name := range opts.Names {
			if _, err := os.Lstat(filepath.Join(legacy, name)); err == nil {
				return result, fmt.Errorf("codex skill %q already exists in the legacy .codex/skills tree; use its existing owner or export for review", name)
			} else if !errors.Is(err, fs.ErrNotExist) {
				return result, err
			}
		}
	}
	root, err := checkedInstallRoot(target)
	if err != nil {
		return result, err
	}
	defer func() { _ = root.Close() }()
	if _, err := root.Lstat(".agent-skills-managed.json"); err == nil {
		return result, errors.New("target is declaratively managed; consume the repository thin entries through Nix instead")
	}
	originals := map[string][]byte{}
	available := map[string]bool{}
	for _, name := range opts.Names {
		data, err := fs.ReadFile(Entries(), path.Join(name, "SKILL.md"))
		if err != nil {
			return result, err
		}
		originals[name] = data
		already, err := existingEntry(root, name, data)
		if err != nil {
			return result, err
		}
		if already {
			available[name] = true
			result.Actions = append(result.Actions, InstallAction{name, filepath.Join(target, name), "already_available"})
		}
	}
	stage, err := os.MkdirTemp("", "postman-skill-install-")
	if err != nil {
		return result, err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	copied, err := skillsmith.CopySkills(selectedEntries{Entries(), selected}, stage, skillsmith.CopyOptions{Mode: skillsmith.ModeInstall, Name: "tmux-a2a-postman", Version: opts.Version})
	if err != nil {
		return result, err
	}
	if len(copied.Warned()) != 0 || len(copied.Installed()) != len(selected) {
		return result, errors.New("embedded Skill metadata did not produce the complete selected set")
	}
	license, err := fs.ReadFile(Assets(), "licenses/postman.txt")
	if err != nil {
		return result, err
	}
	for _, name := range opts.Names {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		already, err := existingEntry(root, name, originals[name])
		if err != nil {
			return result, err
		}
		if already {
			if !available[name] {
				result.Actions = append(result.Actions, InstallAction{name, filepath.Join(target, name), "already_available"})
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(stage, name, "LICENSE"), license, 0o644); err != nil {
			return result, err
		}
		if err := root.Mkdir(name, 0o755); err != nil {
			return result, fmt.Errorf("reserve selected Skill %q: %w", name, err)
		}
		destination, err := root.OpenRoot(name)
		if err != nil {
			return result, err
		}
		err = publishEntry(destination, filepath.Join(stage, name))
		own, statErr := destination.Stat(".")
		current, currentErr := root.Lstat(name)
		closeErr := destination.Close()
		if err != nil || statErr != nil || currentErr != nil || !os.SameFile(own, current) {
			return result, errors.Join(err, statErr, currentErr, closeErr, errors.New("installation did not complete at the reserved Skill directory"))
		}
		if closeErr != nil {
			return result, closeErr
		}
		result.Actions = append(result.Actions, InstallAction{name, filepath.Join(target, name), "installed"})
	}
	return result, nil
}

func publishEntry(root *os.Root, stage string) error {
	// Ancillary files come first; a complete SKILL.md is linked into view last.
	entries, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "SKILL.md" {
			continue
		}
		if !entry.Type().IsRegular() {
			return errors.New("unexpected staged entry")
		}
		data, err := os.ReadFile(filepath.Join(stage, entry.Name()))
		if err != nil {
			return err
		}
		if err := writeNewFile(root, entry.Name(), data, 0o644); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(filepath.Join(stage, "SKILL.md"))
	if err != nil {
		return err
	}
	temporary := ".entry-" + rand.Text()
	defer func() { _ = root.Remove(temporary) }()
	if err := writeNewFile(root, temporary, data, 0o644); err != nil {
		return err
	}
	return root.Link(temporary, "SKILL.md")
}

func writeNewFile(root *os.Root, name string, data []byte, mode fs.FileMode) error {
	out, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, writeErr := out.Write(data)
	modeErr := out.Chmod(mode)
	syncErr, closeErr := out.Sync(), out.Close()
	return errors.Join(writeErr, modeErr, syncErr, closeErr)
}
