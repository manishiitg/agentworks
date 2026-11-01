package filesnapshots

import (
	"strconv"
	"strings"
)

// The per-file size limit is an intentional capture policy, not a failed
// comparison. Use saved evidence, never today's file or its name/extension.
// Unknown/read failures and within-limit budget omissions still need notice.
func sizeOnlyOmission(record Record, path string) bool {
	known := false
	for _, side := range []struct {
		state string
		stats CaptureStats
	}{{record.BeforeSkippedState[path], record.BeforeCapture}, {record.AfterSkippedState[path], record.AfterCapture}} {
		if side.state == "" {
			// Non-regular paths have no fingerprint and are counted as size
			// skips too. An unknown side must not hide that actual failure.
			if side.stats.ReadSkipped > 0 || side.stats.SizeSkipped > 0 {
				return false
			}
			continue
		}
		size, _, ok := strings.Cut(side.state, ":")
		n, err := strconv.ParseInt(size, 10, 64)
		if !ok || err != nil || n <= MaxFileBytes {
			return false
		}
		known = true
	}
	return known
}
