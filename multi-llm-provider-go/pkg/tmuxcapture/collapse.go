package tmuxcapture

import (
	"strings"
	"unicode/utf8"
)

const maxConsecutiveBlankLines = 2

var spinnerStatusWords = []string{
	"loading", "working", "generating", "thinking", "analyzing", "exploring",
	"reviewing", "confirming", "refining", "investigating", "searching",
	"reading", "writing", "calling", "running", "navigating", "examining",
	"identifying", "saving", "extracting", "discovering", "processing",
	"waiting", "fetching", "building", "planning", "composing", "retrieving",
	"downloading", "uploading", "connecting", "preparing", "finalizing",
}

// CollapseBlankRuns preserves readable TUI spacing while removing repeated
// blank and flattened spinner frames from tmux scrollback.
func CollapseBlankRuns(content string) string {
	if content == "" {
		return content
	}
	lines := strings.Split(content, "\n")
	lines = pruneSpinnerWordFragments(lines)
	out := make([]string, 0, len(lines))
	blankRun := 0
	for index, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(trimmed) == "" {
			blankRun++
			if blankRun <= maxConsecutiveBlankLines {
				out = append(out, "")
			}
			continue
		}
		blankRun = 0
		if isSpinnerLine(trimmed) {
			next := index + 1
			for next < len(lines) && strings.TrimSpace(lines[next]) == "" {
				next++
			}
			if next < len(lines) && isSpinnerLine(strings.TrimRight(lines[next], " \t\r")) {
				continue
			}
		}
		out = append(out, trimmed)
	}
	return strings.Join(out, "\n")
}

func isSpinnerLine(line string) bool {
	r, _ := utf8.DecodeRuneInString(line)
	return r >= 0x2800 && r <= 0x28FF
}

func spinnerFragmentKind(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed != "" {
		runes := []rune(trimmed)
		if runes[0] >= 0x2800 && runes[0] <= 0x28FF {
			trimmed = strings.TrimSpace(string(runes[1:]))
		}
	}
	if trimmed == "" {
		return ""
	}
	core := strings.Trim(trimmed, ". ")
	if core == "" {
		return "weak"
	}
	if len(core) > 14 {
		return ""
	}
	lower := strings.ToLower(core)
	for _, r := range lower {
		if r < 'a' || r > 'z' {
			return ""
		}
	}
	matched := false
	for _, word := range spinnerStatusWords {
		if strings.Contains(word, lower) {
			matched = true
			break
		}
	}
	if !matched {
		return ""
	}
	if len(core) == 1 {
		return "weak"
	}
	return "strong"
}

func pruneSpinnerWordFragments(lines []string) []string {
	kinds := make([]string, len(lines))
	for index, line := range lines {
		kinds[index] = spinnerFragmentKind(line)
	}
	drop := make([]bool, len(lines))
	for index := 0; index < len(lines); {
		if kinds[index] == "" {
			index++
			continue
		}
		end := index
		strongCount := 0
		lastFragment := index
		for end < len(lines) {
			switch {
			case kinds[end] != "":
				if kinds[end] == "strong" {
					strongCount++
				}
				lastFragment = end
				end++
			case strings.TrimSpace(lines[end]) == "":
				end++
			default:
				goto classify
			}
		}
	classify:
		if strongCount >= 2 {
			for candidate := index; candidate <= lastFragment; candidate++ {
				if kinds[candidate] != "" {
					drop[candidate] = true
				}
			}
		}
		index = end
	}
	out := make([]string, 0, len(lines))
	for index, line := range lines {
		if !drop[index] {
			out = append(out, line)
		}
	}
	return out
}
