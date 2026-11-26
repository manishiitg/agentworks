package cursorcli

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
)

// PLAT-101 for Cursor. When a Cursor plan is spent, the TUI prints the notice
// as its own lines and returns to the prompt with no assistant output:
//
//	Error: Weekly usage limit reached. It resets in 6 days.
//	Buy credits to keep going now, or upgrade your plan for more weekly usage.
//
// Without this, the turn ended as "returned to the prompt without visible
// assistant output" and whatever partial text preceded it reached the caller
// as if it were the answer (server A, 2026-09-29: a Crew's reply to another Crew's
// ask). Quota exhaustion must surface as KindQuotaExhausted so the run waits
// for capacity instead of failing or passing on a half answer.
//
// A statement only counts at the START of a line, after an optional "Error:",
// so an agent writing about someone else's usage limit is not misread.
var cursorUsageLimitLine = regexp.MustCompile(
	`(?i)^(?:error:\s*)?(?:(?:weekly|monthly|daily|hourly)\s+)?usage\s+limit\s+(?:reached|exceeded)\b|^(?:error:\s*)?you(?:'ve|’ve|\s+have)\s+(?:hit|reached)\s+your\s+(?:[a-z0-9-]+\s+){0,3}limit\b`,
)

// cursorResetInPattern reads the relative reset Cursor states: "resets in 6
// days", "resets in 3 hours", "resets in 45 minutes".
var cursorResetInPattern = regexp.MustCompile(`(?i)resets?\s+in\s+(\d{1,3})\s*(day|hour|hr|minute|min)s?\b`)

const cursorUsageLimitLineMarkers = " \t⎿●⏺│•*·✻⚠!-"

// IsCursorUsageLimitText reports whether pane text states that the Cursor
// plan's usage limit has been reached.
func IsCursorUsageLimitText(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimLeft(line, cursorUsageLimitLineMarkers)
		if line != "" && cursorUsageLimitLine.MatchString(line) {
			return true
		}
	}
	return false
}

// CursorUsageLimitResetAt returns when the limit reopens, from the relative
// time Cursor states, or the zero time when none is stated.
func CursorUsageLimitResetAt(text string, now time.Time) time.Time {
	match := cursorResetInPattern.FindStringSubmatch(text)
	if match == nil {
		return time.Time{}
	}
	n, err := strconv.Atoi(match[1])
	if err != nil || n <= 0 {
		return time.Time{}
	}
	unit := time.Minute
	switch strings.ToLower(match[2]) {
	case "day":
		unit = 24 * time.Hour
	case "hour", "hr":
		unit = time.Hour
	}
	return now.Add(time.Duration(n) * unit).UTC()
}

// NewCursorUsageLimitError is the typed failure for a spent Cursor plan.
func NewCursorUsageLimitError(model, paneText string, now time.Time) error {
	return &llmerrors.Error{
		Kind:     llmerrors.KindQuotaExhausted,
		Provider: "cursor-cli",
		Model:    model,
		RetryAt:  CursorUsageLimitResetAt(paneText, now),
		Err:      errors.New("cursor usage limit reached"),
	}
}

// agentworks#3135: the SDK path reports a spent plan as a typed failure, not
// pane text: a RunError/CursorSdkError code such as PRO_USER_USAGE_LIMIT, or a
// message like "You've hit your usage limit". A plain rate limit (RateLimitError,
// HTTP 429, RATE_LIMITED, resource_exhausted) is a short wait, not a spent plan.
var (
	cursorSDKUsageLimitCode = regexp.MustCompile(`(?i)^\w*_usage_limit(?:_exceeded)?$`)
	cursorSDKRateLimitCode  = regexp.MustCompile(`(?i)^(?:resource_exhausted|rate_limited(?:_changeable)?|\w*_rate_limit(?:_exceeded)?)$`)
)

// cursorSDKFailure classifies a failed SDK turn from the bridge's typed fields.
// nil means no known class: the caller keeps its generic error.
func cursorSDKFailure(message, code, name string, status int, model string, now time.Time) error {
	if m := cursorModelRefusal.FindStringSubmatch(message); m != nil {
		// The account's model list (on a company account, the models its administrator allows) has no such
		// model: say so plainly, with what is allowed.
		return &llmerrors.Error{Kind: llmerrors.KindModelNotFound, Provider: "cursor-cli", Model: m[1],
			Err: fmt.Errorf("%s is not allowed on this account; allowed: %s. Pick another model.", m[1], strings.TrimSpace(m[2]))}
	}
	if cursorSDKUsageLimitCode.MatchString(code) || IsCursorUsageLimitText(message) {
		return NewCursorUsageLimitError(model, message, now)
	}
	if name == "RateLimitError" || status == 429 || cursorSDKRateLimitCode.MatchString(code) {
		return &llmerrors.Error{Kind: llmerrors.KindRateLimit, Provider: "cursor-cli", Model: model, Status: status,
			Err: fmt.Errorf("Cursor is busy for this account right now: %s", message)}
	}
	return nil
}
