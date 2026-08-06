//go:build !(linux || darwin || freebsd || netbsd || openbsd)

package store

import "fmt"

type boundArchiveSource struct{}

func openBoundArchiveSource(sourcePath string, data []byte) (*boundArchiveSource, error) {
	return nil, fmt.Errorf("verified archive object binding is unsupported on this platform")
}

func recoverArchiveBindingsInInboxDir(string, string) error {
	return fmt.Errorf("verified archive object binding is unsupported on this platform")
}

func (s *boundArchiveSource) Close() error {
	return nil
}

func (s *boundArchiveSource) Stage(func()) (string, error) {
	return "", fmt.Errorf("verified archive object binding is unsupported on this platform")
}

func (s *boundArchiveSource) UnlinkStage(string, func()) error {
	return fmt.Errorf("verified archive object binding is unsupported on this platform")
}

func (s *boundArchiveSource) Restore(string) error {
	return nil
}

func (s *boundArchiveSource) archiveBindingCleanupComplete(string) bool {
	return false
}
