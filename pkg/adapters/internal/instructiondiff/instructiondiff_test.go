package instructiondiff

import (
	"strings"
	"testing"
)

func lines(n int, change map[int]string) string {
	out := make([]string, n)
	for i := range out {
		out[i] = "rule number " + strings.Repeat("x", i%7) + string(rune('a'+i%26))
		if v, ok := change[i]; ok {
			out[i] = v
		}
	}
	return strings.Join(out, "\n")
}

func TestLinesSendsOnlyChangedLines(t *testing.T) {
	old := lines(400, nil)
	current := lines(400, map[int]string{120: "You are the \"ai-work\" agent in this Crew."})
	diff, ok := Lines(old, current)
	if !ok || !strings.Contains(diff, "+You are the \"ai-work\" agent") || !strings.Contains(diff, "-rule number") || strings.Count(diff, "\n") > 8 {
		t.Fatalf("diff = %q ok=%t", diff, ok)
	}
	if _, ok := Lines(old, "entirely\nnew"); ok {
		t.Fatal("a large change must send the whole set")
	}
}
