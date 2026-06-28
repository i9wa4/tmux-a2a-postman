package daemon

import "fmt"

type processRSSSnapshot struct {
	Supported bool
	Available bool
	Bytes     uint64
}

var currentProcessRSSSnapshot = readCurrentProcessRSSSnapshot

func processRSSLogFields(snapshot processRSSSnapshot) string {
	if !snapshot.Supported {
		return "rss_supported=false rss_available=false"
	}
	if !snapshot.Available {
		return "rss_supported=true rss_available=false"
	}
	return fmt.Sprintf("rss_supported=true rss_available=true rss_bytes=%d", snapshot.Bytes)
}
