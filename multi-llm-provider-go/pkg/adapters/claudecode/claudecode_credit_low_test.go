package claudecode

import (
	"testing"
)

func TestIsClaudeCreditLowText(t *testing.T) {
	for name, text := range map[string]string{
		"plain line":       "Credit balance is too low",
		"api error":        "API Error: Credit balance is too low",
		"drawn with glyph": "  ⎿  Credit balance is too low",
		"after output":     "working...\nCredit balance is too low\n",
		"lower case":       "credit balance is too low",
	} {
		if !IsClaudeCreditLowText(text) {
			t.Errorf("%s: not recognised", name)
		}
	}
	for name, text := range map[string]string{
		"typed by the person": "> Credit balance is too low",
		"typed with caret":    "❯ Credit balance is too low",
		"mid sentence":        "The error says Credit balance is too low and nothing else.",
		"other limit":         "You've hit your weekly limit",
		"empty":               "",
	} {
		if IsClaudeCreditLowText(text) {
			t.Errorf("%s: wrongly recognised", name)
		}
	}
}
