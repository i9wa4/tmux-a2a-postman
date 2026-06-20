package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const postmanProcessName = "tmux-a2a-postman"

type signalProcess interface {
	Signal(os.Signal) error
}

type sessionPIDRecord struct {
	PID              int    `json:"pid"`
	ProcessName      string `json:"process_name,omitempty"`
	ProcessStartedAt string `json:"process_started_at,omitempty"`
}

type sessionPIDProbe struct {
	readFile         func(string) ([]byte, error)
	writeFile        func(string, []byte, os.FileMode) error
	findProcess      func(int) (signalProcess, error)
	processCommand   func(int) (string, error)
	processStartedAt func(int) (string, error)
	stat             func(string) (os.FileInfo, error)
	fileOwnerUID     func(os.FileInfo) (int, bool)
	currentUID       func() int
}

var sessionPIDs = defaultSessionPIDProbe()

func defaultSessionPIDProbe() sessionPIDProbe {
	return sessionPIDProbe{
		readFile:  os.ReadFile,
		writeFile: os.WriteFile,
		findProcess: func(pid int) (signalProcess, error) {
			return os.FindProcess(pid)
		},
		processCommand:   defaultProcessCommand,
		processStartedAt: defaultProcessStartedAt,
		stat:             os.Stat,
		fileOwnerUID:     unixFileOwnerUID,
		currentUID:       os.Getuid,
	}
}

func (p sessionPIDProbe) withDefaults() sessionPIDProbe {
	defaults := defaultSessionPIDProbe()
	if p.readFile == nil {
		p.readFile = defaults.readFile
	}
	if p.writeFile == nil {
		p.writeFile = defaults.writeFile
	}
	if p.findProcess == nil {
		p.findProcess = defaults.findProcess
	}
	if p.processCommand == nil {
		p.processCommand = defaults.processCommand
	}
	if p.processStartedAt == nil {
		p.processStartedAt = defaults.processStartedAt
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

func WriteSessionPIDFile(pidPath string, pid int) error {
	return sessionPIDs.writeSessionPIDFile(pidPath, pid)
}

func ReadSessionPIDFile(pidPath string) (int, error) {
	record, ok := sessionPIDs.readSessionPIDRecord(pidPath)
	if !ok {
		return 0, fmt.Errorf("invalid pid in %s", pidPath)
	}
	return record.PID, nil
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
	record, ok := p.readSessionPIDRecord(pidPath)
	if !ok {
		return nil, false
	}
	if !p.matchesSessionPIDRecord(record) {
		return nil, false
	}
	proc, err := p.findProcess(record.PID)
	if err != nil {
		return nil, false
	}
	return proc.Signal(syscall.Signal(0)), true
}

func (p sessionPIDProbe) readSessionPIDRecord(pidPath string) (sessionPIDRecord, bool) {
	p = p.withDefaults()
	data, err := p.readFile(pidPath)
	if err != nil {
		return sessionPIDRecord{}, false
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return sessionPIDRecord{}, false
	}
	if strings.HasPrefix(trimmed, "{") {
		var record sessionPIDRecord
		if err := json.Unmarshal([]byte(trimmed), &record); err != nil {
			return sessionPIDRecord{}, false
		}
		if record.PID <= 0 {
			return sessionPIDRecord{}, false
		}
		return record, true
	}
	pid, err := strconv.Atoi(trimmed)
	if err != nil || pid <= 0 {
		return sessionPIDRecord{}, false
	}
	return sessionPIDRecord{PID: pid}, true
}

func (p sessionPIDProbe) writeSessionPIDFile(pidPath string, pid int) error {
	p = p.withDefaults()
	record := sessionPIDRecord{
		PID: pid,
	}
	record.ProcessName = p.processName(pid)
	if record.ProcessName == "" {
		record.ProcessName = postmanProcessName
	}
	if startedAt, err := p.processStartedAt(pid); err == nil {
		record.ProcessStartedAt = strings.TrimSpace(startedAt)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return p.writeFile(pidPath, data, 0o600)
}

func (p sessionPIDProbe) processName(pid int) string {
	command, err := p.processCommand(pid)
	if err != nil {
		return ""
	}
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return ""
	}
	return filepath.Base(fields[0])
}

func (p sessionPIDProbe) matchesSessionPIDRecord(record sessionPIDRecord) bool {
	if record.ProcessStartedAt != "" {
		startedAt, err := p.processStartedAt(record.PID)
		if err != nil || strings.TrimSpace(startedAt) != record.ProcessStartedAt {
			return false
		}
		return true
	}
	expectedName := record.ProcessName
	if expectedName == "" {
		expectedName = postmanProcessName
	}
	if expectedName == "" {
		return true
	}
	command, err := p.processCommand(record.PID)
	if err != nil {
		return false
	}
	return processCommandMatches(command, expectedName)
}

func processCommandMatches(command, expectedName string) bool {
	command = strings.TrimSpace(command)
	expectedName = strings.TrimSpace(expectedName)
	if command == "" || expectedName == "" {
		return false
	}
	fields := strings.Fields(command)
	if len(fields) > 0 && filepath.Base(fields[0]) == expectedName {
		return true
	}
	return strings.Contains(command, expectedName)
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

func defaultProcessCommand(pid int) (string, error) {
	if command, err := procProcessCommand(pid); err == nil {
		return command, nil
	}
	if command, err := psProcessCommand(pid); err == nil {
		return command, nil
	}
	return platformProcessCommand(pid)
}

func defaultProcessStartedAt(pid int) (string, error) {
	if startedAt, err := procProcessStartedAt(pid); err == nil {
		return startedAt, nil
	}
	if startedAt, err := psProcessStartedAt(pid); err == nil {
		return startedAt, nil
	}
	return platformProcessStartedAt(pid)
}

func procProcessCommand(pid int) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return "", err
	}
	command := strings.TrimSpace(strings.ReplaceAll(string(data), "\x00", " "))
	if command == "" {
		return "", fmt.Errorf("empty /proc cmdline for pid %d", pid)
	}
	return command, nil
}

func procProcessStartedAt(pid int) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	stat := string(data)
	closeParen := strings.LastIndex(stat, ")")
	if closeParen < 0 || closeParen+2 >= len(stat) {
		return "", fmt.Errorf("invalid /proc stat for pid %d", pid)
	}
	fields := strings.Fields(stat[closeParen+2:])
	if len(fields) < 20 {
		return "", fmt.Errorf("short /proc stat for pid %d", pid)
	}
	return fields[19], nil
}

func psProcessCommand(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func psProcessStartedAt(pid int) (string, error) {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "lstart=").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
