package mcpclient

import "testing"

func TestHTTPParentRequiresUniqueLiveRegistration(t *testing.T) {
	tracker := &httpSessionTracker{sessions: map[string]map[string]struct{}{}, stoppedSessions: map[string]struct{}{}}
	if got := tracker.parent("group"); got != "" {
		t.Fatal(got)
	}
	tracker.register("run", "group")
	if got := tracker.parent("group"); got != "run" {
		t.Fatal(got)
	}
	tracker.register("other", "group")
	if got := tracker.parent("group"); got != "" {
		t.Fatal("ambiguous parent", got)
	}
	tracker.remove("other")
	tracker.markStopped([]string{"group"})
	if got := tracker.parent("group"); got != "" {
		t.Fatal("stopped parent", got)
	}
	tracker.clearStopped([]string{"group"})
	tracker.remove("run")
	if got := tracker.parent("group"); got != "" {
		t.Fatal("removed parent", got)
	}
}
