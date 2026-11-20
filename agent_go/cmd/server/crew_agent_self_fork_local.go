package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

// A fork to this Mac runs as a job (#3218, owner 20 Nov: "this should be an async operation in the ui and show some
// kind of progress"). The menu starts it with async=1 and reads its stage; MCP keeps the one synchronous call.
type crewSelfForkJob struct {
	ID      string         `json:"id"`
	Owner   string         `json:"-"`
	Stage   string         `json:"stage"` // server, download, import, done, failed
	Line    string         `json:"line"`
	Percent int            `json:"percent"`
	Error   string         `json:"error,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
	Updated time.Time      `json:"updated_at"`
}

var crewSelfForkJobs = struct {
	sync.Mutex
	byID map[string]*crewSelfForkJob
}{byID: map[string]*crewSelfForkJob{}}

func (j *crewSelfForkJob) set(stage, line string, percent int) {
	crewSelfForkJobs.Lock()
	j.Stage, j.Line, j.Percent, j.Updated = stage, line, percent, time.Now()
	crewSelfForkJobs.Unlock()
}

type crewSelfForkFailure struct {
	status int
	line   string
	cause  error
}

func (f *crewSelfForkFailure) Error() string { return f.line }

// Both menu entry points and MCP land here. A remote Team ID is mapped only
// through this person's saved connection and owned, already joined local Crew.
func (api *StreamingAPI) handleCrewAgentSelfForkLocal(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if !IsLocalMode() || claims == nil || claims.UserID == "" || claims.Scope != "" {
		externalError(w, 403, "agent_self_fork_unavailable", "Fork from your own AgentWorks Mac app.")
		return
	}
	var in struct {
		Agent      string `json:"agent_id"`
		Connection string `json:"connection_id"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&in) != nil || in.Agent == "" {
		externalError(w, 400, "agent_self_fork_unavailable", "Choose the teammate's agent to fork.")
		return
	}
	project := mux.Vars(r)["project"]
	if r.URL.Query().Get("async") != "1" {
		out, err := api.runCrewAgentSelfForkLocal(r, claims, project, in.Agent, in.Connection, nil)
		if err != nil {
			var f *crewSelfForkFailure
			errors.As(err, &f)
			externalError(w, f.status, "agent_self_fork_unavailable", f.line)
			return
		}
		externalJSON(w, out)
		return
	}
	job := &crewSelfForkJob{ID: uuid.NewString(), Owner: claims.UserID, Stage: "server", Line: "Preparing the copy on your company's server…", Percent: 5, Updated: time.Now()}
	crewSelfForkJobs.Lock()
	for id, old := range crewSelfForkJobs.byID {
		if time.Since(old.Updated) > time.Hour {
			delete(crewSelfForkJobs.byID, id)
		}
	}
	crewSelfForkJobs.byID[job.ID] = job
	crewSelfForkJobs.Unlock()
	// The job outlives this request; it keeps the caller's identity, not its cancellation.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	detached := r.Clone(ctx)
	go func() {
		defer cancel()
		out, err := api.runCrewAgentSelfForkLocal(detached, claims, project, in.Agent, in.Connection, job)
		crewSelfForkJobs.Lock()
		defer crewSelfForkJobs.Unlock()
		job.Updated = time.Now()
		if err != nil {
			var f *crewSelfForkFailure
			errors.As(err, &f)
			job.Stage, job.Error, job.Percent = "failed", f.line, 100
			return
		}
		job.Stage, job.Line, job.Percent, job.Result = "done", "Your copy is ready.", 100, out
	}()
	w.WriteHeader(http.StatusAccepted)
	externalJSON(w, job)
}

// handleCrewAgentSelfForkJob reads one fork job's stage; only the person who started it can read it.
func (api *StreamingAPI) handleCrewAgentSelfForkJob(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	crewSelfForkJobs.Lock()
	job, ok := crewSelfForkJobs.byID[mux.Vars(r)["job"]]
	var copy crewSelfForkJob
	if ok {
		copy = *job
	}
	crewSelfForkJobs.Unlock()
	if !IsLocalMode() || claims == nil || !ok || copy.Owner != claims.UserID {
		externalError(w, 404, "agent_self_fork_unavailable", "This copy is no longer being made. Open your Crew to check its agents.")
		return
	}
	externalJSON(w, copy)
}

func (api *StreamingAPI) runCrewAgentSelfForkLocal(r *http.Request, claims *UserClaims, project, agent, connectionID string, job *crewSelfForkJob) (map[string]any, error) {
	step := func(stage, line string, percent int) {
		if job != nil {
			job.set(stage, line, percent)
		}
	}
	fail := func(status int, line, stage string, cause error) error {
		// The real cause, so a failed fork on a Mac can be traced from its log (#3218: the reason was dropped).
		log.Printf("[AGENT_FORK] project=%s agent=%s event=self_fork_local_failed stage=%s status=%d reason=%q", project, agent, stage, status, crewForkErrorText(cause))
		return &crewSelfForkFailure{status: status, line: line, cause: cause}
	}
	if connectionID != "" {
		c, err := desktopConnectionFor(claims.UserID, connectionID)
		if err != nil {
			return nil, fail(403, "Use one of your own saved connections.", "connection", err)
		}
		project = crewLocalProjectForTeam(claims.UserID, c, project)
	}
	if project == "" || !crewTeamClaims(claims, project, true) || ownerOfCrewProjectID(project) != claims.UserID {
		return nil, fail(409, "Add this Team to your Mac first, then fork its agent.", "project", nil)
	}
	localClaims, binding, connection, err := api.desktopCrewChatBinding(r.Context(), project)
	if err != nil || localClaims.UserID != claims.UserID {
		return nil, fail(409, "Connect your own copy of this Crew to its Team first.", "binding", err)
	}
	base := "/api/crew-teams/" + url.PathEscape(binding.PeerProjectID) + "/agent-forks"
	query := "?pair=" + url.QueryEscape(binding.PairID)
	var offer crewAgentFork
	if err = crewTeamRemoteJSON(r.Context(), claims.UserID, connection, http.MethodPost, base+query, map[string]string{"operation": "self", "agent_id": agent}, &offer); err != nil {
		return nil, fail(409, laptopActionErrorText(err.Error()), "server", err)
	}
	if !offer.SelfFork || !offer.Ready || offer.Agent.ID != agent || offer.Recipient != connection.User.ID || offer.Project != binding.PeerProjectID {
		return nil, fail(409, "The shared copy changed. Try forking again.", "server", nil)
	}
	step("download", "Downloading the conversation to your Mac…", 40)
	result, err := api.acceptCrewAgentForkOnMac(r, project, connection, binding.PeerProjectID, offer.ID, base, query, func() {
		step("import", "Adding the copy to your Crew…", 80)
	})
	if err != nil {
		return nil, fail(409, "Your copy is ready but wasn't added to your Mac: "+laptopActionErrorText(err.Error()), "import", err)
	}
	out, ok := result.(map[string]any)
	if !ok {
		return nil, fail(409, "The copy receipt is unavailable. Open your Crew to check it.", "receipt", nil)
	}
	out["snapshot_at"], out["project_id"] = offer.SnapshotAt, project
	return out, nil
}

func crewForkErrorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
