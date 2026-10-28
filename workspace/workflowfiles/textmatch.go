package workflowfiles

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxSearchQueryBytes bounds a search pattern.
const MaxSearchQueryBytes = 500

// LineMatcher compiles the shared "search inside files" options (the Files
// panel and the file search tools) into one line test. Patterns use Go's RE2
// engine, which runs in linear time, so no pattern can stall a search.
func LineMatcher(query string, matchCase, wholeWord, regex bool) (func(string) bool, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("type something to search for")
	}
	if len(query) > MaxSearchQueryBytes {
		return nil, fmt.Errorf("the search text is too long (at most %d characters)", MaxSearchQueryBytes)
	}
	if !regex && !wholeWord {
		if matchCase {
			return func(line string) bool { return strings.Contains(line, query) }, nil
		}
		lower := strings.ToLower(query)
		return func(line string) bool { return strings.Contains(strings.ToLower(line), lower) }, nil
	}
	pattern := query
	if !regex {
		pattern = regexp.QuoteMeta(query)
	}
	if wholeWord {
		pattern = `\b(?:` + pattern + `)\b`
	}
	if !matchCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("the search pattern is not valid: %v", err)
	}
	return re.MatchString, nil
}
