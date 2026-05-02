package session

import (
	"sort"

	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/tui"
)

type Key string

type Record struct {
	Key       Key
	Name      string
	NodeCount int
	Enabled   bool
}

type Registry struct {
	records []Record
}

func BuildRegistry(nodes map[string]discovery.NodeInfo, allSessions []string, isSessionEnabled func(string) bool) Registry {
	sessionNodeCount := make(map[string]int)
	for nodeName := range nodes {
		sessionName, _, hasSession := nodeaddr.Split(nodeName)
		if hasSession {
			sessionNodeCount[sessionName]++
		}
	}

	sessionSet := make(map[string]bool)
	for _, sessionName := range allSessions {
		sessionSet[sessionName] = true
	}
	for sessionName := range sessionNodeCount {
		sessionSet[sessionName] = true
	}

	records := make([]Record, 0, len(sessionSet))
	for _, sessionName := range allSessions {
		if !sessionSet[sessionName] {
			continue
		}
		delete(sessionSet, sessionName)
		records = append(records, buildRecord(sessionName, sessionNodeCount[sessionName], isSessionEnabled))
	}

	missingSessions := make([]string, 0, len(sessionSet))
	for sessionName := range sessionSet {
		missingSessions = append(missingSessions, sessionName)
	}
	sort.Strings(missingSessions)
	for _, sessionName := range missingSessions {
		records = append(records, buildRecord(sessionName, sessionNodeCount[sessionName], isSessionEnabled))
	}

	return Registry{records: records}
}

func buildRecord(sessionName string, nodeCount int, isSessionEnabled func(string) bool) Record {
	return Record{
		Key:       Key(sessionName),
		Name:      sessionName,
		NodeCount: nodeCount,
		Enabled:   isSessionEnabled(sessionName),
	}
}

func (r Registry) Records() []Record {
	records := make([]Record, len(r.records))
	copy(records, r.records)
	return records
}

func (r Registry) SessionInfos() []tui.SessionInfo {
	sessions := make([]tui.SessionInfo, 0, len(r.records))
	for _, record := range r.records {
		sessions = append(sessions, tui.SessionInfo{
			Name:      record.Name,
			NodeCount: record.NodeCount,
			Enabled:   record.Enabled,
		})
	}
	return sessions
}
