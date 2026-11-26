package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productschedule"
)

// Run problems (#3617, owner-approved mockup "Workflow run problems"): a Crew workflow timer that was missed (the
// clock was not running, on a Mac usually because it slept; there is no catch-up) or whose run failed shows its
// owner one Needs-you card per workflow and kind. Misses of the same workflow join one card; a new failure replaces
// the older one; a successful run clears the failure card; Dismiss clears a card. Only the Crew owner sees them:
// a Mac Crew's workflows are private to its owner.

const (
	runProblemMissed    = "missed"
	runProblemFailed    = "failed"
	runProblemMaxMissed = 12
)

type runProblem struct {
	Kind       string    `json:"kind"`
	CrewID     string    `json:"crew_id"`
	ScheduleID string    `json:"schedule_id"`
	Name       string    `json:"name"`
	Owner      string    `json:"owner"`
	Missed     []string  `json:"missed,omitempty"` // RFC3339 scheduled times
	RunFolder  string    `json:"run_folder,omitempty"`
	Reason     string    `json:"reason,omitempty"`
	At         time.Time `json:"at"`
}

func (p runProblem) id() string { return p.Kind + ":" + p.ScheduleID }

type runProblemStore struct {
	mu sync.Mutex
}

var runProblems = &runProblemStore{}

func (s *runProblemStore) path() (string, error) {
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "run-problems.json"), nil
}

func (s *runProblemStore) update(change func(map[string]runProblem)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path, err := s.path()
	if err != nil {
		return err
	}
	all := map[string]runProblem{}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &all); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	change(all)
	raw, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func runProblemKey(crewID, id string) string { return crewID + "/" + id }

// noteMissed joins a missed occurrence into the workflow's missed card.
func (s *runProblemStore) noteMissed(job productScheduleJob, scheduledFor time.Time) {
	if job.ProjectID == "" || job.Schedule.WorkflowID == "" || scheduledFor.IsZero() {
		return
	}
	owner := firstNonEmptyTrimmed(job.CrewOwner, job.UserID)
	err := s.update(func(all map[string]runProblem) {
		p := runProblem{Kind: runProblemMissed, CrewID: job.ProjectID, ScheduleID: job.Schedule.ID}
		key := runProblemKey(job.ProjectID, p.id())
		if old, ok := all[key]; ok {
			p = old
		}
		p.Name, p.Owner, p.At = job.Schedule.Name, owner, time.Now().UTC()
		at := scheduledFor.UTC().Format(time.RFC3339)
		for _, seen := range p.Missed {
			if seen == at {
				all[key] = p
				return
			}
		}
		p.Missed = append(p.Missed, at)
		if len(p.Missed) > runProblemMaxMissed {
			p.Missed = p.Missed[len(p.Missed)-runProblemMaxMissed:]
		}
		all[key] = p
	})
	log.Printf("[RUN_PROBLEMS] crew=%s schedule=%s event=missed scheduled_for=%s saved=%t", job.ProjectID, job.Schedule.ID, scheduledFor.UTC().Format(time.RFC3339), err == nil)
}

// noteOutcome replaces the failure card on a failed run and clears it on a finished one.
func (s *runProblemStore) noteOutcome(job productScheduleJob, status, folder, reason string) {
	if job.ProjectID == "" || job.Schedule.WorkflowID == "" {
		return
	}
	key := runProblemKey(job.ProjectID, runProblemFailed+":"+job.Schedule.ID)
	err := s.update(func(all map[string]runProblem) {
		switch status {
		case "failed":
			all[key] = runProblem{Kind: runProblemFailed, CrewID: job.ProjectID, ScheduleID: job.Schedule.ID, Name: job.Schedule.Name,
				Owner: firstNonEmptyTrimmed(job.CrewOwner, job.UserID), RunFolder: folder, Reason: runProblemReason(reason), At: time.Now().UTC()}
		case "completed":
			delete(all, key)
		}
	})
	log.Printf("[RUN_PROBLEMS] crew=%s schedule=%s event=outcome status=%s saved=%t", job.ProjectID, job.Schedule.ID, status, err == nil)
}

// runProblemReason is the first plain line of the run's error, short enough for a card.
func runProblemReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if i := strings.IndexAny(reason, "\n"); i >= 0 {
		reason = strings.TrimSpace(reason[:i])
	}
	if len(reason) > 240 {
		reason = reason[:240] + "…"
	}
	return reason
}

// runProblemCard is one Needs-you card as the server words it; the page only draws it.
type runProblemCard struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Title   string   `json:"title"`
	Line    string   `json:"line"`
	At      string   `json:"at"`
	Buttons []string `json:"buttons"`
}

func runProblemWhen(at string) string {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return at
	}
	local := t.In(time.Local)
	if local.YearDay() == time.Now().YearDay() && local.Year() == time.Now().Year() {
		return local.Format("15:04")
	}
	return local.Format("Mon 15:04")
}

func (p runProblem) card(keepAwake string) runProblemCard {
	name := firstNonEmptyTrimmed(p.Name, "This workflow")
	c := runProblemCard{ID: p.id(), Kind: p.Kind, Title: name, At: p.At.Format(time.RFC3339)}
	switch p.Kind {
	case runProblemMissed:
		why := "because the scheduler wasn't running"
		if IsLocalMode() {
			why = "because your Mac was asleep"
		}
		times := make([]string, 0, len(p.Missed))
		for _, at := range p.Missed {
			times = append(times, runProblemWhen(at))
		}
		if len(times) == 1 {
			c.Line = fmt.Sprintf("%s didn't run at %s %s. It is not run later.", name, times[0], why)
		} else {
			c.Line = fmt.Sprintf("%s didn't run %d times (%s) %s. They are not run later.", name, len(times), strings.Join(times, ", "), why)
		}
		c.Buttons = []string{"Run now"}
		if IsLocalMode() && keepAwake != keepAwakeUpcoming {
			c.Buttons = append(c.Buttons, "Keep Mac awake for this")
		}
	case runProblemFailed:
		c.Line = fmt.Sprintf("%s failed at %s.", name, runProblemWhen(p.At.Format(time.RFC3339)))
		if p.Reason != "" {
			c.Line = fmt.Sprintf("%s failed at %s: %s", name, runProblemWhen(p.At.Format(time.RFC3339)), p.Reason)
		}
		c.Buttons = []string{"Resume", "Ask the agent"}
	}
	c.Buttons = append(c.Buttons, "Dismiss")
	return c
}

// cardsFor lists the owner's cards for one Crew, newest first.
func (s *runProblemStore) cardsFor(crewID, viewer string, keepAwake func(scheduleID string) string) []runProblemCard {
	cards := []runProblemCard{}
	_ = s.update(func(all map[string]runProblem) {
		for key, p := range all {
			if !strings.HasPrefix(key, crewID+"/") || p.Owner != viewer {
				continue
			}
			cards = append(cards, p.card(keepAwake(p.ScheduleID)))
		}
	})
	sort.SliceStable(cards, func(i, j int) bool { return cards[i].At > cards[j].At })
	return cards
}

func (s *runProblemStore) take(crewID, id, viewer string) (runProblem, bool) {
	var found runProblem
	ok := false
	_ = s.update(func(all map[string]runProblem) {
		if p, exists := all[runProblemKey(crewID, id)]; exists && p.Owner == viewer {
			found, ok = p, true
		}
	})
	return found, ok
}

func (s *runProblemStore) dismiss(crewID, id string) error {
	return s.update(func(all map[string]runProblem) { delete(all, runProblemKey(crewID, id)) })
}

// GET /api/crew-run-problems/{project}: the owner's run-problem cards for this Crew.
// POST /api/crew-run-problems/{project}/{id} {button}: Run now, Keep Mac awake for this and Dismiss act here;
// Resume and Ask the agent come back as a message for the Crew's agent (resuming is the Builder's chat tool).
func (api *StreamingAPI) handleCrewRunProblems(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil {
		http.Error(w, "sign in", http.StatusUnauthorized)
		return
	}
	project := mux.Vars(r)["project"]
	jobs := map[string]productScheduleJob{}
	if api.projectActions != nil {
		if all, err := api.projectActions.JobsForUser(r.Context(), claims.UserID); err == nil {
			for _, job := range all {
				if job.ProjectID == project {
					jobs[job.Schedule.ID] = job
				}
			}
		}
	}
	if r.Method == http.MethodGet {
		externalJSON(w, map[string]any{"cards": runProblems.cardsFor(project, claims.UserID, func(id string) string { return jobs[id].Schedule.KeepAwake })})
		return
	}
	id := mux.Vars(r)["id"]
	var in struct {
		Button string `json:"button"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
		externalError(w, http.StatusBadRequest, "invalid_button", "Choose one of the card's buttons.")
		return
	}
	p, ok := runProblems.take(project, id, claims.UserID)
	if !ok {
		externalError(w, http.StatusNotFound, "card_gone", "This card is already cleared. Refresh the panel.")
		return
	}
	name := firstNonEmptyTrimmed(p.Name, "this workflow")
	reply := map[string]any{"ok": true}
	switch in.Button {
	case "Dismiss":
		if err := runProblems.dismiss(project, id); err != nil {
			externalError(w, http.StatusInternalServerError, "dismiss_failed", "The card could not be cleared. Try again.")
			return
		}
	case "Run now":
		job, found := jobs[p.ScheduleID]
		if !found {
			externalError(w, http.StatusNotFound, "schedule_gone", "This schedule is gone. Ask the project's agent to run the workflow.")
			return
		}
		if _, err := api.projectActions.Trigger(r.Context(), claims.UserID, job.ID()); err != nil {
			externalError(w, http.StatusConflict, "run_refused", err.Error())
			return
		}
		_ = runProblems.dismiss(project, id)
		reply["note"] = name + " is running now."
	case "Keep Mac awake for this":
		job, found := jobs[p.ScheduleID]
		if !found || !IsLocalMode() {
			externalError(w, http.StatusNotFound, "schedule_gone", "This schedule is gone, or this isn't a Mac.")
			return
		}
		if _, err := api.projectActions.UpdateProjectSchedule(r.Context(), claims.UserID, job.ID(), func(s *productschedule.Schedule) { s.KeepAwake = keepAwakeUpcoming }); err != nil {
			externalError(w, http.StatusConflict, "keep_awake_refused", err.Error())
			return
		}
		reply["note"] = "Your Mac will stay awake for " + name + " while it's plugged in."
	case "Resume":
		reply["ask"] = fmt.Sprintf("Resume the failed run %s of %s.", p.RunFolder, name)
	case "Ask the agent":
		reply["ask"] = fmt.Sprintf("Why did the run %s of %s fail? Tell me what to do.", p.RunFolder, name)
	default:
		externalError(w, http.StatusBadRequest, "invalid_button", "Choose one of the card's buttons.")
		return
	}
	log.Printf("[RUN_PROBLEMS] crew=%s card=%s event=button button=%q", project, id, in.Button)
	externalJSON(w, reply)
}
