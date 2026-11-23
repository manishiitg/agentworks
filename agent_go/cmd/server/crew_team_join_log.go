package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"regexp"

	"github.com/google/uuid"
)

// crewTeamIdentifier is the shape of a Team project or request ID that may be written to a log line.
var crewTeamIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

type crewTeamJoinLogKey struct{}

func crewTeamJoinRequestID(ctx context.Context) string {
	id, _ := ctx.Value(crewTeamJoinLogKey{}).(string)
	return id
}

// Mac and MCP accept share the same ID-only start/finish receipt, including
// refusals. Neither response contents nor a caller-supplied invalid ID is logged.
func startCrewTeamJoin(w http.ResponseWriter, r *http.Request, project string) (http.ResponseWriter, *http.Request, func()) {
	request := uuid.NewString()
	capture := &crewTeamJoinResponse{ResponseWriter: w}
	logProject := ""
	if crewTeamIdentifier.MatchString(project) {
		logProject = project
	}
	log.Printf("[TEAM_JOIN] request_id=%s project=%s event=started", request, logProject)
	r = r.WithContext(context.WithValue(r.Context(), crewTeamJoinLogKey{}, request))
	return capture, r, func() { capture.finish(request, logProject) }
}

// Keep the error code for the final receipt, never error text, instructions or
// a response body in logs. This endpoint always returns one JSON response.
type crewTeamJoinResponse struct {
	http.ResponseWriter
	status int
	reason string
}

func (w *crewTeamJoinResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *crewTeamJoinResponse) Write(raw []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.status >= 400 {
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &body) == nil {
			w.reason = body.Error.Code
		}
	}
	return w.ResponseWriter.Write(raw)
}

func (w *crewTeamJoinResponse) finish(request, project string) {
	result, reason := "done", "none"
	if w.status >= 400 || w.status == 0 {
		result = "failed"
		reason = w.reason
		if reason == "" {
			reason = "incomplete_response"
		}
	}
	log.Printf("[TEAM_JOIN] request_id=%s project=%s event=finished outcome=%s status=%d reason=%s", request, project, result, w.status, reason)
}

func crewTeamLogPrivateSkill(b *crewTeamLocal, rel string) {
	event := "skill_upload_skipped"
	if rel == "skills" {
		event = "skill_scope_skipped"
	}
	ref := crewTransferDigest(rel)
	log.Printf("[TEAM_JOIN] project=%s client=%s event=%s skill_ref=%s reason=not_team_skill_id", b.Project, b.Client, event, ref[:12])
}
