// Package cutover contains read-only inventory tools for planning a mailbox
// cutover. Inventory does not authorize or perform a migration.
package cutover

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Mapping identifies where one legacy regular file would land in a new root.
// Paths are relative to their respective roots.
type Mapping struct {
	Legacy string `json:"legacy"`
	Target string `json:"target"`
}

// Entry records a source file's observed metadata. It is an observation, not
// a lock or a promise that the file will remain unchanged.
type Entry struct {
	Legacy string `json:"legacy"`
	Target string `json:"target"`
	Size   int64  `json:"size"`
	Mode   string `json:"mode"`
	SHA256 string `json:"sha256"`
}

// Inventory is deliberately unable to express cutover approval. Writers and
// pending approvals must be fenced separately before any conversion.
type Inventory struct {
	LegacyRoot       string  `json:"legacy_root"`
	TargetRoot       string  `json:"target_root"`
	Entries          []Entry `json:"entries"`
	CutoverAllowed   bool    `json:"cutover_allowed"`
	RequiredEvidence string  `json:"required_evidence"`
}

// BuildInventory inspects explicit roots without creating or modifying files.
// It rejects links, special files, unmapped sources and occupied destinations.
func BuildInventory(legacyRoot, targetRoot string, mappings []Mapping) (Inventory, error) {
	result := Inventory{
		LegacyRoot: legacyRoot, TargetRoot: targetRoot, Entries: []Entry{},
		RequiredEvidence: "all old and new writers enrolled in a durable generation fence; pending approvals drained; conversion and rollback independently verified",
	}
	if !filepath.IsAbs(legacyRoot) || !filepath.IsAbs(targetRoot) {
		return result, errors.New("both roots must be absolute")
	}
	legacyRoot = filepath.Clean(legacyRoot)
	targetRoot = filepath.Clean(targetRoot)
	result.LegacyRoot, result.TargetRoot = legacyRoot, targetRoot
	if legacyRoot == targetRoot || within(legacyRoot, targetRoot) || within(targetRoot, legacyRoot) {
		return result, errors.New("roots must be distinct and disjoint")
	}
	if err := checkDirectory(legacyRoot); err != nil {
		return result, fmt.Errorf("legacy root: %w", err)
	}
	if err := checkDirectory(targetRoot); err != nil {
		return result, fmt.Errorf("target root: %w", err)
	}
	// Compare resolved roots too: a symlink in an ancestor can make two
	// different spellings refer to overlapping trees.
	resolvedLegacy, err := filepath.EvalSymlinks(legacyRoot)
	if err != nil {
		return result, err
	}
	resolvedTarget, err := filepath.EvalSymlinks(targetRoot)
	if err != nil {
		return result, err
	}
	if resolvedLegacy == resolvedTarget || within(resolvedLegacy, resolvedTarget) || within(resolvedTarget, resolvedLegacy) {
		return result, errors.New("resolved roots must be distinct and disjoint")
	}
	byLegacy := make(map[string]string, len(mappings))
	byTarget := make(map[string]bool, len(mappings))
	for _, mapping := range mappings {
		if err := validRelative(mapping.Legacy); err != nil {
			return result, fmt.Errorf("legacy mapping %q: %w", mapping.Legacy, err)
		}
		if err := validRelative(mapping.Target); err != nil {
			return result, fmt.Errorf("target mapping %q: %w", mapping.Target, err)
		}
		if _, exists := byLegacy[mapping.Legacy]; exists {
			return result, fmt.Errorf("duplicate legacy mapping %q", mapping.Legacy)
		}
		if byTarget[mapping.Target] {
			return result, fmt.Errorf("duplicate target mapping %q", mapping.Target)
		}
		byLegacy[mapping.Legacy] = mapping.Target
		byTarget[mapping.Target] = true
	}
	seen := make(map[string]bool, len(mappings))
	err = filepath.WalkDir(legacyRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == legacyRoot {
			return nil
		}
		rel, err := filepath.Rel(legacyRoot, path)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("link in legacy root: %s", rel)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("nonregular legacy file: %s", rel)
		}
		target, mapped := byLegacy[rel]
		if !mapped {
			return fmt.Errorf("unmapped legacy file: %s", rel)
		}
		if err := checkDestination(targetRoot, target); err != nil {
			return fmt.Errorf("target %q: %w", target, err)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err == nil && !info.Mode().IsRegular() {
			err = fmt.Errorf("source changed type: %s", rel)
		}
		var digest string
		if err == nil {
			hash := sha256.New()
			_, err = io.Copy(hash, file)
			digest = hex.EncodeToString(hash.Sum(nil))
		}
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		result.Entries = append(result.Entries, Entry{Legacy: rel, Target: target, Size: info.Size(), Mode: info.Mode().String(), SHA256: digest})
		seen[rel] = true
		return nil
	})
	if err != nil {
		return result, err
	}
	for source := range byLegacy {
		if !seen[source] {
			return result, fmt.Errorf("mapping has no source: %s", source)
		}
	}
	sort.Slice(result.Entries, func(i, j int) bool { return result.Entries[i].Legacy < result.Entries[j].Legacy })
	return result, nil
}

func validRelative(path string) error {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || path == "." {
		return errors.New("path must be clean and relative")
	}
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if component == ".." || component == "." || component == "" {
			return errors.New("path has an invalid component")
		}
	}
	return nil
}

func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func checkDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a real directory: %s", path)
	}
	return nil
}

func checkDestination(root, rel string) error {
	parts := strings.Split(rel, string(filepath.Separator))
	path := root
	for i, part := range parts {
		path = filepath.Join(path, part)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if i == len(parts)-1 {
			return fmt.Errorf("destination exists: %s", rel)
		}
		if !info.IsDir() {
			return fmt.Errorf("destination parent is not a real directory: %s", path)
		}
	}
	return nil
}
