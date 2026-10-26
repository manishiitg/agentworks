package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentworksclient"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type externalMCPTask struct {
	ID                        string
	Family                    agentworksclient.MCPTaskFamily
	JobID, WorkflowID         string
	Created, Updated, Expires time.Time
	Status, IgnoredInput      string
}

// Handles contain routing metadata only. Execution and its results remain in
// their existing stores; every access redispatches the native scoped operation.
func openExternalMCPTasks() (*mcpOAuthStore, error) {
	s, err := openMCPOAuthStore()
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS external_mcp_tasks (
 id TEXT PRIMARY KEY, user_id TEXT NOT NULL, connection_id TEXT NOT NULL,
 family TEXT NOT NULL, job_id TEXT NOT NULL, workflow_id TEXT NOT NULL,
 created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, expires_at INTEGER NOT NULL,
 status TEXT NOT NULL DEFAULT 'working', ignored_input TEXT NOT NULL DEFAULT '',
 UNIQUE(user_id,connection_id,job_id,family))`)
	if err == nil {
		_, err = s.db.Exec(`DELETE FROM external_mcp_tasks WHERE expires_at < ?`, time.Now().Unix())
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func mcpTaskPrincipal(r *http.Request) (string, string) {
	c := GetUserFromContext(r.Context())
	if c == nil {
		return "", ""
	}
	connection := "website"
	if c.AccessToken != nil {
		connection = c.AccessToken.ID
	}
	return c.UserID, connection
}

func (t externalMCPTask) args() map[string]any {
	args := map[string]any{t.Family.IDField: t.JobID, "wait_seconds": 0}
	if t.Family.Workflow {
		args["workflow_id"] = t.WorkflowID
	}
	if t.Family.Poll == "run_status" {
		args["compact"] = true
	}
	return args
}

func (t externalMCPTask) view(kind string) map[string]any {
	return map[string]any{"resultType": kind, "taskId": t.ID, "status": t.Status,
		"createdAt": t.Created.UTC().Format(time.RFC3339Nano), "lastUpdatedAt": t.Updated.UTC().Format(time.RFC3339Nano),
		"ttlMs": int64(t.Expires.Sub(t.Created) / time.Millisecond), "pollIntervalMs": 2000}
}

func mcpTaskToolResult(raw json.RawMessage) json.RawMessage {
	var value any
	_ = json.Unmarshal(raw, &value)
	result, err := mcp.NewToolResultJSON(value)
	if err != nil {
		result = mcp.NewToolResultError("The operation returned an invalid response.")
	}
	result.ResultType = mcp.ResultTypeComplete
	encoded, _ := json.Marshal(result)
	return encoded
}

// Both stdio and remote MCP use this authenticated adapter. No task ID is a
// bearer credential: the person and connection family must also match.
func (api *StreamingAPI) handleExternalMCPTasks(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Method            string          `json:"method"`
		Params            json.RawMessage `json:"params"`
		GuidanceSessionID string          `json:"guidance_session_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, agentworksclient.MaxBodyBytes)).Decode(&request); err != nil {
		externalError(w, 400, "invalid_arguments", "Invalid task request.")
		return
	}
	r = externalWithGuidanceHandle(r, request.GuidanceSessionID)
	raw, status, err := api.externalMCPTaskOperation(r, request.Method, request.Params)
	if err != nil {
		externalError(w, status, "task_unavailable", err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(raw)
}

func (api *StreamingAPI) externalMCPTaskOperation(r *http.Request, method string, params json.RawMessage) (json.RawMessage, int, error) {
	user, connection := mcpTaskPrincipal(r)
	if user == "" {
		return nil, 404, errors.New("Task not found or no longer available.")
	}
	var p struct {
		agentworksclient.MCPTaskParams
		TaskID         string                       `json:"taskId"`
		InputResponses map[string]mcp.InputResponse `json:"inputResponses"`
	}
	if err := agentworksclient.DecodeMCPTaskParams(params, &p); err != nil {
		return nil, 400, errors.New("Invalid task request.")
	}
	caps := p.ClientCapabilities()
	ctx := server.WithRequestProtocolInfo(r.Context(), &server.RequestProtocolInfo{Modern: true, ProtocolVersion: mcp.ProtocolVersion20260728, ClientCapabilities: caps})
	r = r.WithContext(ctx)
	store, err := openExternalMCPTasks()
	if err != nil {
		return nil, 503, errors.New("The task store is unavailable.")
	}
	defer store.Close()
	var task externalMCPTask
	if method == "tools/call" {
		name, args := p.Name, p.Arguments
		if name == "call_tool" {
			handle, _ := args["guidance_session_id"].(string)
			if handle != "" {
				r = externalWithGuidanceHandle(r, handle)
				ctx = r.Context()
			}
			name, _ = args["name"].(string)
			args, _ = args["arguments"].(map[string]any)
		}
		catalog, err := externalTools()
		if err != nil {
			return nil, 503, err
		}
		var tool externalTool
		found := false
		for _, candidate := range catalog {
			if candidate.Name == name {
				tool, found = candidate, true
				break
			}
		}
		if !found || tool.internalOnly {
			return nil, 400, errors.New("Choose an available job tool.")
		}
		if tool.actions != nil {
			member, rest, err := externalResolveMerged(tool, args)
			if err != nil {
				return nil, 400, err
			}
			name, args = member.Name, rest
		}
		family, ok := agentworksclient.MCPTaskFamilyFor(name)
		if !ok {
			return nil, 400, errors.New("This operation does not produce a task.")
		}
		args = cloneTaskArgs(args)
		args["wait_seconds"] = 0
		rec, err := api.externalMCPInvoke(ctx, r, name, args, true)
		if err != nil {
			return nil, 503, err
		}
		if rec.status < 200 || rec.status >= 300 {
			result := externalMCPDispatchResult(rec)
			result.ResultType = mcp.ResultTypeComplete
			raw, _ := json.Marshal(result)
			return raw, 200, nil
		}
		var started map[string]any
		if json.Unmarshal(rec.body.Bytes(), &started) != nil {
			return mcpTaskToolResult(rec.body.Bytes()), 200, nil
		}
		job, _ := started[family.IDField].(string)
		if job == "" {
			job, _ = args[family.IDField].(string)
		}
		if job == "" {
			return mcpTaskToolResult(rec.body.Bytes()), 200, nil
		}
		workflow, _ := args["workflow_id"].(string)
		now := time.Now().UTC().Truncate(time.Second)
		task = externalMCPTask{ID: uuid.NewString(), Family: family, JobID: job, WorkflowID: workflow, Created: now, Updated: now, Expires: now.Add(crewFunctionSubmissionTTL), Status: "working"}
		encodedFamily, _ := json.Marshal(family)
		_, err = store.db.ExecContext(ctx, `INSERT INTO external_mcp_tasks(id,user_id,connection_id,family,job_id,workflow_id,created_at,updated_at,expires_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(user_id,connection_id,job_id,family) DO NOTHING`, task.ID, user, connection, string(encodedFamily), job, workflow, now.Unix(), now.Unix(), task.Expires.Unix())
		if err != nil {
			return nil, 503, errors.New("The job started, but its task handle could not be saved. Check the project's run history before starting it again.")
		}
		var created, updated, expires int64
		err = store.db.QueryRowContext(ctx, `SELECT id,created_at,updated_at,expires_at FROM external_mcp_tasks WHERE user_id=? AND connection_id=? AND job_id=? AND family=?`, user, connection, job, string(encodedFamily)).Scan(&task.ID, &created, &updated, &expires)
		if err != nil {
			return nil, 503, errors.New("The job started, but its task handle could not be read. Check the project's run history before starting it again.")
		}
		task.Created, task.Updated, task.Expires = time.Unix(created, 0), time.Unix(updated, 0), time.Unix(expires, 0)
		logfWithContext(api.httpRequestLogContext(r), "[MCP_TASK] task=%s event=created tool=%s", task.ID, name)
	} else {
		var family string
		var created, updated, expires int64
		err = store.db.QueryRowContext(ctx, `SELECT id,family,job_id,workflow_id,created_at,updated_at,expires_at,status,ignored_input FROM external_mcp_tasks WHERE id=? AND user_id=? AND connection_id=?`, p.TaskID, user, connection).Scan(&task.ID, &family, &task.JobID, &task.WorkflowID, &created, &updated, &expires, &task.Status, &task.IgnoredInput)
		if err != nil {
			return nil, 404, errors.New("Task not found or no longer available.")
		}
		if json.Unmarshal([]byte(family), &task.Family) != nil {
			return nil, 503, errors.New("The task record is unavailable.")
		}
		task.Created, task.Updated, task.Expires = time.Unix(created, 0), time.Unix(updated, 0), time.Unix(expires, 0)
	}
	// Poll before any update/cancel as well, rechecking current project access.
	rec, err := api.externalMCPInvoke(ctx, r, task.Family.Poll, task.args(), true)
	if err != nil {
		return nil, 503, err
	}
	if rec.status < 200 || rec.status >= 300 {
		return nil, 404, errors.New("Task not found or no longer available.")
	}
	if method == "tasks/update" {
		host := api.externalMCPElicitation(r, taskAllowedTools(r))
		form := host.MaybeElicit(ctx, task.Family.Poll, task.args(), rec.body.Bytes())
		if form == nil {
			return nil, 409, errors.New("This task has no answerable question for this client.")
		}
		request := mcp.CallToolRequest{}
		request.Params.RequestState, request.Params.InputResponses = form.RequestState, p.InputResponses
		result := host.Resume(ctx, task.Family.Poll, task.args(), request)
		if result.IsError {
			return nil, 409, errors.New("The answer could not be accepted. Read the task again to check its current question.")
		}
		for key := range p.InputResponses {
			if response := server.ElicitationResponse(p.InputResponses, key); response != nil && response.Action != mcp.ElicitationResponseActionAccept {
				task.IgnoredInput = key
			}
		}
		rec, err = api.externalMCPInvoke(ctx, r, task.Family.Poll, task.args(), true)
		if err != nil || rec.status >= 300 {
			return nil, 404, errors.New("Task not found or no longer available.")
		}
	} else if method == "tasks/cancel" {
		if task.Family.Cancel == "" {
			return nil, 409, errors.New("This entry point does not support stopping the job. It is still running; use its project controls.")
		}
		cancelArgs := task.args()
		delete(cancelArgs, "compact")
		delete(cancelArgs, "wait_seconds")
		cancelled, err := api.externalMCPInvoke(ctx, r, task.Family.Cancel, cancelArgs, true)
		if err != nil || cancelled.status >= 300 {
			return nil, 404, errors.New("Task not found or no longer available.")
		}
		rec, err = api.externalMCPInvoke(ctx, r, task.Family.Poll, task.args(), true)
		if err != nil || rec.status >= 300 {
			return nil, 404, errors.New("Task not found or no longer available.")
		}
	} else if method != "tools/call" && method != "tasks/get" {
		return nil, 400, errors.New("Unknown task method.")
	}
	var status struct {
		Status     string `json:"status"`
		TurnStatus string `json:"turn_status"`
		Working    *bool  `json:"working"`
	}
	_ = json.Unmarshal(rec.body.Bytes(), &status)
	state := status.Status
	if state == "" {
		state = status.TurnStatus
	}
	view := task.view("complete")
	task.Status = "working"
	switch state {
	case "completed", "complete", "succeeded", "success", "answered":
		task.Status = "completed"
		view["result"] = json.RawMessage(mcpTaskToolResult(rec.body.Bytes()))
	case "failed", "error":
		task.Status = "failed"
		view["error"] = map[string]any{"code": -32603, "message": "The job failed. Read its project execution logs for details."}
	case "cancelled", "canceled", "stopped":
		task.Status = "cancelled"
	}
	if task.Status == "working" {
		host := api.externalMCPElicitation(r, taskAllowedTools(r))
		if form := host.MaybeElicit(ctx, task.Family.Poll, task.args(), rec.body.Bytes()); form != nil {
			requests := map[string]mcp.InputRequest{}
			for key, input := range form.InputRequests {
				if key != task.IgnoredInput {
					requests[key] = input
				}
			}
			if len(requests) > 0 {
				task.Status = "input_required"
				view["inputRequests"] = requests
			}
		}
	}
	if method == "tools/call" {
		view["resultType"] = "task"
		delete(view, "result")
		delete(view, "error")
		delete(view, "inputRequests")
		task.Status = "working"
	}
	if task.Status != view["status"] {
		task.Updated = time.Now().UTC()
	}
	view["status"], view["lastUpdatedAt"] = task.Status, task.Updated.UTC().Format(time.RFC3339Nano)
	_, err = store.db.ExecContext(ctx, `UPDATE external_mcp_tasks SET status=?,updated_at=?,ignored_input=? WHERE id=? AND user_id=? AND connection_id=?`, task.Status, task.Updated.Unix(), task.IgnoredInput, task.ID, user, connection)
	if err != nil {
		return nil, 503, errors.New("The task state could not be saved. Try reading it again.")
	}
	if method != "tools/call" {
		logfWithContext(api.httpRequestLogContext(r), "[MCP_TASK] task=%s event=%s status=%s", task.ID, method, task.Status)
	}
	raw, _ := json.Marshal(view)
	return raw, 200, nil
}

func cloneTaskArgs(args map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range args {
		out[k] = v
	}
	return out
}
func taskAllowedTools(r *http.Request) map[string]externalTool {
	out := map[string]externalTool{}
	catalog, _ := externalTools()
	for _, tool := range catalog {
		if externalTokenAllows(GetUserFromContext(r.Context()), tool) {
			out[tool.Name] = tool
		}
	}
	return out
}

// The SDK handles all ordinary methods. The current Tasks extension is
// intercepted here because the SDK's task support is the older core dialect.
func (api *StreamingAPI) externalMCPTaskHTTP(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, agentworksclient.MaxBodyBytes))
	if err != nil {
		externalError(w, 400, "invalid_request", "Invalid MCP request.")
		return true
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	request, handled := agentworksclient.DecodeMCPTaskRequest(body)
	if !handled {
		return false
	}
	raw, status, err := api.externalMCPTaskOperation(r, request.Method, request.Params)
	var failure error
	if err != nil {
		failure = &agentworksclient.APIError{Status: status, Message: err.Error()}
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(agentworksclient.MCPTaskResponse(request, raw, failure))
	return true
}
