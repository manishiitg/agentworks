package server

import (
	"sort"
	"testing"
)

func resetScheduleOrigins(t *testing.T) {
	t.Helper()
	scheduleOrigins.mu.Lock()
	scheduleOrigins.originBySchedule = map[string]string{}
	scheduleOrigins.schedulesByOrigin = map[string]map[string]struct{}{}
	scheduleOrigins.mu.Unlock()
}

// A run triggered from a chat mints its own scheduled session, so all of its
// step and sub-agent terminals are filed under that session. The chat that
// asked for the run showed only its own agent — the work it requested appeared
// nowhere in the tab that requested it.
func TestTerminalScopeIncludesRunsTriggeredFromThisChat(t *testing.T) {
	resetScheduleOrigins(t)
	const chat = "d0afa8ad-5af3-4a3d-aeea-a6dfa7646975"

	rememberScheduleOrigin("schedule-manual--44163d28_1785290407859979000", chat)
	rememberScheduleOrigin("schedule-manual--8cd3f671_1785246057912544000", chat)

	got := sessionsTriggeredFrom(chat)
	sort.Strings(got)
	want := []string{
		chat,
		"schedule-manual--44163d28_1785290407859979000",
		"schedule-manual--8cd3f671_1785246057912544000",
	}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("scope = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("scope = %v, want %v", got, want)
		}
	}
}

// Scoping stays per-chat. A run another tab triggered must not appear here, or
// the fix trades one wrong listing for a noisier one.
func TestTerminalScopeExcludesOtherChatsRuns(t *testing.T) {
	resetScheduleOrigins(t)
	rememberScheduleOrigin("schedule-manual--aaaa_1", "chat-a")
	rememberScheduleOrigin("schedule-manual--bbbb_2", "chat-b")

	for _, session := range sessionsTriggeredFrom("chat-a") {
		if session == "schedule-manual--bbbb_2" {
			t.Fatal("chat-a must not see a run triggered from chat-b")
		}
	}
}

// An unknown chat scopes to itself, which is the pre-existing behavior and the
// path every non-triggering chat takes.
func TestUnknownChatScopesToItself(t *testing.T) {
	resetScheduleOrigins(t)
	got := sessionsTriggeredFrom("chat-with-no-runs")
	if len(got) != 1 || got[0] != "chat-with-no-runs" {
		t.Fatalf("scope = %v, want just the chat itself", got)
	}
	if sessionsTriggeredFrom("") != nil {
		t.Fatal("an empty session must not produce a scope")
	}
}

// The registry is capped so a long-lived server does not accumulate dead
// sessions, and eviction must keep both indexes consistent.
func TestOriginRegistryStaysBounded(t *testing.T) {
	resetScheduleOrigins(t)
	for i := 0; i < maxTrackedScheduleOrigins+50; i++ {
		rememberScheduleOrigin("schedule-manual--x_"+string(rune('a'+i%26))+string(rune('a'+i/26)), "chat-a")
	}
	scheduleOrigins.mu.RLock()
	defer scheduleOrigins.mu.RUnlock()
	if len(scheduleOrigins.originBySchedule) > maxTrackedScheduleOrigins {
		t.Fatalf("registry grew to %d, cap is %d", len(scheduleOrigins.originBySchedule), maxTrackedScheduleOrigins)
	}
	for origin, schedules := range scheduleOrigins.schedulesByOrigin {
		for scheduleSession := range schedules {
			if scheduleOrigins.originBySchedule[scheduleSession] != origin {
				t.Fatalf("reverse index kept %q after eviction", scheduleSession)
			}
		}
	}
}
