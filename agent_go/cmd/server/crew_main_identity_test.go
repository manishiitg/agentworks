package server

import "testing"

// #4030: asking Main to rename itself renamed the whole project. Main now keeps its own name on its tab, like any agent;
// with none (or only an automatic import label) it is "Main", never the project's name or a saved chat title.
func TestMainChatNameIsItsOwn(t *testing.T) {
	for _, c := range []struct {
		identity ProductAgentIdentity
		want     string
	}{
		{ProductAgentIdentity{Name: "Project lead"}, "Project lead"},
		{ProductAgentIdentity{}, "Main"},
		{ProductAgentIdentity{Name: "owner@example.com · Builder", GeneratedName: true}, "Main"},
	} {
		if got := mainChatName(c.identity); got != c.want {
			t.Fatalf("mainChatName(%+v) = %q, want %q", c.identity, got, c.want)
		}
	}
}
