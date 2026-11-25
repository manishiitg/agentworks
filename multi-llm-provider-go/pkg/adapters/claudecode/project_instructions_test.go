package claudecode

import (
	"errors"
	"testing"
	"time"
)

func TestReadsAgentsMDFollowsVersionAndCaches(t *testing.T) {
	saved := claudeBinaryVersion
	t.Cleanup(func() {
		claudeBinaryVersion = saved
		readsAgentsMDCache.at = time.Time{}
	})
	calls := 0
	for _, tc := range []struct {
		version string
		err     error
		want    bool
	}{
		{"/bin/claude 2.1.331 (Claude Code)", nil, true},
		{"/bin/claude 2.1.233 (Claude Code)", nil, false},
		{"", errors.New("no claude"), false},
	} {
		claudeBinaryVersion = func() (string, error) { calls++; return tc.version, tc.err }
		readsAgentsMDCache.at = time.Time{}
		if got := ReadsAgentsMD(); got != tc.want {
			t.Fatalf("%q: got %v want %v", tc.version, got, tc.want)
		}
	}
	before := calls
	ReadsAgentsMD()
	if calls != before {
		t.Fatal("a second call within ten minutes must use the cached answer")
	}
}
