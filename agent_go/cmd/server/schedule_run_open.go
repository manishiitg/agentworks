package server

import "strings"

// Opening a Crew's schedule or trigger run: the server says what to open, the
// app opens it (the same run view the activity monitor uses). A run row of a
// Crew carries the saved conversation's session, the Crew it belongs to and its
// workspace. Only the server knows whether that conversation is the Crew's own
// chat or a conversation of the schedule alone, and the app decides nothing by
// who is looking.

// scheduleRunOpenTarget is what the app needs to open one run's conversation.
type scheduleRunOpenTarget struct {
	SessionID     string `json:"session_id"`
	CrewProjectID string `json:"crew_project_id"`
	WorkspacePath string `json:"workspace_path"`
	Title         string `json:"title,omitempty"`
	// ConversationKey and Writable say the run's conversation is a chat the person can reply in: the schedule's own
	// conversation (or the run's, for a new conversation each time). Without them the run opens as a read-only view.
	ConversationKey string `json:"conversation_key,omitempty"`
	Writable        bool   `json:"writable,omitempty"`
}

// scheduleRunRow is a saved run plus where to open it.
type scheduleRunRow struct {
	ScheduleRunEntry
	Open *scheduleRunOpenTarget `json:"open,omitempty"`
}

// scheduleRunConversation names a run's conversation when it can be written to.
type scheduleRunConversation func(run ScheduleRunEntry) (key string, writable bool)

// writableScheduleConversation is the conversation of a message schedule with its own conversation, or a new one
// each run, for the person the schedule runs as. A schedule in a chat has no conversation of its own to open.
func writableScheduleConversation(job productScheduleJob, viewerID string) scheduleRunConversation {
	if strings.TrimSpace(job.ProjectID) == "" || !job.Schedule.Isolated || len(compactScheduleMessages(job.Schedule.Messages)) == 0 || job.GuestCallerID != "" || job.UserID == "" || job.UserID != viewerID {
		return nil
	}
	kind := firstNonEmptyTrimmed(job.AutomationKind, "schedule")
	return func(run ScheduleRunEntry) (string, bool) {
		conversation := job.ConversationKey
		if job.Schedule.New {
			conversation = run.ID
		}
		return isolatedAutomationConversationKey(job.ProjectID, kind, crewCallIsolatedKey(job.Schedule.ID, conversation)), true
	}
}

// crewScheduleRunRows adds the open target to each run of a Crew's schedule or
// trigger. A run without a saved session has nothing to open.
func crewScheduleRunRows(runs []ScheduleRunEntry, crewID, workspacePath, title string, conversation scheduleRunConversation) []scheduleRunRow {
	rows := make([]scheduleRunRow, 0, len(runs))
	for _, run := range runs {
		row := scheduleRunRow{ScheduleRunEntry: run}
		if run.SessionID != "" && crewID != "" {
			row.Open = &scheduleRunOpenTarget{SessionID: run.SessionID, CrewProjectID: crewID, WorkspacePath: workspacePath, Title: title}
			if conversation != nil {
				row.Open.ConversationKey, row.Open.Writable = conversation(run)
			}
		}
		rows = append(rows, row)
	}
	return rows
}
