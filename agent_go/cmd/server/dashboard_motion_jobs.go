package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

var dashboardMotionRuntimeID = uuid.NewString()

type dashboardMotionJob struct {
	ID     string          `json:"export_id"`
	Status string          `json:"status"`
	Caller string          `json:"created_by"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func openDashboardMotionStore() (*mcpOAuthStore, error) {
	s, err := openMCPOAuthStore()
	if err != nil {
		return nil, err
	}
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS dashboard_motion_exports (
id TEXT PRIMARY KEY, user_id TEXT NOT NULL, workspace TEXT NOT NULL, submission_id TEXT NOT NULL,
composition TEXT NOT NULL, runtime_id TEXT NOT NULL, status TEXT NOT NULL, result TEXT NOT NULL DEFAULT '',
error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, UNIQUE(user_id,workspace,submission_id))`)
	if err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// A durable receipt outlives the HTTP request. Status always rechecks the
// caller's current project/connection access in the shared dashboard service.
func (api *StreamingAPI) dashboardMotionOperation(w http.ResponseWriter, r *http.Request, target dashboardTarget, composition, exportID, submission string) {
	claims := GetUserFromContext(r.Context())
	if !dashboardAuthoringAllowed(claims, &target) {
		externalError(w, 403, "forbidden", "Ask this project's Builder to export the animation, or download an export they have already shared.")
		return
	}
	store, err := openDashboardMotionStore()
	if err != nil {
		dashboardError(w, err)
		return
	}
	defer store.Close()
	if exportID != "" {
		var job dashboardMotionJob
		var raw, runtimeID string
		err = store.db.QueryRowContext(r.Context(), `SELECT id,status,user_id,result,error,runtime_id FROM dashboard_motion_exports WHERE id=? AND user_id=? AND workspace=?`, exportID, claims.UserID, target.Root).Scan(&job.ID, &job.Status, &job.Caller, &raw, &job.Error, &runtimeID)
		if errors.Is(err, sql.ErrNoRows) {
			externalError(w, 404, "not_found", "This export is unavailable. Ask the Builder to start a new export.")
			return
		}
		if err != nil {
			dashboardError(w, err)
			return
		}
		if job.Status == "running" && runtimeID != dashboardMotionRuntimeID {
			job.Status, job.Error = "failed", "The server restarted before this export finished. Start a new export."
		}
		if raw != "" {
			job.Result = json.RawMessage(raw)
		}
		externalJSON(w, job)
		return
	}
	if !validDashboardMotionPath(composition) || len(submission) == 0 || len(submission) > 128 || strings.TrimSpace(submission) != submission {
		externalError(w, 400, "invalid_arguments", "To export, pass composition_path under code/motion/ and a unique submission_id. To check an existing export, pass its export_id.")
		return
	}
	id := uuid.NewString()
	if _, err := dashboardMotionGuard(claims, target, composition, id); err != nil {
		externalError(w, 403, "protected_path", err.Error())
		return
	}
	insert, err := store.db.ExecContext(r.Context(), `INSERT INTO dashboard_motion_exports(id,user_id,workspace,submission_id,composition,runtime_id,status,created_at) VALUES(?,?,?,?,?,?,'running',?) ON CONFLICT(user_id,workspace,submission_id) DO NOTHING`, id, claims.UserID, target.Root, submission, composition, dashboardMotionRuntimeID, time.Now().Unix())
	if err != nil {
		dashboardError(w, err)
		return
	}
	count, err := insert.RowsAffected()
	if err != nil {
		dashboardError(w, err)
		return
	}
	if count == 0 {
		var prior string
		if err := store.db.QueryRowContext(r.Context(), `SELECT id,composition FROM dashboard_motion_exports WHERE user_id=? AND workspace=? AND submission_id=?`, claims.UserID, target.Root, submission).Scan(&id, &prior); err != nil {
			dashboardError(w, err)
			return
		}
		if prior != composition {
			externalError(w, 409, "conflict", "This submission_id was already used for a different animation. Use a new one.")
			return
		}
		api.dashboardMotionOperation(w, r, target, "", id, "")
		return
	}
	// Keep the authenticated context and trusted shell restrictions, without the
	// proxy request's lifetime. The render has its own ten-minute deadline.
	jobCtx := context.WithoutCancel(r.Context())
	go api.runDashboardMotionJob(jobCtx, claims, target, composition, id)
	externalJSON(w, dashboardMotionJob{ID: id, Status: "running", Caller: claims.UserID})
}

func (api *StreamingAPI) runDashboardMotionJob(ctx context.Context, claims *UserClaims, target dashboardTarget, composition, id string) {
	result, err := api.renderDashboardMotion(ctx, claims, target, composition, id)
	status, detail := "completed", ""
	if err != nil {
		status, detail = "failed", err.Error()
	}
	raw := ""
	if result != nil {
		if data, e := json.Marshal(result); e == nil {
			raw = string(data)
		}
	}
	store, e := openDashboardMotionStore()
	if e != nil {
		log.Printf("[DASHBOARD_MOTION_EXPORT] export=%s status_store_error=%v", id, e)
		return
	}
	defer store.Close()
	if _, e = store.db.Exec(`UPDATE dashboard_motion_exports SET status=?,result=?,error=? WHERE id=?`, status, raw, detail, id); e != nil {
		log.Printf("[DASHBOARD_MOTION_EXPORT] export=%s status_update_error=%v", id, e)
	}
}
