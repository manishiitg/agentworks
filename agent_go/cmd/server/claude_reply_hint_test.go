package server

import "testing"

func TestClaudeSummarisesPreToolText(t *testing.T) {
	for id, want := range map[string]bool{
		"claude-opus-5-5": true, "claude-fable-5-2": true, "claude-sonnet-5-5[1m]": true, "claude-opus-5-1-20261101": true, "claude-opus-6-0": true,
		"claude-opus-5": false, "claude-opus-4-8": false, "claude-sonnet-4-6": false, "gpt-5.4": false, "": false,
	} {
		if got := claudeSummarisesPreToolText(id); got != want {
			t.Errorf("%q: got %v, want %v", id, got, want)
		}
	}
}
