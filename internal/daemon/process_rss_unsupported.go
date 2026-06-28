//go:build !linux

package daemon

func readCurrentProcessRSSSnapshot() processRSSSnapshot {
	return processRSSSnapshot{}
}
