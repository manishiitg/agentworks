package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/sparkquillproduct"
	wf "github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

type sparkquillExternalJob struct {
	ActivityDir  string     `json:"activity_dir,omitempty"`
	ID           string     `json:"job_id"`
	UserID       string     `json:"user_id"`
	ConnectionID string     `json:"connection_id"`
	Fingerprint  string     `json:"fingerprint"`
	Status       string     `json:"status"`
	SessionID    string     `json:"session_id,omitempty"`
	Result       string     `json:"result,omitempty"`
	Error        string     `json:"error,omitempty"`
	StartedAt    time.Time  `json:"started_at"`
	CompletedAt  *time.Time `json:"completed_at,omitempty"`
}

// Submissions serialize per family; private receipts preserve retry identities
// across restarts. An uncertain running job is never automatically replayed.
var sparkquillJobs = struct {
	sync.Mutex
	active map[string]string
}{active: map[string]string{}}
var sparkquillJobIDPattern = regexp.MustCompile(`^sq_job_[a-f0-9]{64}$`)

func sparkquillJobPath(id string) (string, error) {
	if !sparkquillJobIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid job_id")
	}
	root, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "sparkquill-mcp-jobs", id+".json"), nil
}
func readSparkQuillJob(id string) (sparkquillExternalJob, error) {
	var job sparkquillExternalJob
	p, err := sparkquillJobPath(id)
	if err != nil {
		return job, err
	}
	data, err := os.ReadFile(p)
	if err == nil {
		err = json.Unmarshal(data, &job)
	}
	return job, err
}
func writeSparkQuillJob(job sparkquillExternalJob) error {
	p, err := sparkquillJobPath(job.ID)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(job)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(p), ".job-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(file.Name(), p)
}
func sparkquillJobResponse(job sparkquillExternalJob) map[string]any {
	return map[string]any{"job_id": job.ID, "status": job.Status, "result": job.Result, "error": job.Error, "started_at": job.StartedAt, "completed_at": job.CompletedAt}
}
func sparkquillJobActive(job sparkquillExternalJob) bool {
	sparkquillJobs.Lock()
	defer sparkquillJobs.Unlock()
	return sparkquillJobs.active[job.UserID] == job.ID
}

func (api *StreamingAPI) externalSparkQuillStart(w http.ResponseWriter, r *http.Request, name string, args map[string]any) {
	c := GetUserFromContext(r.Context())
	if api.agentProfiles == nil {
		externalError(w, 503, "unavailable", "SparkQuill is not ready.")
		return
	}
	profile, err := api.agentProfiles.Resolve(sparkquillproduct.ParentProfileID, 0, c.UserID)
	if err != nil {
		externalFailure(w, err)
		return
	}
	body, _ := json.Marshal(map[string]any{"tool": name, "arguments": args})
	id := "sq_job_" + wf.Revision([]byte(c.UserID+"\x00"+c.AccessToken.ID+"\x00"+externalArg(args, "request_id")))
	job := sparkquillExternalJob{ID: id, UserID: c.UserID, ConnectionID: c.AccessToken.ID, Fingerprint: wf.Revision(body), Status: "running", StartedAt: time.Now().UTC()}
	sparkquillJobs.Lock()
	previous, readErr := readSparkQuillJob(id)
	if readErr == nil {
		active := sparkquillJobs.active[c.UserID] == id
		sparkquillJobs.Unlock()
		if previous.Fingerprint != job.Fingerprint {
			externalError(w, 409, "request_id_conflict", "Reuse request_id only for identical requests.")
			return
		}
		if previous.Status == "running" && !active {
			previous.Status = "interrupted"
			previous.Error = "The server restarted before this job completed. Inspect activity files before making a new request."
		}
		externalJSON(w, sparkquillJobResponse(previous))
		return
	}
	if !errors.Is(readErr, os.ErrNotExist) {
		sparkquillJobs.Unlock()
		externalFailure(w, readErr)
		return
	}
	if sparkquillJobs.active[c.UserID] != "" {
		sparkquillJobs.Unlock()
		externalError(w, 409, "activity_busy", "Another MCP activity job is running for this family. Wait for it before starting another.")
		return
	}
	dir := ""
	if name == "update_learning_activity" {
		dir, err = sparkquillActivityDir(externalArg(args, "dir"))
		if err != nil {
			externalError(w, 400, "invalid_arguments", err.Error())
			sparkquillJobs.Unlock()
			return
		}
		m, e := sparkquillRead(r.Context(), c, dir+"/activity.json")
		if e != nil {
			externalFailure(w, e)
			sparkquillJobs.Unlock()
			return
		}
		if !m.Exists {
			externalError(w, 404, "not_found", "Activity not found.")
			sparkquillJobs.Unlock()
			return
		}
	}
	state, err := sparkquillLoadFamily(r.Context(), c)
	if err != nil {
		externalFailure(w, err)
		sparkquillJobs.Unlock()
		return
	}
	job.ActivityDir = dir
	binding, err := resolveIsolatedProductBinding(r.Context(), c.UserID, profile, "mcp:"+id, "SparkQuill · MCP activity")
	if err != nil {
		sparkquillJobs.Unlock()
		externalFailure(w, err)
		return
	}
	conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(r.Context(), c.UserID, profile, binding, "")
	if err != nil {
		sparkquillJobs.Unlock()
		externalFailure(w, err)
		return
	}
	job.SessionID = conversation.SessionID
	message := "This parent-authorized request came through SparkQuill MCP. Work only in this family's SparkQuill workspace. Build and verify the requested learning activity, finalize it, and return its activity directory and finished item paths. Do not assign it to the child.\n"
	if name == "update_learning_activity" {
		message += "Revise existing activity " + dir + ".\n"
	} else {
		message += "Create format: " + externalArg(args, "format") + ".\n"
	}
	message += "Parent's brief:\n" + externalArg(args, "brief")
	input := AgentProfileChatRequest{Message: message, Engine: state.Engine, ModelID: state.ParentModel, ReasoningEffort: state.ParentReasoningEffort, ConnectionID: state.ConnectionID}
	if input.ModelID == "" {
		input.ModelID = state.Model
	}
	req, err := queryRequestForAgentProfileChat(profile, input, conversation)
	if err != nil {
		sparkquillJobs.Unlock()
		externalFailure(w, err)
		return
	}
	reqMap, err := queryRequestToMap(req)
	if err != nil {
		sparkquillJobs.Unlock()
		externalFailure(w, err)
		return
	}
	reqMap["triggered_by"] = "sparkquill-mcp"
	reqMap["triggered_by_label"] = "SparkQuill MCP activity"
	if err = writeSparkQuillJob(job); err != nil {
		sparkquillJobs.Unlock()
		externalFailure(w, err)
		return
	}
	sparkquillJobs.active[c.UserID] = id
	sparkquillJobs.Unlock()
	// sparkquill:run explicitly delegates to the normal parent profile and its
	// sandbox, without giving the external token arbitrary query access.
	actor := *c
	actor.AccessToken = nil
	runCtx := context.WithValue(context.Background(), UserContextKey, &actor)
	go executeSparkQuillJob(runCtx, job, func(ctx context.Context) (internalSessionTurnResult, error) {
		return api.startSessionInternalWithResult(ctx, reqMap, conversation.SessionID, c.UserID, nil)
	})
	externalJSON(w, sparkquillJobResponse(job))
}
func (api *StreamingAPI) externalSparkQuillStatus(w http.ResponseWriter, r *http.Request, args map[string]any) {
	c := GetUserFromContext(r.Context())
	job, err := readSparkQuillJob(externalArg(args, "job_id"))
	if err != nil || job.UserID != c.UserID || c.AccessToken == nil || job.ConnectionID != c.AccessToken.ID {
		externalError(w, 404, "not_found", "Job not found for this connection.")
		return
	}
	if job.Status == "running" && !sparkquillJobActive(job) {
		job.Status = "interrupted"
		job.Error = "The server restarted before this job completed. Inspect activity files before making a new request."
	}
	out := sparkquillJobResponse(job)
	if job.Status == "completed" {
		activities, err := sparkquillActivities(r.Context(), c, r, "")
		if err != nil {
			externalFailure(w, err)
			return
		}
		dirs := sparkquillResultActivityDirs(job)
		matched := []map[string]any{}
		for _, activity := range activities {
			if dir, ok := activity["dir"].(string); ok && dirs[dir] {
				matched = append(matched, activity)
			}
		}
		out["activities"] = matched
	}
	externalJSON(w, out)
}

func executeSparkQuillJob(ctx context.Context, job sparkquillExternalJob, run func(context.Context) (internalSessionTurnResult, error)) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	defer func() { sparkquillJobs.Lock(); delete(sparkquillJobs.active, job.UserID); sparkquillJobs.Unlock() }()
	result, err := run(ctx)
	job.Status = "completed"
	job.Result = result.FinalResponse
	if err != nil {
		job.Status = "error"
		job.Error = err.Error()
	}
	now := time.Now().UTC()
	job.CompletedAt = &now
	if err := writeSparkQuillJob(job); err != nil {
		scheduleLogf("[SPARKQUILL-MCP] cannot persist result for %s: %v", job.ID, err)
	}
}

var sparkquillResultDirectoryPattern = regexp.MustCompile("activities/[a-z0-9][a-z0-9._-]{0,99}")

func sparkquillResultActivityDirs(job sparkquillExternalJob) map[string]bool {
	dirs := map[string]bool{}
	if job.ActivityDir != "" {
		dirs[job.ActivityDir] = true
	}
	for _, dir := range sparkquillResultDirectoryPattern.FindAllString(job.Result, -1) {
		dirs[dir] = true
	}
	return dirs
}
