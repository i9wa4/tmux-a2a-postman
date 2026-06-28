//go:build linux

package daemon

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func readCurrentProcessRSSSnapshot() processRSSSnapshot {
	bytes, err := readStatmRSSBytes("/proc/self/statm", os.Getpagesize())
	if err != nil {
		return processRSSSnapshot{Supported: true}
	}
	return processRSSSnapshot{Supported: true, Available: true, Bytes: bytes}
}

func readStatmRSSBytes(path string, pageSize int) (uint64, error) {
	if pageSize <= 0 {
		return 0, fmt.Errorf("invalid page size %d", pageSize)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(content))
	if len(fields) < 2 {
		return 0, fmt.Errorf("statm missing resident page field")
	}
	residentPages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}
	pageSizeBytes := uint64(pageSize)
	if residentPages > ^uint64(0)/pageSizeBytes {
		return 0, fmt.Errorf("resident byte count overflow")
	}
	return residentPages * pageSizeBytes, nil
}
