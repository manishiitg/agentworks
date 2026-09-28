package terminals

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	ansiEscapePattern     = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
	callingToolPattern    = regexp.MustCompile(`(?i)\b(?:Calling|Called)\s+([a-zA-Z0-9_.-]+)(?:\s+(\d+)\s+times?)?`)
	geminiToolPattern     = regexp.MustCompile(`^[\s│╎╏]*[✓•]\s+([a-zA-Z0-9_.-]+)\s*\(`)
	codexToolPattern      = regexp.MustCompile(`^[\s│╎╏]*[•-]?\s*Called\s+([a-zA-Z0-9_.-]+)\b`)
	longSeparatorPattern  = regexp.MustCompile(`^[\s─━═-]{16,}$`)
	boxDrawingOnlyPattern = regexp.MustCompile(`^[\s│┃║┌┐└┘├┤┬┴┼╭╮╰╯─━═╎╏]+$`)
	whitespacePattern     = regexp.MustCompile(`\s+`)
	leadingMarkerPattern  = regexp.MustCompile(`^[\s│┃║╎╏>›❯]+`)
	trailingStatusPattern = regexp.MustCompile(`\s*(?:\?|for shortcuts|Shift\+Tab.*|esc to interrupt.*)$`)
	// cursorComposerStatusLine matches Cursor's bottom status line precisely,
	// e.g. "Composer 2.5 · 15.8%" or "Composer 2 Fast · 5.5%". Anchored so
	// arbitrary prose starting with the word "Composer" is not stripped.
	cursorComposerStatusLine = regexp.MustCompile(`(?i)^composer\s+\S+(?:\s+\S+)?\s+·\s+\d+(?:\.\d+)?%`)
)

const (
	maxAssistantPreviewLines = 12
	maxAssistantPreviewChars = 1200
)

// DeriveStatus extracts a compact progress summary from a terminal screen.
// It intentionally returns no assistant preview when the screen is ambiguous.
func DeriveStatus(content string, metadata map[string]interface{}) Status {
	provider := providerLabel(content, metadata)
	preview := assistantPreview(content, provider)
	toolName, toolCount := latestToolSummary(content)

	toolSummary := ""
	if toolName != "" {
		if toolCount > 1 {
			toolSummary = toolName + " x" + strconv.Itoa(toolCount)
		} else {
			toolSummary = toolName
		}
	}

	statusText := preview
	if statusText == "" {
		if provider != "" {
			statusText = provider + " is working"
		} else {
			statusText = "Agent is working"
		}
	}

	return Status{
		ProviderLabel:    provider,
		StatusText:       statusText,
		AssistantPreview: preview,
		ToolSummary:      toolSummary,
		ToolName:         toolName,
		ToolCount:        toolCount,
		InputTokens:      intValue(metadata["input_tokens"]),
		OutputTokens:     intValue(metadata["output_tokens"]),
		CostUSD:          floatValue(metadata["cost_usd_estimated"]),
		DurationMs:       int64Value(metadata["duration_ms"]),
		RateLimited:      detectRateLimit(content),
	}
}

func providerLabel(content string, metadata map[string]interface{}) string {
	if provider := firstNonEmpty(stringValue(metadata, "provider"), stringValue(metadata, "provider_id")); provider != "" {
		switch strings.ToLower(provider) {
		case "claude-code", "claudecode":
			return "Claude Code"
		case "codex-cli", "codexcli":
			return "Codex CLI"
		case "cursor-cli", "cursorcli":
			return "Cursor CLI"
		case "pi-cli", "picli":
			return "Pi CLI"
		case "muse-cli", "musecli":
			return "Muse"
		case "agy-cli", "agycli":
			return "Antigravity CLI"
		default:
			return provider
		}
	}

	lower := strings.ToLower(content)
	switch {
	case strings.Contains(lower, "claude code"):
		return "Claude Code"
	case strings.Contains(lower, "openai codex") || strings.Contains(lower, ">_ openai codex"):
		return "Codex CLI"
	case isCursorPaneHeader(lower):
		return "Cursor CLI"
	case strings.Contains(lower, "muse code"):
		return "Muse"
	case strings.Contains(lower, "antigravity cli"):
		return "Antigravity CLI"
	default:
		return ""
	}
}

// isCursorPaneHeader looks for the combination of Cursor's app banner AND its
// version stamp on the same pane — both must be present to claim ownership.
// Either string alone (e.g. a Claude response mentioning "open in cursor agent")
// is not enough to misclassify the pane.
func isCursorPaneHeader(lower string) bool {
	return strings.Contains(lower, "cursor agent") &&
		(strings.Contains(lower, "v20") || strings.Contains(lower, "composer "))
}

func assistantPreview(content, provider string) string {
	switch provider {
	case "Claude Code":
		return markerPreview(content, "⏺")
	case "Cursor CLI":
		// Older Cursor builds prefix each assistant turn with a literal
		// "Assistant:" header. Newer builds (CLI v2026-05-20 +) drop the label
		// and emit bare prose. Try the marker first, then fall back to a
		// section-aware scan that finds the last response block above the
		// "→ Add a follow-up" boundary.
		if preview := markerPreview(content, "Assistant:"); preview != "" {
			return preview
		}
		return cursorMarkerlessPreview(content)
	default:
		return ""
	}
}

// cursorMarkerlessPreview extracts the last block of response prose from a
// Cursor pane that omits the "Assistant:" label (CLI v2026-05-20 +). Cursor
// separates the echoed user prompt from the assistant response with two or
// more blank lines, while paragraphs within the response are separated by
// only one blank line — so we walk backwards from the input-box boundary
// ("→ Add a follow-up") and stop at the first 2-blank gap.
func cursorMarkerlessPreview(content string) string {
	// While the pane is busy, the only candidate text between the spinner and
	// the input box is the echoed user prompt — surfacing that as the preview
	// would be misleading. Let DeriveStatus fall back to "Cursor CLI is working".
	if terminalContentLooksBusy(content) {
		return ""
	}
	// Preserve blank lines (cleanedLines drops them) — they are the only
	// signal that separates the echoed user prompt from the response block.
	stripped := ansiEscapePattern.ReplaceAllString(content, "")
	rawLines := strings.Split(stripped, "\n")
	lines := make([]string, len(rawLines))
	for i, raw := range rawLines {
		lines[i] = strings.TrimSpace(strings.Trim(strings.TrimSpace(raw), "│┃║╎╏"))
	}
	// Find the input-box boundary scanning from the bottom up.
	boundary := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "→ ") {
			boundary = i
			break
		}
	}
	if boundary < 0 {
		boundary = len(lines)
	}
	collected := make([]string, 0, maxAssistantPreviewLines)
	consecutiveBlanks := 0
	started := false
	for i := boundary - 1; i >= 0 && len(collected) < maxAssistantPreviewLines; i-- {
		line := lines[i]
		if line == "" {
			if !started {
				// Skip the visual gap between the response and the input box.
				continue
			}
			consecutiveBlanks++
			if consecutiveBlanks >= 2 {
				// Two blanks separate the echoed user prompt from the response —
				// stop here so the prompt is not included in the preview.
				break
			}
			continue
		}
		if isNoisyTerminalLine(line) {
			if started {
				// Hit chrome above the response — stop.
				break
			}
			continue
		}
		started = true
		consecutiveBlanks = 0
		collected = append(collected, line)
	}
	// Reverse so the order matches the on-screen reading order.
	for left, right := 0, len(collected)-1; left < right; left, right = left+1, right-1 {
		collected[left], collected[right] = collected[right], collected[left]
	}
	return cleanPreviewBlock(collected)
}

func markerPreview(content, marker string) string {
	lines := cleanedLines(content)
	best := ""
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		markerIndex := strings.Index(line, marker)
		if markerIndex < 0 {
			continue
		}
		candidateParts := []string{strings.TrimSpace(line[markerIndex+len(marker):])}
		for j := i + 1; j < len(lines) && len(candidateParts) < maxAssistantPreviewLines; j++ {
			next := strings.TrimSpace(lines[j])
			if strings.Contains(next, marker) || isNoisyTerminalLine(next) {
				break
			}
			if next != "" {
				candidateParts = append(candidateParts, next)
			}
		}
		candidate := cleanPreviewBlock(candidateParts)
		if candidate != "" {
			best = candidate
		}
	}
	return best
}

func latestToolSummary(content string) (string, int) {
	lines := cleanedLines(content)
	counts := map[string]int{}
	latestTool := ""
	latestCount := 0

	for _, line := range lines {
		tool, count := toolFromLine(line)
		if tool == "" {
			continue
		}
		if count <= 0 {
			counts[tool]++
			count = counts[tool]
		} else if count > counts[tool] {
			counts[tool] = count
		}
		latestTool = tool
		latestCount = count
	}

	return latestTool, latestCount
}

func toolFromLine(line string) (string, int) {
	line = strings.TrimSpace(line)
	for _, pattern := range []*regexp.Regexp{callingToolPattern, geminiToolPattern, codexToolPattern} {
		match := pattern.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		tool := strings.Trim(match[1], " .,:;")
		if isIgnoredToolName(tool) {
			continue
		}
		count := 0
		if len(match) > 2 && match[2] != "" {
			if parsed, err := strconv.Atoi(match[2]); err == nil {
				count = parsed
			}
		}
		return tool, count
	}
	return "", 0
}

func isIgnoredToolName(tool string) bool {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "", "working", "thinking", "vibing":
		return true
	default:
		return false
	}
}

func cleanedLines(content string) []string {
	rawLines := strings.Split(ansiEscapePattern.ReplaceAllString(content, ""), "\n")
	lines := make([]string, 0, len(rawLines))
	for _, raw := range rawLines {
		line := strings.TrimSpace(raw)
		line = strings.TrimSpace(strings.Trim(line, "│┃║╎╏"))
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines
}

func cleanPreviewBlock(lines []string) string {
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		cleanedLine := cleanPreviewLine(line)
		if cleanedLine != "" {
			cleaned = append(cleaned, cleanedLine)
		}
	}
	value := strings.TrimSpace(strings.Join(cleaned, "\n"))
	if value == "" || isNoisyTerminalLine(value) {
		return ""
	}
	if len(value) > maxAssistantPreviewChars {
		value = strings.TrimSpace(value[:maxAssistantPreviewChars]) + "..."
	}
	return value
}

func cleanPreviewLine(value string) string {
	value = strings.TrimSpace(value)
	value = leadingMarkerPattern.ReplaceAllString(value, "")
	value = trailingStatusPattern.ReplaceAllString(value, "")
	value = whitespacePattern.ReplaceAllString(value, " ")
	value = strings.TrimSpace(value)
	if value == "" || isNoisyTerminalLine(value) {
		return ""
	}
	return value
}

func isNoisyTerminalLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || boxDrawingOnlyPattern.MatchString(line) || longSeparatorPattern.MatchString(line) {
		return true
	}
	lower := strings.ToLower(line)
	noisyPrefixes := []string{
		"calling ",
		"called ",
		"working ",
		"press ",
		"shift+tab",
		"workspace ",
		"sandbox ",
		"model:",
		"directory:",
		"claude code v",
		"openai codex",
		"cursor agent v",
		"tips for getting started",
		"what's new",
		"authenticated with",
		"question id:",
		"raw operator question:",
	}
	for _, prefix := range noisyPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	noisyContains := []string{
		"ctrl+o",
		"ctrl+c to stop",
		"esc to interrupt",
		"for shortcuts",
		"tokens",
		"thought for",
		"thinking with",
		"still thinking",
		"type your message",
		"mcp server",
		"shift+tab to cycle",
		// Cursor's input-box placeholder. The literal phrase only appears in
		// Cursor's TUI; safe to treat as chrome rather than prose.
		"→ add a follow-up",
		"use /mcp to connect cursor",
	}
	for _, needle := range noisyContains {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	// Cursor's "Composer 2.5 · 15.8%" mode line — matched precisely so we
	// never strip arbitrary prose that happens to begin with "Composer".
	if cursorComposerStatusLine.MatchString(line) {
		return true
	}
	return false
}

func terminalStateFromContent(content string, active bool) string {
	if terminalContentLooksFatal(content) {
		return "failed"
	}
	if active {
		return "running"
	}
	if terminalContentLooksBusy(content) {
		return "running"
	}
	switch terminalLatestExplicitOutcome(content) {
	case "failed":
		return "failed"
	case "completed":
		return "completed"
	}
	return "completed"
}

func terminalContentLooksIdle(content string) bool {
	if terminalContentLooksBusy(content) {
		return false
	}
	// A pane showing a trust/auth/approval modal is not idle even though the
	// underlying CLI is "waiting": it requires explicit operator input. Treating
	// it as idle would let the terminal time-out into "completed" while the
	// agent is actually blocked on the modal.
	if terminalContentHasBlockingModal(content) {
		return false
	}
	lines := cleanedLines(content)
	if len(lines) == 0 {
		return false
	}
	start := 0
	if len(lines) > 40 {
		start = len(lines) - 40
	}
	provider := providerLabel(content, nil)
	hasCompletionContext := terminalHasExplicitCompletion(content) || terminalHasPromptCompletionFallback(content)
	for _, line := range lines[start:] {
		if isProviderIdlePromptLine(provider, line, hasCompletionContext) {
			return true
		}
	}
	return false
}

// terminalContentHasBlockingModal detects in-pane prompts that require operator
// input before the agent can continue: workspace-trust, web-search approval,
// auth/login, and the generic Cursor "[a] / [w] / [q]" key-binding menu.
func terminalContentHasBlockingModal(content string) bool {
	lower := strings.ToLower(content)
	if strings.Contains(lower, "workspace trust required") ||
		strings.Contains(lower, "do you trust the contents") {
		return true
	}
	if strings.Contains(lower, "approve this web search") ||
		strings.Contains(lower, "allow web search") {
		return true
	}
	if strings.Contains(lower, "[a] trust this workspace") ||
		strings.Contains(lower, "[w] trust this workspace") {
		return true
	}
	if strings.Contains(lower, "press enter to continue") &&
		strings.Contains(lower, "sign in") {
		return true
	}
	return false
}

func isProviderIdlePromptLine(provider, line string, hasExplicitCompletion bool) bool {
	trimmed := strings.TrimSpace(line)
	lower := strings.ToLower(trimmed)
	switch provider {
	case "Codex CLI":
		if hasExplicitCompletion && strings.HasPrefix(trimmed, "›") {
			return true
		}
		return strings.HasPrefix(trimmed, "› ") &&
			(strings.Contains(lower, "/skills") ||
				strings.Contains(lower, "type your message") ||
				strings.Contains(lower, "/model to change"))
	case "Claude Code":
		return trimmed == "❯" || strings.HasPrefix(trimmed, "❯ ")
	case "Cursor CLI":
		// Cursor's input box always renders "→ Add a follow-up" as a placeholder.
		// It is present in both busy and idle states, so this matcher relies on
		// terminalContentLooksBusy short-circuiting first (see terminalContentLooksIdle).
		// "ask (shift+tab to cycle)" and the "Composer 2.x · NN%" mode line are
		// equally reliable structural markers of the cursor TUI being settled.
		if strings.HasPrefix(trimmed, "→ ") && strings.Contains(lower, "add a follow-up") {
			return true
		}
		if strings.Contains(lower, "ask (shift+tab") {
			return true
		}
		if strings.HasPrefix(lower, "composer ") && strings.Contains(trimmed, "%") {
			return true
		}
		return false
	default:
		return false
	}
}

func terminalHasExplicitCompletion(content string) bool {
	for _, lower := range terminalTailLines(content, 40) {
		if terminalLineIsCompletion(lower) {
			return true
		}
	}
	return false
}

func terminalHasPromptCompletionFallback(content string) bool {
	for _, lower := range terminalTailLines(content, 40) {
		if terminalLineIsPromptCompletion(lower) {
			return true
		}
	}
	return false
}

func terminalLatestExplicitOutcome(content string) string {
	outcome := ""
	for _, lower := range terminalTailLines(content, 80) {
		if terminalLineIsFailure(lower) {
			outcome = "failed"
			continue
		}
		if terminalLineIsCompletion(lower) || terminalLineIsPromptCompletion(lower) {
			outcome = "completed"
		}
	}
	return outcome
}

func terminalLineIsCompletion(lower string) bool {
	return strings.Contains(lower, "completed successfully") ||
		isTerminalWorkedForLine(lower)
}

func terminalLineIsPromptCompletion(lower string) bool {
	return strings.Contains(lower, "status: completed") ||
		strings.Contains(lower, "status: complete")
}

func isTerminalWorkedForLine(lower string) bool {
	lower = strings.TrimSpace(lower)
	if !strings.Contains(lower, "worked for ") {
		return false
	}
	return strings.HasPrefix(lower, "─") ||
		strings.HasPrefix(lower, "━") ||
		strings.HasPrefix(lower, "-") ||
		strings.HasPrefix(lower, "worked for ")
}

func terminalLineIsFailure(lower string) bool {
	return strings.Contains(lower, "status: failed") ||
		strings.Contains(lower, "pre-validation failed") ||
		strings.Contains(lower, "llm generation error") ||
		strings.Contains(lower, "conversation error") ||
		strings.Contains(lower, "agent error:") ||
		strings.Contains(lower, " error details:")
}

func terminalTailLines(content string, maxLines int) []string {
	lines := cleanedLines(content)
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, strings.ToLower(line))
	}
	return out
}

func terminalContentLooksFatal(content string) bool {
	lower := strings.ToLower(strings.Join(cleanedLines(content), "\n"))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "debug console") &&
		(strings.Contains(lower, "unhandled promise rejection") ||
			strings.Contains(lower, "this is an unexpected error") ||
			strings.Contains(lower, "enametoolong")) {
		return true
	}
	if strings.Contains(lower, "enametoolong") && strings.Contains(lower, "lstat") {
		return true
	}
	return false
}

func terminalContentLooksBusy(content string) bool {
	lines := cleanedLines(content)
	if len(lines) == 0 {
		return false
	}
	start := 0
	if len(lines) > 80 {
		start = len(lines) - 80
	}
	for _, line := range lines[start:] {
		if isTerminalBusyLine(line) {
			return true
		}
	}
	return false
}

func isTerminalBusyLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	if strings.Contains(lower, "agent is still working") {
		return true
	}
	// Cursor uses "ctrl+c to stop" instead of "esc to interrupt", and emits
	// "Composing" as its primary active verb (often paired with a braille spinner).
	if strings.Contains(lower, "ctrl+c to stop") {
		return true
	}
	if strings.Contains(lower, "composing") &&
		(strings.Contains(lower, "tokens") ||
			strings.Contains(lower, "thinking") ||
			isCursorBrailleSpinnerLine(trimmed)) {
		return true
	}
	if strings.Contains(lower, "esc to cancel") || strings.Contains(lower, "esc to interrupt") {
		// Bare "esc to interrupt" always means the agent is active — no secondary
		// keyword required. The idle-prompt detector short-circuits on looksBusy,
		// so a false positive here only delays a "completed" transition.
		return true
	}
	if strings.HasPrefix(trimmed, "⊷") || strings.HasPrefix(trimmed, "⠇") {
		return strings.Contains(lower, "thinking") ||
			strings.Contains(lower, "processing") ||
			strings.Contains(lower, "running") ||
			strings.Contains(lower, "agent_browser") ||
			strings.Contains(lower, "execute_")
	}
	if strings.HasPrefix(trimmed, "✳") || strings.HasPrefix(trimmed, "✽") {
		return strings.Contains(lower, "tokens") ||
			strings.Contains(lower, "thought") ||
			strings.Contains(lower, "thinking")
	}
	return false
}

// isCursorBrailleSpinnerLine reports whether a line begins with one of the
// braille glyphs Cursor cycles through as its "Composing" spinner (e.g. ⠰⠰,
// ⠠⠦, ⠉⠉). The full unicode braille range is U+2800–U+28FF.
func isCursorBrailleSpinnerLine(trimmed string) bool {
	for _, r := range trimmed {
		if r == ' ' || r == '\t' {
			continue
		}
		return r >= '⠀' && r <= '⣿'
	}
	return false
}
