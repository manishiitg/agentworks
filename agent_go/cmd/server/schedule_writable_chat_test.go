package server

import (
	"net/http/httptest"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

// A message schedule's own conversation is a chat its person can reply in: the run list names its key, a message typed
// there lands in the conversation the schedule's runs use, and the next run continues it. A new conversation each time
// names a different one per run, and nobody but the person the schedule runs as is offered it.
func TestScheduleOwnConversationTakesAReplyAndTheNextRunContinuesIt(t *testing.T) {
	f := newAgentWorksFixture(t)
	crew, err := f.API.ensureAgentWorks(f.Ctx(fixtureUserA), fixtureUserA)
	if err != nil {
		t.Fatal(err)
	}
	ctx := f.Ctx(fixtureUserA)
	profile := f.Crew
	store := defaultProductConversationRegistryStore()
	run := func(job productScheduleJob, runID string) ProductConversationRecord {
		t.Helper()
		conversation := job.ConversationKey
		if job.Schedule.New {
			conversation = runID
		}
		binding, err := resolveIsolatedProjectAutomationBinding(ctx, job.UserID, profile, job.ProjectID, "schedule", crewCallIsolatedKey(job.Schedule.ID, conversation), "Daily")
		if err != nil {
			t.Fatal(err)
		}
		record, err := store.resolveOrCreate(ctx, job.UserID, profile, binding, "")
		if err != nil {
			t.Fatal(err)
		}
		return record
	}
	job := productScheduleJob{UserID: fixtureUserA, Profile: profile, ProjectID: crew.CrewID, Schedule: productschedule.Schedule{ID: "daily", Name: "Daily", Isolated: true, Messages: []string{"Check the numbers."}}}
	first := run(job, "run-1")
	target := func(j productScheduleJob, viewer string, runID string) *scheduleRunOpenTarget {
		rows := crewScheduleRunRows([]ScheduleRunEntry{{ID: runID, SessionID: "any", Status: "success"}}, crew.CrewID, crew.WorkspacePath, "Daily", writableScheduleConversation(j, viewer))
		return rows[0].Open
	}
	open := target(job, fixtureUserA, "run-1")
	if !open.Writable || open.ConversationKey == "" {
		t.Fatalf("the schedule's own conversation is not offered as a chat: %+v", open)
	}
	reply, err := f.API.resolveAgentProfileConversation(httptest.NewRequest("POST", "/", nil).WithContext(ctx), profile, open.ConversationKey, "")
	if err != nil {
		t.Fatalf("a reply typed there could not be addressed: %v", err)
	}
	if reply.SessionID != first.SessionID {
		t.Fatalf("a typed message went to session %q, the schedule's runs use %q", reply.SessionID, first.SessionID)
	}
	if next := run(job, "run-2"); next.SessionID != first.SessionID {
		t.Fatalf("the next run started session %q instead of continuing %q", next.SessionID, first.SessionID)
	}
	if other := target(job, fixtureUserB, "run-1"); other.Writable || other.ConversationKey != "" {
		t.Fatalf("another person was offered the schedule's own conversation: %+v", other)
	}

	fresh := job
	fresh.Schedule.New = true
	one, two := run(fresh, "run-a"), run(fresh, "run-b")
	if one.SessionID == two.SessionID {
		t.Fatal("two runs of a new-conversation-each-time schedule shared a conversation")
	}
	keyOne, keyTwo := target(fresh, fixtureUserA, "run-a").ConversationKey, target(fresh, fixtureUserA, "run-b").ConversationKey
	if keyOne == keyTwo {
		t.Fatal("two runs named the same conversation")
	}
	replyOne, err := f.API.resolveAgentProfileConversation(httptest.NewRequest("POST", "/", nil).WithContext(ctx), profile, keyOne, "")
	if err != nil || replyOne.SessionID != one.SessionID {
		t.Fatalf("a reply to the first run's conversation went to %q, want %q (%v)", replyOne.SessionID, one.SessionID, err)
	}
	// A saved run with no messages has no chat to reply in.
	noMessages := job
	noMessages.Schedule.Messages = nil
	if writableScheduleConversation(noMessages, fixtureUserA) != nil {
		t.Fatal("a schedule with no messages was offered a chat")
	}
	// A schedule that runs in a chat has no conversation of its own to open.
	inChat := job
	inChat.Schedule.Isolated = false
	if writableScheduleConversation(inChat, fixtureUserA) != nil {
		t.Fatal("a schedule in a chat was offered a conversation of its own")
	}
}
