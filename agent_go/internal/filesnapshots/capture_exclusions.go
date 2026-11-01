package filesnapshots

import (
	"os"
	"strconv"
	"strings"
)

// Classify this path from saved size/type evidence, never another file's
// counters or today's filesystem. Unknown/read and budget failures still warn.
func sizeOnlyOmission(record Record, path string) bool {
	known := 0
	for _, state := range []string{record.BeforeSkippedState[path], record.AfterSkippedState[path]} {
		if state == "" {
			continue
		}
		parts := strings.SplitN(state, ":", 3)
		if len(parts) != 3 {
			return false
		}
		size, err := strconv.ParseInt(parts[0], 10, 64)
		mode, modeErr := strconv.ParseUint(parts[2], 10, 32)
		if err != nil || modeErr != nil || size <= MaxFileBytes || !os.FileMode(mode).IsRegular() {
			return false
		}
		known++
	}
	if known == 2 {
		return true
	}
	if known != 1 {
		return false
	}
	// Begin and Finish append one entry per skipped boundary. A second entry
	// with no fingerprint is an unknown failure, not a successful/absent side.
	omissions := 0
	for _, skipped := range record.Skipped {
		if skipped == path {
			omissions++
		}
	}
	return omissions == 1
}
