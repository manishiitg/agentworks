package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
)

type crewTeamJoinLogKey struct{}

func crewTeamJoinRequestID(ctx context.Context) string {
	id, _ := ctx.Value(crewTeamJoinLogKey{}).(string)
	return id
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
