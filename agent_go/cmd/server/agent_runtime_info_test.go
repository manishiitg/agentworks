package server

import (
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"testing"
	"time"
)

// PLAT-856-572: a real pending question wins over a running turn; loss of the
// live connection wins over either. No question text reaches the directory.
func TestAgentActivityUsesTurnAndQuestionStateWithoutText(t *testing.T) {
	api := &StreamingAPI{}
	record := ProductConversationRecord{SessionID: t.Name()}
	if info := api.activityForRecord(record); info.Activity != "Idle" {
		t.Fatal(info)
	}
	api.setSessionBusy(record.SessionID, true)
	if info := api.activityForRecord(record); info.Activity != "Working" {
		t.Fatal(info)
	}
	feedback := virtualtools.GetHumanFeedbackStore()
	id := t.Name() + "-question"
	if err := feedback.CreatePendingRequest(id, "private question", "private context", record.SessionID, nil, true, time.Minute); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = feedback.SubmitResponse(id, "answered") })
	info := api.activityForRecord(record)
	if info.Activity != "Waiting for you" {
		t.Fatal(info)
	}
	row := map[string]interface{}{"online": true}
	projectAgentActivity(row, info.Activity, time.Now().Add(-3*time.Minute))
	if row["activity"] != "Waiting for you" || row["last_active_at"] == nil {
		t.Fatal(row)
	}
	row["online"] = false
	projectAgentActivity(row, info.Activity, info.LastActiveAt)
	if row["activity"] != "Offline" {
		t.Fatal(row)
	}
	if len(row) > 5 {
		t.Fatalf("activity projection contains unexpected data: %v", row)
	}
}
