//go:build linux || darwin || freebsd || netbsd || openbsd

package store

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

type boundArchiveSource struct {
	sourcePath string
	baseName   string
	want       []byte
	parent     *os.File
	sourceInfo fs.FileInfo
}

type archiveBindingManifest struct {
	SchemaVersion int    `json:"schema_version"`
	BaseName      string `json:"base_name"`
	StageName     string `json:"stage_name"`
	Device        uint64 `json:"device"`
	Inode         uint64 `json:"inode"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
}

var syncArchiveBindingDirectory = func(dir *os.File) error {
	return dir.Sync()
}

var beforeRecoverArchiveStageRemove = func(string) {}

func openBoundArchiveSource(sourcePath string, data []byte) (*boundArchiveSource, error) {
	dir := filepath.Dir(sourcePath)
	base := filepath.Base(sourcePath)
	parentInfo, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("checking archive source directory: %w", err)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("checking archive source directory: refusing symlink %s", dir)
	}
	parent, err := os.Open(dir)
	if err != nil {
		return nil, fmt.Errorf("opening archive source directory: %w", err)
	}
	keepParent := false
	defer func() {
		if !keepParent {
			_ = parent.Close()
		}
	}()
	parentDescriptorInfo, err := parent.Stat()
	if err != nil {
		return nil, fmt.Errorf("stating archive source directory descriptor: %w", err)
	}
	if !os.SameFile(parentInfo, parentDescriptorInfo) {
		return nil, fmt.Errorf("checking archive source directory: directory object changed for %s", dir)
	}
	fd, err := unix.Openat(int(parent.Fd()), base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening archive source descriptor: %w", err)
	}
	file := os.NewFile(uintptr(fd), sourcePath)
	defer file.Close()
	sourceInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stating archive source descriptor: %w", err)
	}
	if !sourceInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("checking archive source descriptor: refusing non-regular file %s", sourcePath)
	}
	current, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("reading archive source descriptor for verification: %w", err)
	}
	if !bytes.Equal(current, data) {
		return nil, fmt.Errorf("checking archive source: source bytes changed for %s", sourcePath)
	}
	currentInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("checking archive source descriptor after read: %w", err)
	}
	if !os.SameFile(sourceInfo, currentInfo) {
		return nil, fmt.Errorf("checking archive source descriptor after read: source object changed for %s", sourcePath)
	}
	keepParent = true
	return &boundArchiveSource{
		sourcePath: sourcePath,
		baseName:   base,
		want:       append([]byte(nil), data...),
		parent:     parent,
		sourceInfo: sourceInfo,
	}, nil
}

func openBoundArchiveSourceAt(sourceDir *os.File, sourcePath, base string, data []byte) (*boundArchiveSource, error) {
	if sourceDir == nil {
		return nil, fmt.Errorf("opening archive source directory descriptor: nil descriptor")
	}
	if base == "" || base != filepath.Base(base) {
		return nil, fmt.Errorf("opening archive source descriptor: invalid base name %q", base)
	}
	sourceDirInfo, err := sourceDir.Stat()
	if err != nil {
		return nil, fmt.Errorf("stating archive source directory descriptor: %w", err)
	}
	if !sourceDirInfo.IsDir() {
		return nil, fmt.Errorf("checking archive source directory descriptor: refusing non-directory %s", filepath.Dir(sourcePath))
	}
	parentFD, err := unix.Dup(int(sourceDir.Fd()))
	if err != nil {
		return nil, fmt.Errorf("duplicating archive source directory descriptor: %w", err)
	}
	parent := os.NewFile(uintptr(parentFD), filepath.Dir(sourcePath))
	if parent == nil {
		_ = unix.Close(parentFD)
		return nil, fmt.Errorf("duplicating archive source directory descriptor: invalid file descriptor")
	}
	keepParent := false
	defer func() {
		if !keepParent {
			_ = parent.Close()
		}
	}()
	fd, err := unix.Openat(int(parent.Fd()), base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("opening archive source descriptor: %w", err)
	}
	file := os.NewFile(uintptr(fd), sourcePath)
	defer file.Close()
	sourceInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stating archive source descriptor: %w", err)
	}
	if !sourceInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("checking archive source descriptor: refusing non-regular file %s", sourcePath)
	}
	current, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("reading archive source descriptor for verification: %w", err)
	}
	if !bytes.Equal(current, data) {
		return nil, fmt.Errorf("checking archive source: source bytes changed for %s", sourcePath)
	}
	currentInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("checking archive source descriptor after read: %w", err)
	}
	if !os.SameFile(sourceInfo, currentInfo) {
		return nil, fmt.Errorf("checking archive source descriptor after read: source object changed for %s", sourcePath)
	}
	keepParent = true
	return &boundArchiveSource{
		sourcePath: sourcePath,
		baseName:   base,
		want:       append([]byte(nil), data...),
		parent:     parent,
		sourceInfo: sourceInfo,
	}, nil
}

func (s *boundArchiveSource) Close() error {
	if s == nil || s.parent == nil {
		return nil
	}
	return s.parent.Close()
}

func (s *boundArchiveSource) Stage(beforeStage func()) (string, error) {
	if beforeStage != nil {
		beforeStage()
	}
	stageName, err := privateArchiveStageName(s.baseName)
	if err != nil {
		return "", err
	}
	if err := s.writeBindingManifest(stageName); err != nil {
		return "", err
	}
	if err := unix.Renameat(int(s.parent.Fd()), s.baseName, int(s.parent.Fd()), stageName); err != nil {
		_ = s.removeBindingManifest(stageName)
		return "", fmt.Errorf("staging archive source: %w", err)
	}
	if err := s.verifyStage(stageName); err != nil {
		_ = s.Restore(stageName)
		return "", err
	}
	if err := syncArchiveBindingDirectory(s.parent); err != nil {
		_ = s.Restore(stageName)
		return "", fmt.Errorf("syncing archive source directory after staging: %w", err)
	}
	return stageName, nil
}

func (s *boundArchiveSource) UnlinkStage(stageName string, beforeUnlink func()) error {
	if beforeUnlink != nil {
		beforeUnlink()
	}
	if err := s.verifyStage(stageName); err != nil {
		return err
	}
	if err := unix.Unlinkat(int(s.parent.Fd()), stageName, 0); err != nil {
		return fmt.Errorf("removing staged archive source: %w", err)
	}
	if err := syncArchiveBindingDirectory(s.parent); err != nil {
		return fmt.Errorf("syncing archive source directory after stage removal: %w", err)
	}
	if err := s.removeBindingManifest(stageName); err != nil {
		return err
	}
	if err := syncArchiveBindingDirectory(s.parent); err != nil {
		return fmt.Errorf("syncing archive source directory after manifest removal: %w", err)
	}
	return nil
}

func (s *boundArchiveSource) Restore(stageName string) error {
	fd, err := unix.Openat(int(s.parent.Fd()), s.baseName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err == nil {
		_ = unix.Close(fd)
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err := unix.Renameat(int(s.parent.Fd()), stageName, int(s.parent.Fd()), s.baseName); err != nil {
		return err
	}
	if err := syncArchiveBindingDirectory(s.parent); err != nil {
		return fmt.Errorf("syncing archive source directory after restore: %w", err)
	}
	if err := s.removeBindingManifest(stageName); err != nil {
		return err
	}
	return syncArchiveBindingDirectory(s.parent)
}

func (s *boundArchiveSource) archiveBindingCleanupComplete(stageName string) bool {
	stageMissing := archiveBindingEntryMissing(s.parent, stageName)
	manifestMissing := archiveBindingEntryMissing(s.parent, archiveBindingManifestName(stageName))
	return stageMissing && manifestMissing
}

func archiveBindingEntryMissing(parent *os.File, name string) bool {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err == nil {
		_ = unix.Close(fd)
		return false
	}
	return os.IsNotExist(err)
}

func (s *boundArchiveSource) verifyStage(stageName string) error {
	fd, err := unix.Openat(int(s.parent.Fd()), stageName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("opening staged archive source descriptor: %w", err)
	}
	file := os.NewFile(uintptr(fd), filepath.Join(filepath.Dir(s.sourcePath), stageName))
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stating staged archive source descriptor: %w", err)
	}
	if !os.SameFile(s.sourceInfo, info) {
		return fmt.Errorf("checking staged archive source: source object changed for %s", s.sourcePath)
	}
	current, err := io.ReadAll(file)
	if err != nil {
		return fmt.Errorf("reading staged archive source descriptor: %w", err)
	}
	if !bytes.Equal(current, s.want) {
		return fmt.Errorf("checking staged archive source: source bytes changed for %s", s.sourcePath)
	}
	return nil
}

func privateArchiveStageName(baseName string) (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generating archive source stage name: %w", err)
	}
	return "." + baseName + ".archive-" + hex.EncodeToString(raw[:]), nil
}

func archiveBindingManifestName(stageName string) string {
	return stageName + ".bind.json"
}

func (s *boundArchiveSource) writeBindingManifest(stageName string) error {
	device, inode, err := fileIdentity(s.sourceInfo)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(s.want)
	manifest := archiveBindingManifest{
		SchemaVersion: 1,
		BaseName:      s.baseName,
		StageName:     stageName,
		Device:        device,
		Inode:         inode,
		Size:          s.sourceInfo.Size(),
		SHA256:        hex.EncodeToString(sum[:]),
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encoding archive binding manifest: %w", err)
	}
	data = append(data, '\n')
	tmpName, err := privateArchiveStageName(s.baseName + ".bind")
	if err != nil {
		return err
	}
	if err := writeFileAt(s.parent, tmpName, data, 0o600); err != nil {
		_ = unix.Unlinkat(int(s.parent.Fd()), tmpName, 0)
		return err
	}
	if err := unix.Renameat(int(s.parent.Fd()), tmpName, int(s.parent.Fd()), archiveBindingManifestName(stageName)); err != nil {
		_ = unix.Unlinkat(int(s.parent.Fd()), tmpName, 0)
		return fmt.Errorf("publishing archive binding manifest: %w", err)
	}
	if err := syncArchiveBindingDirectory(s.parent); err != nil {
		_ = s.removeBindingManifest(stageName)
		return fmt.Errorf("syncing archive binding manifest: %w", err)
	}
	return nil
}

func (s *boundArchiveSource) removeBindingManifest(stageName string) error {
	if err := unix.Unlinkat(int(s.parent.Fd()), archiveBindingManifestName(stageName), 0); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing archive binding manifest: %w", err)
	}
	return nil
}

func writeFileAt(parent *os.File, name string, data []byte, perm os.FileMode) error {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC, uint32(perm))
	if err != nil {
		return fmt.Errorf("creating archive binding manifest: %w", err)
	}
	file := os.NewFile(uintptr(fd), name)
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("writing archive binding manifest: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("syncing archive binding manifest file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("closing archive binding manifest file: %w", err)
	}
	return nil
}

func fileIdentity(info fs.FileInfo) (uint64, uint64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, fmt.Errorf("checking archive source identity: stat_t unavailable")
	}
	return uint64(stat.Dev), uint64(stat.Ino), nil
}

func recoverArchiveBindingsInInboxDir(inboxDir, readDir string) error {
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading archive binding directory: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".bind.json") {
			continue
		}
		if err := recoverArchiveBinding(filepath.Join(inboxDir, name), readDir); err != nil {
			return err
		}
	}
	return nil
}

func recoverArchiveBinding(manifestPath, readDir string) error {
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil {
		return fmt.Errorf("checking archive binding manifest: %w", err)
	}
	if !manifestInfo.Mode().IsRegular() {
		return fmt.Errorf("checking archive binding manifest: refusing non-regular file %s", manifestPath)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("reading archive binding manifest: %w", err)
	}
	var manifest archiveBindingManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("decoding archive binding manifest %s: %w", manifestPath, err)
	}
	if err := validateArchiveBindingManifest(filepath.Base(manifestPath), manifest); err != nil {
		return err
	}
	inboxDir := filepath.Dir(manifestPath)
	stagePath := filepath.Join(inboxDir, manifest.StageName)
	sourcePath := filepath.Join(inboxDir, manifest.BaseName)
	readPath := filepath.Join(readDir, manifest.BaseName)

	sourceData, sourceExists, err := verifiedManifestFileContent(sourcePath, manifest, "source")
	if err != nil {
		return err
	}
	stageData, stageExists, err := verifiedManifestFileContent(stagePath, manifest, "stage")
	if err != nil {
		return err
	}
	readData, readExists, err := verifiedArchiveReadContent(readPath)
	if err != nil {
		return err
	}

	switch {
	case sourceExists && stageExists:
		return fmt.Errorf("recovering archive binding: source and stage both exist for %s", manifest.BaseName)
	case sourceExists && readExists:
		return fmt.Errorf("recovering archive binding: source and read archive both exist for %s", manifest.BaseName)
	case stageExists && readExists:
		if !bytes.Equal(readData, stageData) {
			return fmt.Errorf("recovering archive binding: read archive content differs for %s", manifest.BaseName)
		}
		beforeRecoverArchiveStageRemove(stagePath)
		if err := os.Remove(stagePath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing recovered archive stage: %w", err)
		}
		if err := syncDirectory(inboxDir); err != nil {
			return fmt.Errorf("syncing archive binding stage removal: %w", err)
		}
		return removeRecoveredArchiveManifest(manifestPath, inboxDir)
	case stageExists:
		if err := os.Rename(stagePath, sourcePath); err != nil {
			return fmt.Errorf("restoring archive binding source: %w", err)
		}
		if err := syncDirectory(inboxDir); err != nil {
			return fmt.Errorf("syncing archive binding restore: %w", err)
		}
		return removeRecoveredArchiveManifest(manifestPath, inboxDir)
	case sourceExists:
		if len(sourceData) == 0 && manifest.Size != 0 {
			return fmt.Errorf("recovering archive binding: source content unexpectedly empty for %s", manifest.BaseName)
		}
		return removeRecoveredArchiveManifest(manifestPath, inboxDir)
	case readExists:
		sum := sha256.Sum256(readData)
		if int64(len(readData)) != manifest.Size || hex.EncodeToString(sum[:]) != manifest.SHA256 {
			return fmt.Errorf("recovering archive binding: read archive content differs for %s", manifest.BaseName)
		}
		return removeRecoveredArchiveManifest(manifestPath, inboxDir)
	default:
		return fmt.Errorf("recovering archive binding: manifest has no source, stage, or read archive for %s", manifest.BaseName)
	}
}

func validateArchiveBindingManifest(manifestFile string, manifest archiveBindingManifest) error {
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("recovering archive binding: unsupported manifest schema %d", manifest.SchemaVersion)
	}
	if manifest.BaseName == "" || manifest.BaseName != filepath.Base(manifest.BaseName) {
		return fmt.Errorf("recovering archive binding: invalid base name %q", manifest.BaseName)
	}
	if manifest.StageName == "" || manifest.StageName != filepath.Base(manifest.StageName) {
		return fmt.Errorf("recovering archive binding: invalid stage name %q", manifest.StageName)
	}
	if archiveBindingManifestName(manifest.StageName) != manifestFile {
		return fmt.Errorf("recovering archive binding: manifest filename does not match stage")
	}
	if !strings.HasPrefix(manifest.StageName, "."+manifest.BaseName+".archive-") {
		return fmt.Errorf("recovering archive binding: stage name does not bind base name")
	}
	if manifest.Device == 0 || manifest.Inode == 0 || manifest.Size < 0 || manifest.SHA256 == "" {
		return fmt.Errorf("recovering archive binding: incomplete manifest for %s", manifest.BaseName)
	}
	return nil
}

func verifiedManifestFileContent(path string, manifest archiveBindingManifest, role string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("checking archive binding %s: %w", role, err)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("checking archive binding %s: refusing non-regular file %s", role, path)
	}
	device, inode, err := fileIdentity(info)
	if err != nil {
		return nil, false, err
	}
	if device != manifest.Device || inode != manifest.Inode || info.Size() != manifest.Size {
		return nil, false, fmt.Errorf("checking archive binding %s: object identity changed for %s", role, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, fmt.Errorf("reading archive binding %s: %w", role, err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != manifest.SHA256 {
		return nil, false, fmt.Errorf("checking archive binding %s: content hash changed for %s", role, path)
	}
	return data, true, nil
}

func verifiedArchiveReadContent(readPath string) ([]byte, bool, error) {
	info, err := os.Lstat(readPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("checking archive binding read archive: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("recovering archive binding: refusing non-regular read archive %s", readPath)
	}
	data, err := os.ReadFile(readPath)
	if err != nil {
		return nil, false, fmt.Errorf("reading archive binding read archive: %w", err)
	}
	return data, true, nil
}

func removeRecoveredArchiveManifest(manifestPath, inboxDir string) error {
	if err := os.Remove(manifestPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing recovered archive binding manifest: %w", err)
	}
	return syncDirectory(inboxDir)
}
