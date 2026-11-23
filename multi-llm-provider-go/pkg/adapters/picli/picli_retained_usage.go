package picli

import (
	"sync"
)

// The bounded Run sums the usage of the assistant messages in its window (piMarkUsageReported records each). A
// follow-up typed into the running terminal has the same per-message usage in the transcript and nobody reads it; this
// registry lets the retained-turn reader report each message once.
var piReportedUsage = struct {
	sync.Mutex
	keys map[string]map[string]bool
}{keys: map[string]map[string]bool{}}

func piMarkUsageReported(path, key string) bool {
	if path == "" || key == "" {
		return false
	}
	piReportedUsage.Lock()
	defer piReportedUsage.Unlock()
	set := piReportedUsage.keys[path]
	if set == nil {
		set = map[string]bool{}
		piReportedUsage.keys[path] = set
	}
	if set[key] {
		return false
	}
	set[key] = true
	return true
}

// piUsageKey identifies one assistant message: Pi's response id when it has one, else the record timestamp.
func piUsageKey(ev piTranscriptRecord) string {
	if ev.Message != nil && ev.Message.ResponseID != "" {
		return ev.Message.ResponseID
	}
	return "ts:" + ev.Timestamp
}
