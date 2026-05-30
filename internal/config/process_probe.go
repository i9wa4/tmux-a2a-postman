package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
)

type signalProcess interface {
	Signal(os.Signal) error
}

type sessionPIDProbe struct {
	readFile     func(string) ([]byte, error)
	findProcess  func(int) (signalProcess, error)
	stat         func(string) (os.FileInfo, error)
	fileOwnerUID func(os.FileInfo) (int, bool)
	currentUID   func() int
}

var sessionPIDs = defaultSessionPIDProbe()

func defaultSessionPIDProbe() sessionPIDProbe {
	return sessionPIDProbe{
		readFile: os.ReadFile,
		findProcess: func(pid int) (signalProcess, error) {
			return os.FindProcess(pid)
		},
		stat:         os.Stat,
		fileOwnerUID: unixFileOwnerUID,
		currentUID:   os.Getuid,
	}
}

func (p sessionPIDProbe) withDefaults() sessionPIDProbe {
	defaults := defaultSessionPIDProbe()
	if p.readFile == nil {
		p.readFile = defaults.readFile
	}
	if p.findProcess == nil {
		p.findProcess = defaults.findProcess
	}
	if p.stat == nil {
		p.stat = defaults.stat
	}
	if p.fileOwnerUID == nil {
		p.fileOwnerUID = defaults.fileOwnerUID
	}
	if p.currentUID == nil {
		p.currentUID = defaults.currentUID
	}
	return p
}

func (p sessionPIDProbe) isPIDAlive(pidPath string) bool {
	sigErr, ok := p.signalPID(pidPath)
	if !ok {
		return false
	}
	return sigErr == nil || errors.Is(sigErr, syscall.EPERM)
}

func (p sessionPIDProbe) isPIDOwnedByCurrentUser(pidPath string) bool {
	p = p.withDefaults()
	if !p.pidFileOwnedByCurrentUser(pidPath) {
		return false
	}
	sigErr, ok := p.signalPID(pidPath)
	return ok && sigErr == nil
}

func (p sessionPIDProbe) signalPID(pidPath string) (error, bool) {
	p = p.withDefaults()
	pid, ok := p.readSessionPID(pidPath)
	if !ok {
		return nil, false
	}
	proc, err := p.findProcess(pid)
	if err != nil {
		return nil, false
	}
	return proc.Signal(syscall.Signal(0)), true
}

func (p sessionPIDProbe) readSessionPID(pidPath string) (int, bool) {
	p = p.withDefaults()
	data, err := p.readFile(pidPath)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

func (p sessionPIDProbe) pidFileOwnedByCurrentUser(pidPath string) bool {
	p = p.withDefaults()
	info, err := p.stat(pidPath)
	if err != nil {
		return false
	}
	uid, ok := p.fileOwnerUID(info)
	return ok && uid == p.currentUserID()
}

func (p sessionPIDProbe) currentUserID() int {
	p = p.withDefaults()
	return p.currentUID()
}

func unixFileOwnerUID(info os.FileInfo) (int, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(stat.Uid), true
}
