package config

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

type fakeSignalProcess struct {
	t   *testing.T
	err error
}

func (p fakeSignalProcess) Signal(sig os.Signal) error {
	p.t.Helper()
	if sig != syscall.Signal(0) {
		p.t.Fatalf("Signal(%v), want signal 0", sig)
	}
	return p.err
}

type fakeFileInfo struct {
	name string
	uid  int
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0o600 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

func TestSessionPIDProbeLivenessMatrix(t *testing.T) {
	tests := []struct {
		name      string
		signalErr error
		wantAlive bool
		wantOwned bool
	}{
		{
			name:      "nil signal result means alive and owned when UID matches",
			wantAlive: true,
			wantOwned: true,
		},
		{
			name:      "EPERM means alive but not current-user owned",
			signalErr: syscall.EPERM,
			wantAlive: true,
		},
		{
			name:      "ESRCH means dead",
			signalErr: syscall.ESRCH,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := sessionPIDProbe{
				readFile: func(string) ([]byte, error) {
					return []byte("1234\n"), nil
				},
				findProcess: func(pid int) (signalProcess, error) {
					if pid != 1234 {
						t.Fatalf("findProcess(%d), want 1234", pid)
					}
					return fakeSignalProcess{t: t, err: tt.signalErr}, nil
				},
				stat: func(string) (os.FileInfo, error) {
					return fakeFileInfo{uid: 501}, nil
				},
				fileOwnerUID: func(info os.FileInfo) (int, bool) {
					return info.(fakeFileInfo).uid, true
				},
				currentUID: func() int {
					return 501
				},
			}

			pidPath := filepath.Join("ctx", "sess", "postman.pid")
			if got := probe.isPIDAlive(pidPath); got != tt.wantAlive {
				t.Fatalf("isPIDAlive() = %v, want %v", got, tt.wantAlive)
			}
			if got := probe.isPIDOwnedByCurrentUser(pidPath); got != tt.wantOwned {
				t.Fatalf("isPIDOwnedByCurrentUser() = %v, want %v", got, tt.wantOwned)
			}
		})
	}
}

func TestSessionPIDProbeRejectsInvalidPIDFiles(t *testing.T) {
	tests := []struct {
		name    string
		content string
		err     error
	}{
		{name: "missing pid file", err: os.ErrNotExist},
		{name: "unreadable pid file", err: os.ErrPermission},
		{name: "invalid pid content", content: "not-a-pid"},
		{name: "zero pid", content: "0"},
		{name: "negative pid", content: "-7"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findCalled := false
			probe := sessionPIDProbe{
				readFile: func(string) ([]byte, error) {
					if tt.err != nil {
						return nil, tt.err
					}
					return []byte(tt.content), nil
				},
				findProcess: func(int) (signalProcess, error) {
					findCalled = true
					return fakeSignalProcess{t: t}, nil
				},
				stat: func(string) (os.FileInfo, error) {
					return fakeFileInfo{uid: 501}, nil
				},
				fileOwnerUID: func(info os.FileInfo) (int, bool) {
					return info.(fakeFileInfo).uid, true
				},
				currentUID: func() int {
					return 501
				},
			}

			pidPath := filepath.Join("ctx", "sess", "postman.pid")
			if probe.isPIDAlive(pidPath) {
				t.Fatal("isPIDAlive() = true, want false")
			}
			if probe.isPIDOwnedByCurrentUser(pidPath) {
				t.Fatal("isPIDOwnedByCurrentUser() = true, want false")
			}
			if findCalled {
				t.Fatal("findProcess was called for an invalid pid file")
			}
		})
	}
}

func TestSessionPIDProbeOwnershipFallbacks(t *testing.T) {
	tests := []struct {
		name       string
		ownerUID   int
		ownerUIDOK bool
		currentUID int
		wantOwned  bool
	}{
		{
			name:       "non-current UID is alive but not owned",
			ownerUID:   502,
			ownerUIDOK: true,
			currentUID: 501,
		},
		{
			name:       "non-Unix stat fallback is alive but not owned",
			ownerUIDOK: false,
			currentUID: 501,
		},
		{
			name:       "current UID is owned",
			ownerUID:   501,
			ownerUIDOK: true,
			currentUID: 501,
			wantOwned:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probe := sessionPIDProbe{
				readFile: func(string) ([]byte, error) {
					return []byte("1234"), nil
				},
				findProcess: func(int) (signalProcess, error) {
					return fakeSignalProcess{t: t}, nil
				},
				stat: func(string) (os.FileInfo, error) {
					return fakeFileInfo{uid: tt.ownerUID}, nil
				},
				fileOwnerUID: func(info os.FileInfo) (int, bool) {
					return info.(fakeFileInfo).uid, tt.ownerUIDOK
				},
				currentUID: func() int {
					return tt.currentUID
				},
			}

			pidPath := filepath.Join("ctx", "sess", "postman.pid")
			if !probe.isPIDAlive(pidPath) {
				t.Fatal("isPIDAlive() = false, want true")
			}
			if got := probe.isPIDOwnedByCurrentUser(pidPath); got != tt.wantOwned {
				t.Fatalf("isPIDOwnedByCurrentUser() = %v, want %v", got, tt.wantOwned)
			}
		})
	}
}

func TestFindCurrentUserDaemonUsesPIDProbeAcrossMultipleContexts(t *testing.T) {
	baseDir := t.TempDir()
	foreignPID := writePIDFileForProbeTest(t, baseDir, "ctx-foreign", "daemon", "1001")
	currentPID := writePIDFileForProbeTest(t, baseDir, "ctx-current", "main", "1002")

	withSessionPIDProbe(t, sessionPIDProbe{
		readFile: os.ReadFile,
		findProcess: func(int) (signalProcess, error) {
			return fakeSignalProcess{t: t}, nil
		},
		stat: func(path string) (os.FileInfo, error) {
			switch path {
			case foreignPID:
				return fakeFileInfo{name: "postman.pid", uid: 502}, nil
			case currentPID:
				return fakeFileInfo{name: "postman.pid", uid: 501}, nil
			default:
				t.Fatalf("unexpected stat path %q", path)
				return nil, os.ErrNotExist
			}
		},
		fileOwnerUID: func(info os.FileInfo) (int, bool) {
			return info.(fakeFileInfo).uid, true
		},
		currentUID: func() int {
			return 501
		},
	})

	if !IsSessionPIDAlive(baseDir, "ctx-foreign", "daemon") {
		t.Fatal("foreign daemon should still be considered live")
	}
	if IsSessionPIDOwnedByCurrentUser(baseDir, "ctx-foreign", "daemon") {
		t.Fatal("foreign daemon should not be owned by current user")
	}
	contextID, sessionName, ok := FindCurrentUserDaemon(baseDir)
	if !ok {
		t.Fatal("FindCurrentUserDaemon() ok = false, want true")
	}
	if contextID != "ctx-current" || sessionName != "main" {
		t.Fatalf("FindCurrentUserDaemon() = (%q, %q), want (ctx-current, main)", contextID, sessionName)
	}
}

func writePIDFileForProbeTest(t *testing.T, baseDir, contextName, sessionName, pid string) string {
	t.Helper()
	dir := filepath.Join(baseDir, contextName, sessionName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", dir, err)
	}
	pidPath := filepath.Join(dir, "postman.pid")
	if err := os.WriteFile(pidPath, []byte(pid), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", pidPath, err)
	}
	return pidPath
}

func withSessionPIDProbe(t *testing.T, probe sessionPIDProbe) {
	t.Helper()
	orig := sessionPIDs
	sessionPIDs = probe.withDefaults()
	t.Cleanup(func() { sessionPIDs = orig })
}
