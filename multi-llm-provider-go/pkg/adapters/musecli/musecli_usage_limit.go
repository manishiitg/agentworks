package musecli

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

var museUsageResetRe = regexp.MustCompile(`(?i)wait for usage to reset at\s+([a-z]{3}\s+\d{1,2}\s+at\s+\d{1,2}:\d{2}\s+(?:am|pm))`)

// museUsageLimitError turns Muse's subscription-wall response into a typed,
// non-retryable quota error. Muse can emit the same text as a failed JSON
// terminal event, stderr, or an otherwise-successful terminal result, so all
// transports feed their available output through this one detector.
func museUsageLimitError(model string, now time.Time, values ...string) error {
	detail := ""
	for _, value := range values {
		candidate := strings.TrimSpace(value)
		lower := strings.ToLower(candidate)
		// An explanation or a link to billing is not a provider failure.
		if strings.HasPrefix(lower, "usage limit reached") {
			// Prefer the richest carrier. A terse failed reason can accompany a
			// terminal payload that also contains the reset time and upgrade URL.
			if detail == "" || len(candidate) > len(detail) {
				detail = candidate
			}
		}
	}
	if detail == "" {
		return nil
	}

	return &llmerrors.Error{
		Kind:     llmerrors.KindQuotaExhausted,
		Provider: "muse-cli",
		Model:    model,
		RetryAt:  museUsageResetAt(detail, now),
		Err:      errors.New(detail),
	}
}

// Muse currently prints a local wall-clock reset without a year or timezone
// (for example "Sep 14 at 5:30 AM"). Interpret it in the host's local zone,
// which is the same zone Muse used to render it, and never return a past time.
func museUsageResetAt(message string, now time.Time) time.Time {
	match := museUsageResetRe.FindStringSubmatch(message)
	if len(match) != 2 {
		return time.Time{}
	}
	parsed, err := time.ParseInLocation("Jan 2 at 3:04 PM 2006", match[1]+" "+now.Format("2006"), now.Location())
	if err != nil {
		return time.Time{}
	}
	if !parsed.After(now) {
		// Only infer a year rollover at the actual December/January boundary.
		// A stale reset message in September must not block this model for a year.
		if now.Month() == time.December && parsed.Month() == time.January {
			parsed = parsed.AddDate(1, 0, 0)
		} else {
			return time.Time{}
		}
	}
	return parsed
}
