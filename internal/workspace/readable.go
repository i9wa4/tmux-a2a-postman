package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"syscall"
)

// ReadableName is a presentation alias, never a new storage or lock identity.
// Keep Name's real directory so preceding binaries and old archive paths use
// exactly the same records and lock inode. The suffix distinguishes component
// hyphens from path separators; an unexpected collision is refused.
func ReadableName(root string) (string, error) {
	name, err := Name(root)
	if err != nil {
		return "", err
	}
	return readableName(name), nil
}

func readableName(name string) string {
	if !strings.Contains(name, "%2D") {
		return name
	}
	pretty := strings.ReplaceAll(name, "%2D", "-")
	if len(pretty) > 200 {
		pretty = pretty[:200]
	}
	sum := sha256.Sum256([]byte(name))
	return pretty + "--" + hex.EncodeToString(sum[:16])
}

// checkReadableAlias never follows the alias. A known alias without its real
// target indicates missing history, not permission to initialize a new mailbox.
func checkReadableAlias(base *os.Root, name string) (bool, error) {
	alias := readableName(name)
	if alias == name {
		return true, nil
	}
	info, err := base.Lstat(alias)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	target, linkErr := base.Readlink(alias)
	if info.Mode()&os.ModeSymlink == 0 || !ok || stat.Uid != uint32(os.Geteuid()) || linkErr != nil || target != name {
		return false, errors.New("unsafe readable workspace alias: " + alias)
	}
	real, err := base.Lstat(name)
	if err != nil || !privateInfo(real, true) {
		return false, errors.New("readable workspace alias has missing or unsafe history: " + alias)
	}
	return true, nil
}

// Called under the existing workspace lock after the real identity is verified.
// Atomic publication is resumable: an interruption leaves either no alias or
// this exact link; it never moves history or hands off a lock.
func ensureReadableAlias(base *os.Root, name string) error {
	exists, err := checkReadableAlias(base, name)
	if err != nil || exists {
		return err
	}
	if err := base.Symlink(name, readableName(name)); err != nil {
		if errors.Is(err, os.ErrExist) {
			exists, checkErr := checkReadableAlias(base, name)
			if checkErr != nil {
				return checkErr
			}
			if !exists {
				return ErrUnknown
			}
			return syncDirectory(base)
		}
		return err
	}
	return syncDirectory(base)
}
