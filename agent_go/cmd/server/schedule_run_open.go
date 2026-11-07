package server

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
}

// scheduleRunRow is a saved run plus where to open it.
type scheduleRunRow struct {
	ScheduleRunEntry
	Open *scheduleRunOpenTarget `json:"open,omitempty"`
}

// crewScheduleRunRows adds the open target to each run of a Crew's schedule or
// trigger. A run without a saved session has nothing to open.
func crewScheduleRunRows(runs []ScheduleRunEntry, crewID, workspacePath, title string) []scheduleRunRow {
	rows := make([]scheduleRunRow, 0, len(runs))
	for _, run := range runs {
		row := scheduleRunRow{ScheduleRunEntry: run}
		if run.SessionID != "" && crewID != "" {
			row.Open = &scheduleRunOpenTarget{SessionID: run.SessionID, CrewProjectID: crewID, WorkspacePath: workspacePath, Title: title}
		}
		rows = append(rows, row)
	}
	return rows
}
