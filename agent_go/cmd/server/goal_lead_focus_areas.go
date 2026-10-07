package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Focus areas (PLAT-697 phase 4, docs/design/pulse_goal_owner.md "Focus
// areas"): what matters now, between soul.md (what the goal is) and goal
// memory (what was learned). Stored in the existing pulse.focus_areas of
// workflow.json, which stays the list of active areas every reader already
// uses (Goal Work's view, the settings editor); pulse.focus_area_details adds
// each area's lifecycle: proposed by the Pulse or added by the owner, an
// end date, its own check, daily tracking, and how it closed (with a lesson).
//
//   - The Pulse proposes (record_pulse_focus_area action=propose); it never
//     starts one silently. The owner confirms, rejects or adds one in the Pulse
//     tab; confirming adds the text to pulse.focus_areas.
//   - At most goalLeadMaxActiveFocusAreas are active or proposed at once.
//   - Each goal check tracks the active ones (moving, stuck with a clear ask,
//     done) and closes done or expired ones with a lesson, which also goes to
//     goal memory.
//   - An area the owner removes in settings is no longer active; one typed in
//     settings without details is active with no end date.

const (
	goalLeadMaxActiveFocusAreas = 3
	goalLeadFocusMaxDays        = 90
)

// PulseFocusArea is one focus area's lifecycle (pulse.focus_area_details).
type PulseFocusArea struct {
	ID           string `json:"id"`
	Text         string `json:"text"`
	Status       string `json:"status"` // proposed, active, done, expired, dropped
	EndDate      string `json:"end_date,omitempty"`
	Check        string `json:"check,omitempty"`
	Why          string `json:"why,omitempty"`
	ProposedBy   string `json:"proposed_by,omitempty"` // goal_lead or owner
	CreatedAt    string `json:"created_at,omitempty"`
	ConfirmedAt  string `json:"confirmed_at,omitempty"`
	Progress     string `json:"progress,omitempty"` // moving, stuck, done
	ProgressNote string `json:"progress_note,omitempty"`
	TrackedAt    string `json:"tracked_at,omitempty"`
	ClosedAt     string `json:"closed_at,omitempty"`
	Lesson       string `json:"lesson,omitempty"`
}

var goalLeadFocusMu sync.Mutex

func focusAreaID(text string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.Join(strings.Fields(text), " "))))
	return "FA-" + strings.ToUpper(hex.EncodeToString(sum[:])[:8])
}

func normalizeFocusText(text string) string { return strings.Join(strings.Fields(text), " ") }

// effectiveFocusAreas merges pulse.focus_areas (the active list) with the
// details: active areas first, then proposals, then the last closed ones.
func effectiveFocusAreas(pulse *WorkflowPulseConfig) []PulseFocusArea {
	if pulse == nil {
		return []PulseFocusArea{}
	}
	active, _ := normalizePulseFocusAreas(pulse.FocusAreas)
	activeSet := map[string]bool{}
	out := []PulseFocusArea{}
	for _, text := range active {
		activeSet[strings.ToLower(text)] = true
		area := PulseFocusArea{ID: focusAreaID(text), Text: text, Status: "active", ProposedBy: "owner"}
		for _, detail := range pulse.FocusAreaDetails {
			if strings.EqualFold(normalizeFocusText(detail.Text), text) && (detail.Status == "active" || detail.Status == "proposed") {
				area = detail
				area.Status = "active"
				break
			}
		}
		out = append(out, area)
	}
	var proposed, closed []PulseFocusArea
	for _, detail := range pulse.FocusAreaDetails {
		switch {
		case detail.Status == "proposed" && !activeSet[strings.ToLower(normalizeFocusText(detail.Text))]:
			proposed = append(proposed, detail)
		case detail.Status == "active" && !activeSet[strings.ToLower(normalizeFocusText(detail.Text))]:
			// Removed from the active list in settings.
			detail.Status = "dropped"
			if detail.Lesson == "" {
				detail.Lesson = "Removed by the owner in the Pulse settings."
			}
			closed = append(closed, detail)
		case detail.Status == "done" || detail.Status == "expired" || detail.Status == "dropped":
			closed = append(closed, detail)
		}
	}
	out = append(out, proposed...)
	if len(closed) > 5 {
		closed = closed[len(closed)-5:]
	}
	return append(out, closed...)
}

func focusAreasForView(ctx context.Context, workspacePath string) []PulseFocusArea {
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found || manifest == nil {
		return []PulseFocusArea{}
	}
	return effectiveFocusAreas(manifest.Pulse)
}

func parseFocusEndDate(value string, now time.Time) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("end_date is required (YYYY-MM-DD): every focus area ends")
	}
	day, err := time.Parse("2006-01-02", value)
	if err != nil {
		return "", fmt.Errorf("end_date must be YYYY-MM-DD")
	}
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if day.Before(today) {
		return "", fmt.Errorf("end_date %s is in the past", value)
	}
	if day.Sub(today) > goalLeadFocusMaxDays*24*time.Hour {
		return "", fmt.Errorf("end_date is more than %d days away; focus areas are short", goalLeadFocusMaxDays)
	}
	return value, nil
}

// focusAreaChange is one change to a workflow's focus areas.
type focusAreaChange struct {
	Action   string // propose, track, close (Pulse); confirm, reject, add, extend, close (owner)
	ID       string
	Text     string
	EndDate  string
	Check    string
	Why      string
	Progress string
	Note     string
	Status   string // close: done, expired, dropped
	Lesson   string
	ByOwner  bool
}

// applyFocusAreaChange changes pulse.focus_areas and its details in
// workflow.json and returns the changed area.
func applyFocusAreaChange(ctx context.Context, workspacePath string, change focusAreaChange, now time.Time) (*PulseFocusArea, error) {
	goalLeadFocusMu.Lock()
	defer goalLeadFocusMu.Unlock()
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil {
		return nil, err
	}
	if !found || manifest == nil {
		return nil, fmt.Errorf("workflow %s not found", workspacePath)
	}
	if manifest.Pulse == nil {
		manifest.Pulse = &WorkflowPulseConfig{}
	}
	pulse := manifest.Pulse
	active, err := normalizePulseFocusAreas(pulse.FocusAreas)
	if err != nil {
		return nil, err
	}
	stamp := formatStoredTime(now)
	// Materialize every effective area as a detail, so an owner-typed area
	// gets a lifecycle the first time it is tracked or closed.
	areas := effectiveFocusAreas(pulse)
	find := func() (int, error) {
		id := strings.TrimSpace(change.ID)
		for i, area := range areas {
			if id != "" && strings.EqualFold(area.ID, id) {
				return i, nil
			}
			if id == "" && change.Text != "" && strings.EqualFold(normalizeFocusText(area.Text), normalizeFocusText(change.Text)) {
				return i, nil
			}
		}
		return -1, fmt.Errorf("no focus area %q; use an id from the focus areas", firstNonEmptyTrimmed(change.ID, change.Text))
	}
	countOpen := func() int {
		n := 0
		for _, area := range areas {
			if area.Status == "active" || area.Status == "proposed" {
				n++
			}
		}
		return n
	}
	var changed PulseFocusArea
	switch change.Action {
	case "propose", "add":
		text := normalizeFocusText(change.Text)
		if text == "" {
			return nil, fmt.Errorf("text is required: what to focus on, in plain words")
		}
		if len(text) > maxPulseFocusAreaLength {
			return nil, fmt.Errorf("text is limited to %d characters", maxPulseFocusAreaLength)
		}
		if i, _ := find(); i >= 0 && (areas[i].Status == "active" || areas[i].Status == "proposed") {
			return nil, fmt.Errorf("%q is already a focus area (%s)", text, areas[i].Status)
		}
		if countOpen() >= goalLeadMaxActiveFocusAreas {
			return nil, fmt.Errorf("%d focus areas are already active or proposed; close or reject one first", goalLeadMaxActiveFocusAreas)
		}
		end, err := parseFocusEndDate(change.EndDate, now)
		if err != nil {
			return nil, err
		}
		check := strings.TrimSpace(change.Check)
		if check == "" && !change.ByOwner {
			return nil, fmt.Errorf("check is required: how this focus is judged, e.g. \"drafts waiting 3 -> 0\"")
		}
		changed = PulseFocusArea{ID: focusAreaID(text), Text: text, Status: "proposed", EndDate: end, Check: check,
			Why: strings.TrimSpace(change.Why), ProposedBy: "goal_lead", CreatedAt: stamp}
		if change.Action == "add" {
			changed.Status, changed.ProposedBy, changed.ConfirmedAt = "active", "owner", stamp
			active = append(active, text)
		}
		areas = append(areas, changed)
	case "confirm", "reject":
		i, err := find()
		if err != nil {
			return nil, err
		}
		if areas[i].Status != "proposed" {
			return nil, fmt.Errorf("%q is %s, not a proposal", areas[i].Text, areas[i].Status)
		}
		if change.Action == "confirm" {
			areas[i].Status, areas[i].ConfirmedAt = "active", stamp
			active = append(active, areas[i].Text)
		} else {
			areas[i].Status, areas[i].ClosedAt = "dropped", stamp
			areas[i].Lesson = firstNonEmptyTrimmed(change.Lesson, "The owner rejected this proposal.")
		}
		changed = areas[i]
	case "track":
		i, err := find()
		if err != nil {
			return nil, err
		}
		if areas[i].Status != "active" {
			return nil, fmt.Errorf("%q is %s; only active focus areas are tracked", areas[i].Text, areas[i].Status)
		}
		switch change.Progress {
		case "moving", "stuck", "done":
		default:
			return nil, fmt.Errorf("progress must be moving, stuck or done")
		}
		if change.Progress == "stuck" && strings.TrimSpace(change.Note) == "" {
			return nil, fmt.Errorf("a stuck focus area needs a note with the one clear ask")
		}
		areas[i].Progress, areas[i].ProgressNote, areas[i].TrackedAt = change.Progress, strings.TrimSpace(change.Note), stamp
		changed = areas[i]
	case "extend":
		i, err := find()
		if err != nil {
			return nil, err
		}
		if areas[i].Status != "active" {
			return nil, fmt.Errorf("%q is %s; only an active focus area can be extended", areas[i].Text, areas[i].Status)
		}
		end, err := parseFocusEndDate(change.EndDate, now)
		if err != nil {
			return nil, err
		}
		areas[i].EndDate = end
		changed = areas[i]
	case "close":
		i, err := find()
		if err != nil {
			return nil, err
		}
		if areas[i].Status != "active" {
			return nil, fmt.Errorf("%q is %s; only an active focus area is closed", areas[i].Text, areas[i].Status)
		}
		status := strings.TrimSpace(change.Status)
		switch status {
		case "done", "expired", "dropped":
		default:
			return nil, fmt.Errorf("status must be done, expired or dropped")
		}
		lesson := strings.TrimSpace(change.Lesson)
		if lesson == "" && !change.ByOwner {
			return nil, fmt.Errorf("lesson is required: one line on what this focus taught (for expired: why, and whether to extend, change or drop it)")
		}
		areas[i].Status, areas[i].ClosedAt, areas[i].Lesson = status, stamp, lesson
		kept := active[:0]
		for _, text := range active {
			if !strings.EqualFold(text, areas[i].Text) {
				kept = append(kept, text)
			}
		}
		active = kept
		changed = areas[i]
	default:
		return nil, fmt.Errorf("unknown action %q", change.Action)
	}
	if active, err = normalizePulseFocusAreas(active); err != nil {
		return nil, err
	}
	// Keep the open areas and the last ten closed ones.
	var open, closed []PulseFocusArea
	for _, area := range areas {
		if area.Status == "active" || area.Status == "proposed" {
			open = append(open, area)
		} else {
			closed = append(closed, area)
		}
	}
	if len(closed) > 10 {
		closed = closed[len(closed)-10:]
	}
	pulse.FocusAreas = active
	pulse.FocusAreaDetails = append(open, closed...)
	if err := WriteWorkflowManifest(ctx, workspacePath, manifest); err != nil {
		return nil, err
	}
	if change.Action == "close" && changed.Lesson != "" {
		line := fmt.Sprintf("Focus %q closed (%s): %s", changed.Text, changed.Status, changed.Lesson)
		source := "Pulse inference"
		if change.ByOwner {
			source = "owner answer"
		}
		_ = appendGoalMemory(workspacePath, "Lessons", goalMemoryLine(now, source, line), change.ByOwner)
	}
	return &changed, nil
}

// recordPulseFocusAreaFromToolArgs backs record_pulse_focus_area: the Goal
// Lead proposes, tracks and closes; it never confirms its own proposal.
func recordPulseFocusAreaFromToolArgs(ctx context.Context, args map[string]interface{}) (string, error) {
	workspacePath := stringToolArg(args, "workspace_path")
	if workspacePath == "" {
		return "", fmt.Errorf("record_pulse_focus_area requires workspace_path")
	}
	action := stringToolArg(args, "action")
	switch action {
	case "propose", "track", "close":
	default:
		return "", fmt.Errorf("action must be propose, track or close; the owner confirms proposals in the Pulse tab")
	}
	if err := checkPulsePlainText("the focus area's check and why",
		plainTextField{name: "text", text: stringToolArg(args, "text"), maxLen: maxPulseFocusAreaLength},
		plainTextField{name: "note", text: stringToolArg(args, "note"), maxLen: 400},
		plainTextField{name: "lesson", text: stringToolArg(args, "lesson"), maxLen: 400}); err != nil {
		return "", err
	}
	area, err := applyFocusAreaChange(ctx, workspacePath, focusAreaChange{
		Action: action, ID: stringToolArg(args, "focus_id"), Text: stringToolArg(args, "text"), EndDate: stringToolArg(args, "end_date"),
		Check: stringToolArg(args, "check"), Why: stringToolArg(args, "why"), Progress: stringToolArg(args, "progress"),
		Note: stringToolArg(args, "note"), Status: stringToolArg(args, "status"), Lesson: stringToolArg(args, "lesson"),
	}, time.Now().UTC())
	if err != nil {
		return "", err
	}
	switch action {
	case "propose":
		return fmt.Sprintf("Focus area %s proposed: %q until %s. It is shown to the owner to confirm; it is not active until they do.", area.ID, area.Text, area.EndDate), nil
	case "close":
		return fmt.Sprintf("Focus area %s closed (%s); the lesson is in goal memory.", area.ID, area.Status), nil
	}
	return fmt.Sprintf("Focus area %s tracked: %s.", area.ID, area.Progress), nil
}

// handleGoalLeadFocusArea is the owner's confirm, reject, add, extend and
// close in the Pulse tab.
func (api *StreamingAPI) handleGoalLeadFocusArea(w http.ResponseWriter, r *http.Request) {
	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var body struct {
		WorkspacePath string `json:"workspace_path"`
		Action        string `json:"action"`
		ID            string `json:"id"`
		Text          string `json:"text"`
		EndDate       string `json:"end_date"`
		Check         string `json:"check"`
		Status        string `json:"status"`
		Lesson        string `json:"lesson"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	if query := strings.TrimSpace(r.URL.Query().Get("workspace_path")); query != "" {
		body.WorkspacePath = query
	}
	workspacePath, err := normalizeReportHumanInputWorkspacePath(body.WorkspacePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch body.Action {
	case "confirm", "reject", "add", "extend", "close":
	default:
		http.Error(w, "action must be confirm, reject, add, extend or close", http.StatusBadRequest)
		return
	}
	status := body.Status
	if body.Action == "close" && status == "" {
		status = "dropped"
	}
	area, err := applyFocusAreaChange(r.Context(), workspacePath, focusAreaChange{
		Action: body.Action, ID: body.ID, Text: body.Text, EndDate: body.EndDate, Check: body.Check,
		Status: status, Lesson: body.Lesson, ByOwner: true,
	}, time.Now().UTC())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "focus_area": area, "focus_areas": focusAreasForView(r.Context(), workspacePath)})
}
