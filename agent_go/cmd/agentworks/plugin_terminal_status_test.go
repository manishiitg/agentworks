package main

import (
	"strings"
	"testing"
	"unicode"
)

// The visible line is metadata only and cannot inject terminal controls. Missing
// counts are not reported as zero; no Team does not claim that sharing is on.
func TestTerminalStatusLineStatesAndControls(t *testing.T) {
	two, one := 2, 1
	cases := []struct {
		status terminalStatus
		want   string
	}{
		{terminalStatus{}, ""},
		{terminalStatus{Attached: true}, "AgentWorks · connected"},
		{terminalStatus{Attached: true, Crew: "Design workspace"}, "AgentWorks · Project Design workspace"},
		{terminalStatus{Attached: true, Crew: "Design workspace", TeamOn: true}, "AgentWorks · Project Design workspace · Not shared"},
		{terminalStatus{Attached: true, Crew: "Design workspace", TeamOn: true, InTeam: true, People: &one, Online: &two, Unread: &one}, "AgentWorks · Project Design workspace · Shared with team · 1 person · 2 agents online · 1 new"},
	}
	for _, c := range cases {
		if got := terminalStatusLine(c.status); got != c.want {
			t.Fatalf("status line: %q != %q", got, c.want)
		}
	}
	line := terminalStatusLine(terminalStatus{Attached: true, Crew: "Private\x1b\nname\u202e", TeamOn: true, InTeam: true})
	if strings.IndexFunc(line, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 || strings.Contains(line, "0 online") || strings.Contains(line, "0 new") {
		t.Fatal("unsafe controls or invented counts in metadata line")
	}
}
