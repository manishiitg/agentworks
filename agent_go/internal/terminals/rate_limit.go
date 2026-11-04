package terminals

import (
	"regexp"
	"strings"
)

// rateLimitPatterns matches messages each coding-agent CLI prints into
// its pane when the underlying provider blocks further work due to a
// rate / quota / billing limit. Patterns are intentionally broad — a
// false positive (e.g., the CLI prints a help line mentioning "rate
// limit") is recoverable (user sees a rate_limited badge and clicks
// to expand), while a false negative leaves the pane silently stuck
// with "running" status forever.
//
// Ordering matters: more specific provider-prefixed patterns come
// first so we attribute correctly. Generic fallback patterns at the
// end catch any provider that prints a recognizable phrase. Callers that take
// destructive action must add recency and repeated-observation checks; this
// matcher also powers a non-destructive UI status badge.
var rateLimitPatterns = []*regexp.Regexp{
	// Claude Code: prints messages like "5-hour limit reached", "Your
	// usage limit will reset at <time>", "You have run out of credits",
	// "You've hit your session limit · resets <time>" followed by a
	// "/usage-credits to finish what you're working on." line. NOTE: the
	// "session" wording was missing from the (message|usage|rate) group below
	// and silently slipped past detection, leaving runs wedged "running" -
	// keep "session" here and the dedicated /usage-credits line.
	regexp.MustCompile(`(?i)(5[- ]hour|5h)\s+limit\s+(reached|hit)`),
	regexp.MustCompile(`(?i)(session|usage)\s+limit\s+(reached|hit|will\s+reset)`),
	regexp.MustCompile(`(?i)you(?:'?ve|\s+have)?\s+(reached|hit)\s+your\s+(message|session|usage|rate)\s+limit`),
	regexp.MustCompile(`(?i)you(?:'?ve|\s+have)?\s+run\s+out\s+of\s+credits`),
	regexp.MustCompile(`(?i)/usage-credits\s+to\s+finish`),

	// Codex CLI / OpenAI: prints messages with HTTP 429 hints, "rate
	// limit exceeded", "quota exceeded", "tokens per minute".
	regexp.MustCompile(`(?i)rate[- ]limit(ed|\s+(exceeded|hit|reached))`),
	regexp.MustCompile(`(?i)quota\s+(exceeded|exhausted)`),
	regexp.MustCompile(`(?i)429\s+too\s+many\s+requests`),
	regexp.MustCompile(`(?i)tokens?\s+per\s+minute\b.*\b(exceeded|limit)`),

	// Some providers print "Resource has been exhausted", "RESOURCE_EXHAUSTED",
	// "Quota exceeded for quota metric".
	regexp.MustCompile(`(?i)RESOURCE_EXHAUSTED`),
	regexp.MustCompile(`(?i)resource\s+has\s+been\s+exhausted`),
	regexp.MustCompile(`(?i)quota\s+exceeded\s+for\s+quota\s+metric`),

	// Cursor CLI: prints "you have reached your usage limit", "monthly
	// limit reached".
	regexp.MustCompile(`(?i)monthly\s+(usage\s+)?limit\s+(reached|hit)`),
	regexp.MustCompile(`(?i)you'?ve?\s+reached\s+your\s+usage\s+limit`),

	// Generic Anthropic / API responses surfaced by structured CLIs.
	regexp.MustCompile(`(?i)anthropic[._]rate[._]limit`),
	regexp.MustCompile(`(?i)overloaded_error`),
}

// rateLimitTailBytes is how much of the end of the output detectRateLimit reads.
const rateLimitTailBytes = 8 << 10

// rateLimitHint is a cheap check that one of the patterns could match: each contains one of these words.
func rateLimitHint(content string) bool {
	lower := strings.ToLower(content)
	for _, word := range []string{"limit", "credits", "quota", "429", "exhausted", "exceeded", "overloaded"} {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

// DetectRateLimit is the exported form of detectRateLimit for callers outside
// this package, such as the coding-CLI watchdog that force-stops panes parked
// on a provider limit wall.
func DetectRateLimit(content string) bool {
	return detectRateLimit(content)
}

// detectRateLimit returns true when the rendered pane content contains
// any rate-limit phrase. The caller has already stripped ANSI escapes
// before passing content here.
func detectRateLimit(content string) bool {
	// A limit message is the CLI's latest output, so only the end of the text is read. Status is derived again on
	// every streamed chunk, and matching every pattern against a whole long transcript each time took ~74% of a
	// server's CPU (4 Nov: chat loads timed out behind it).
	if len(content) > rateLimitTailBytes {
		content = content[len(content)-rateLimitTailBytes:]
	}
	if strings.TrimSpace(content) == "" || !rateLimitHint(content) {
		return false
	}
	for _, pattern := range rateLimitPatterns {
		if pattern.MatchString(content) {
			return true
		}
	}
	return false
}
