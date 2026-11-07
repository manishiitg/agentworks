package server

import (
	"encoding/json"
	"testing"
	"time"
)

// A Crew's run row says what to open (its saved conversation and its Crew); a run with no saved conversation says
// nothing, and the saved run's own fields stay where they were.
func TestCrewScheduleRunRowsNameWhatToOpen(t *testing.T) {
	started := time.Now()
	rows := crewScheduleRunRows([]ScheduleRunEntry{
		{ID: "run-1", ScheduleID: "daily", SessionID: "session-1", Status: "success", StartedAt: started},
		{ID: "run-2", ScheduleID: "daily", Status: "error", StartedAt: started},
	}, "crew-1", "Crew/crew-1", "Daily check", nil)
	encoded, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	open, _ := decoded[0]["open"].(map[string]any)
	if decoded[0]["id"] != "run-1" || decoded[0]["session_id"] != "session-1" || open["session_id"] != "session-1" || open["crew_project_id"] != "crew-1" || open["title"] != "Daily check" {
		t.Fatalf("run with a conversation: %v", decoded[0])
	}
	if _, has := decoded[1]["open"]; has || decoded[1]["id"] != "run-2" {
		t.Fatalf("run without a conversation must not name one: %v", decoded[1])
	}
}
