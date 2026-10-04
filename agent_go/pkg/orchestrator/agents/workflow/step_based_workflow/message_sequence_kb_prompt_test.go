package step_based_workflow

import "testing"

// PLAT-438: the prompt's knowledge-base access matches the folder guard.
func TestMessageSequencePromptKBAccessMatchesTheGuard(t *testing.T) {
	for _, tc := range []struct {
		step  string
		write bool
		want  string
	}{
		{KBAccessNone, false, KBAccessNone},
		{KBAccessNone, true, KBAccessNone},
		{KBAccessRead, false, KBAccessRead},
		{KBAccessRead, true, KBAccessRead},
		{KBAccessReadWrite, false, KBAccessRead},
		{KBAccessReadWrite, true, KBAccessReadWrite},
	} {
		if got := messageSequencePromptKBAccess(tc.step, MessageSequenceWriteAccess{Knowledgebase: tc.write}); got != tc.want {
			t.Errorf("step %q, item write %v: prompt says %q, want %q", tc.step, tc.write, got, tc.want)
		}
	}
}
