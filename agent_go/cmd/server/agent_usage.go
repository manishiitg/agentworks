package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentusage"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/productpolicy"
	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
)

const agentUsageRetention = 90 * 24 * time.Hour
const agentUsageOwnerCap = 250000

var agentUsageCleanupOnce sync.Once

func startAgentUsageCleanup() {
	if testing.Testing() {
		return
	}
	agentUsageCleanupOnce.Do(func() {
		go func() {
			ticker := time.NewTicker(time.Hour)
			defer ticker.Stop()
			for range ticker.C {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				db, err := openAgentUsageDB()
				if err == nil {
					_, _ = db.ExecContext(ctx, "DELETE FROM agent_usage_readings WHERE at<?", time.Now().Add(-agentUsageRetention).UnixNano())
					_, _ = db.ExecContext(ctx, "DELETE FROM agent_usage_closed WHERE at<?", time.Now().Add(-agentUsageRetention).UnixNano())
					_ = db.Close()
				}
				cancel()
			}
		}()
	})
}

type usageState struct {
	agentusage.Current
	Available     bool                `json:"available"`
	Reason        string              `json:"reason,omitempty"`
	Today         agentusage.Counters `json:"today"`
	TodayTimezone string              `json:"today_timezone"`
}

// Usage is server-private state, outside workspace files. It is never part of
// the shared named-agent object or any message/offer/admin projection.
func openAgentUsageDB() (*sql.DB, error) {
	base, err := mcpOAuthSecret()
	if err != nil {
		return nil, err
	}
	p := strings.TrimSuffix(base, ".mcp-oauth.sqlite") + ".agent-usage.sqlite"
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return nil, err
	}
	if st, e := os.Lstat(p); e == nil && !st.Mode().IsRegular() {
		return nil, errors.New("unsafe usage store")
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	err = f.Chmod(0600)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteopen.DSN(p)+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS agent_usage_current(owner TEXT NOT NULL,agent TEXT NOT NULL,body TEXT NOT NULL,PRIMARY KEY(owner,agent));
CREATE TABLE IF NOT EXISTS agent_usage_closed(owner TEXT NOT NULL,agent TEXT NOT NULL,generation TEXT NOT NULL,at INTEGER NOT NULL,PRIMARY KEY(owner,agent,generation));
CREATE TABLE IF NOT EXISTS agent_usage_deleted(owner TEXT PRIMARY KEY,at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS agent_usage_readings(id INTEGER PRIMARY KEY AUTOINCREMENT,owner TEXT NOT NULL,agent TEXT NOT NULL,reading_id TEXT NOT NULL,at INTEGER NOT NULL,input_delta INTEGER NOT NULL,output_delta INTEGER NOT NULL,cached_delta INTEGER NOT NULL,body TEXT NOT NULL,UNIQUE(owner,reading_id));
CREATE INDEX IF NOT EXISTS agent_usage_readings_owner_time ON agent_usage_readings(owner,at,id);`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	startAgentUsageCleanup()
	return db, nil
}

func usageSourceOK(s string) bool {
	return s == "Claude transcript usage" || s == "Codex session total" || s == "Muse provider attribution" || s == "Pi getSessionStats"
}

func (api *StreamingAPI) handleAgentUsageReport(w http.ResponseWriter, r *http.Request) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.AccessToken == nil || claims.ExecutionPrincipal != nil || claims.Scope != "" || claims.BotRouteGrant != "" || claims.ExternalBuilderOperationID != "" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+cliOAuthAccessPrefix) || !namedAgentOperationAllowed(claims, true) || !productpolicy.Enabled("bullpen") || !userAllowedProduct(claims, "bullpen") {
		externalError(w, 403, "access_denied", "Usage reporting is unavailable.")
		return
	}
	var req struct {
		Action    string             `json:"action"`
		Reading   agentusage.Reading `json:"reading"`
		Available bool               `json:"available"`
		Reason    string             `json:"reason"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if json.NewDecoder(r.Body).Decode(&req) != nil || (req.Action != "sample" && req.Action != "off") {
		externalError(w, 400, "invalid_arguments", "Invalid usage report.")
		return
	}
	x := req.Reading
	if _, e := uuid.Parse(x.ID); e != nil && req.Action == "sample" {
		externalError(w, 400, "invalid_arguments", "Invalid reading identity.")
		return
	}
	if x.Native.SkippedRecords < 0 || x.Native.SkippedRecords > agentusage.MaxTokens {
		externalError(w, 400, "invalid_arguments", "Invalid native coverage.")
		return
	}
	if req.Available && (x.Native.Turns != nil && (*x.Native.Turns < 0 || *x.Native.Turns > agentusage.MaxTokens) || x.Native.Calls != nil && (*x.Native.Calls < 0 || *x.Native.Calls > agentusage.MaxTokens)) {
		externalError(w, 400, "invalid_arguments", "Invalid native counts.")
		return
	}
	now := time.Now().UTC()
	if _, err := uuid.Parse(x.Generation); err != nil || x.Sequence < 1 || x.At.After(now.Add(5*time.Minute)) || x.At.Before(now.Add(-agentUsageRetention)) || req.Available && (!usageSourceOK(x.Native.Source) || !x.Native.Counters.Valid() || x.Native.Model != agentusage.ModelID(x.Native.Model)) {
		externalError(w, 400, "invalid_arguments", "Invalid native counters or reading window.")
		return
	}
	if req.Reason != "" && req.Reason != "unavailable" && req.Reason != "unsupported" && req.Reason != "cap_reached" {
		externalError(w, 400, "invalid_arguments", "Invalid usage availability.")
		return
	}
	// Keep lease validation and the committed usage mutation under the same
	// message lock. A simultaneous name move/revoke cannot admit an old report.
	agentMessageMu.Lock()
	defer agentMessageMu.Unlock()
	store, err := readAgentMessageStore(r.Context())
	if err != nil {
		externalError(w, 503, "usage_unavailable", "Usage storage unavailable.")
		return
	}
	var agent *namedMessageAgent
	for i := range store.Agents {
		a := &store.Agents[i]
		if a.ID == x.AgentID && namedAgentConnectionAllowed(*a, claims) && a.Attachment != nil && a.Attachment.ID == x.AttachmentID && a.Attachment.ConnectionID == claims.AccessToken.ID {
			agent = a
			break
		}
	}
	if agent != nil && req.Available {
		sources := map[string]string{"codex": "Codex session total", "claude": "Claude transcript usage", "muse": "Muse provider attribution", "pi": "Pi getSessionStats"}
		if sources[agent.Attachment.Provider] != x.Native.Source {
			externalError(w, 400, "invalid_arguments", "Usage source does not match the attached provider.")
			return
		}
	}
	if agent == nil {
		externalError(w, 403, "access_denied", "Attachment unavailable or access denied.")
		return
	}
	db, err := openAgentUsageDB()
	if err != nil {
		externalError(w, 503, "usage_unavailable", "Usage storage unavailable.")
		return
	}
	defer db.Close()
	tx, err := db.BeginTx(r.Context(), nil)
	if err != nil {
		externalError(w, 503, "usage_unavailable", "Usage storage unavailable.")
		return
	}
	defer tx.Rollback()
	owner := claims.UserID
	if _, err = tx.ExecContext(r.Context(), "DELETE FROM agent_usage_readings WHERE at < ?", now.Add(-agentUsageRetention).UnixNano()); err != nil {
		externalError(w, 503, "usage_unavailable", "Usage retention unavailable.")
		return
	}
	_, err = tx.ExecContext(r.Context(), "DELETE FROM agent_usage_closed WHERE at < ?", now.Add(-agentUsageRetention).UnixNano())
	if err != nil {
		externalError(w, 503, "usage_unavailable", "Usage storage unavailable.")
		return
	}
	var floor int64
	err = tx.QueryRowContext(r.Context(), "SELECT at FROM agent_usage_deleted WHERE owner=?", owner).Scan(&floor)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		externalError(w, 503, "usage_unavailable", "Usage storage unavailable.")
		return
	}
	if req.Action == "sample" && x.At.UnixNano() <= floor {
		externalError(w, 409, "usage_deleted", "Reading predates history deletion; establish a new baseline.")
		return
	}
	var old usageState
	var body string
	err = tx.QueryRowContext(r.Context(), "SELECT body FROM agent_usage_current WHERE owner=? AND agent=?", owner, x.AgentID).Scan(&body)
	if err == nil {
		err = json.Unmarshal([]byte(body), &old)
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		externalError(w, 503, "usage_unavailable", "Usage state unavailable.")
		return
	}
	var closed int
	err = tx.QueryRowContext(r.Context(), "SELECT 1 FROM agent_usage_closed WHERE owner=? AND agent=? AND generation=?", owner, x.AgentID, x.Generation).Scan(&closed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		externalError(w, 503, "usage_unavailable", "Usage storage unavailable.")
		return
	}
	err = nil
	if closed == 1 && req.Action == "sample" {
		externalError(w, 409, "usage_disabled", "Reporting generation is closed.")
		return
	}
	if req.Action == "off" {
		_, err = tx.ExecContext(r.Context(), "INSERT OR IGNORE INTO agent_usage_closed VALUES(?,?,?,?)", owner, x.AgentID, x.Generation, now.UnixNano())
		if err == nil && old.Generation == x.Generation {
			old.Reporting = false
			old.Available = false
			old.Native = agentusage.Native{}
			old.Delta = agentusage.Counters{}
			old.Today = agentusage.Counters{}
			b, _ := json.Marshal(old)
			_, err = tx.ExecContext(r.Context(), "UPDATE agent_usage_current SET body=? WHERE owner=? AND agent=?", string(b), owner, x.AgentID)
		}
	} else {
		if old.Generation == x.Generation && x.Sequence <= old.Sequence {
			if x.Sequence == old.Sequence && x.ID == old.ID {
				original, _ := json.Marshal(old.Native)
				repeated, _ := json.Marshal(x.Native)
				if string(original) != string(repeated) || old.Available != req.Available || !old.At.Equal(x.At) || old.Reason != req.Reason {
					externalError(w, 409, "usage_id_reused", "Reading ID was reused with different native numbers.")
					return
				}
				externalJSON(w, map[string]bool{"stored": true})
				return
			}
			externalError(w, 409, "stale_usage", "Stale reading sequence.")
			return
		}
		if old.Generation != x.Generation || old.AttachmentID != x.AttachmentID {
			if !x.Baseline && old.Generation != "" {
				externalError(w, 409, "usage_baseline_required", "Establish a new native baseline.")
				return
			}
			if old.Generation != "" {
				_, err = tx.ExecContext(r.Context(), "INSERT OR IGNORE INTO agent_usage_closed VALUES(?,?,?,?)", owner, x.AgentID, old.Generation, now.UnixNano())
			}
		}
		x.Delta = agentusage.Counters{}
		x.Since = x.At
		if req.Available && old.Reporting && old.Available && old.Generation == x.Generation && old.AttachmentID == x.AttachmentID && old.At.UnixNano() > floor && !x.Baseline {
			var ok bool
			x.Delta, ok = x.Native.Counters.Difference(old.Native.Counters)
			x.Discontinuity = !ok
			if !ok {
				x.Delta = agentusage.Counters{}
			}
			x.Since = old.At
		} else {
			x.Baseline = true
		}
		current := usageState{Current: agentusage.Current{Reading: x, Reporting: true}, Available: req.Available, Reason: req.Reason}
		if err == nil && req.Available {
			var count int
			err = tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM agent_usage_readings WHERE owner=?", owner).Scan(&count)
			if err == nil && count >= agentUsageOwnerCap {
				externalError(w, 429, "usage_cap_reached", "Usage history record cap reached; delete history to continue.")
				return
			}
			if err == nil {
				b, _ := json.Marshal(x)
				_, err = tx.ExecContext(r.Context(), "INSERT INTO agent_usage_readings(owner,agent,reading_id,at,input_delta,output_delta,cached_delta,body) VALUES(?,?,?,?,?,?,?,?)", owner, x.AgentID, x.ID, x.At.UnixNano(), x.Delta.Input, x.Delta.Output, x.Delta.Cached, string(b))
			}
		}
		if err == nil {
			b, _ := json.Marshal(current)
			_, err = tx.ExecContext(r.Context(), "INSERT INTO agent_usage_current VALUES(?,?,?) ON CONFLICT(owner,agent) DO UPDATE SET body=excluded.body", owner, x.AgentID, string(b))
		}
	}
	if err != nil || tx.Commit() != nil {
		externalError(w, 503, "usage_unavailable", "Usage report could not be stored.")
		return
	}
	externalJSON(w, map[string]bool{"stored": true})
}

// Owner-only hydration is deliberately separate from shared agent structs.
func ownerAgentUsage(ctx context.Context, owner, zone string) (map[string]usageState, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	db, err := openAgentUsageDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT agent,body FROM agent_usage_current WHERE owner=?", owner)
	if err != nil {
		return nil, err
	}
	out := map[string]usageState{}
	for rows.Next() {
		var id, b string
		if err = rows.Scan(&id, &b); err != nil {
			break
		}
		var s usageState
		if err = json.Unmarshal([]byte(b), &s); err != nil {
			break
		}
		s.TodayTimezone = loc.String()
		out[id] = s
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = db.QueryContext(ctx, "SELECT agent,SUM(input_delta),SUM(output_delta),SUM(cached_delta) FROM agent_usage_readings WHERE owner=? AND at>=? GROUP BY agent", owner, midnight.UnixNano())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var c agentusage.Counters
		if err = rows.Scan(&id, &c.Input, &c.Output, &c.Cached); err != nil {
			return nil, err
		}
		if s, ok := out[id]; ok {
			s.Today = c
			out[id] = s
		}
	}
	return out, rows.Err()
}

func (api *StreamingAPI) handleBullpenUsage(w http.ResponseWriter, r *http.Request) {
	claims := bullpenBrowserClaims(w, r, true)
	if claims == nil {
		return
	}
	db, err := openAgentUsageDB()
	if err != nil {
		externalError(w, 503, "usage_unavailable", "Usage history unavailable.")
		return
	}
	defer db.Close()
	now := time.Now().UTC()
	if r.Method == http.MethodDelete {
		tx, e := db.BeginTx(r.Context(), nil)
		if e != nil {
			externalError(w, 503, "usage_unavailable", "Usage deletion unavailable.")
			return
		}
		defer tx.Rollback()
		_, e = tx.ExecContext(r.Context(), "INSERT INTO agent_usage_deleted VALUES(?,?) ON CONFLICT(owner) DO UPDATE SET at=excluded.at", claims.UserID, now.UnixNano())
		if e == nil {
			_, e = tx.ExecContext(r.Context(), "DELETE FROM agent_usage_readings WHERE owner=?", claims.UserID)
		}
		if e == nil {
			_, e = tx.ExecContext(r.Context(), "DELETE FROM agent_usage_current WHERE owner=?", claims.UserID)
		}
		if e != nil || tx.Commit() != nil {
			externalError(w, 503, "usage_unavailable", "History deletion failed; try again.")
			return
		}
		externalJSON(w, map[string]bool{"deleted": true})
		return
	}
	_, err = db.ExecContext(r.Context(), "DELETE FROM agent_usage_readings WHERE at<?", now.Add(-agentUsageRetention).UnixNano())
	if err != nil {
		externalError(w, 503, "usage_unavailable", "Usage history unavailable.")
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	if before <= 0 {
		before = 1 << 62
	}
	rows, err := db.QueryContext(r.Context(), "SELECT id,body FROM agent_usage_readings WHERE owner=? AND id<? ORDER BY id DESC LIMIT 501", claims.UserID, before)
	if err != nil {
		externalError(w, 503, "usage_unavailable", "Usage history unavailable.")
		return
	}
	defer rows.Close()
	items := []agentusage.Reading{}
	var next int64
	more := false
	for rows.Next() {
		var id int64
		var b string
		if rows.Scan(&id, &b) != nil {
			externalError(w, 503, "usage_unavailable", "Usage history unavailable.")
			return
		}
		if len(items) == 500 {
			more = true
			break
		}
		var x agentusage.Reading
		if json.Unmarshal([]byte(b), &x) != nil {
			externalError(w, 503, "usage_unavailable", "Usage history unavailable.")
			return
		}
		items = append(items, x)
		next = id
	}
	if rows.Err() != nil {
		externalError(w, 503, "usage_unavailable", "Usage history unavailable.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	externalJSON(w, map[string]any{"readings": items, "before": next, "has_more": more, "retention_days": 90, "row_cap": agentUsageOwnerCap})
}

type agentUsageTimezoneKey struct{}

func withAgentUsageTimezone(ctx context.Context, zone string) context.Context {
	if _, err := time.LoadLocation(zone); err != nil {
		zone = "UTC"
	}
	return context.WithValue(ctx, agentUsageTimezoneKey{}, zone)
}

func hydrateOwnerUsage(ctx context.Context, owner string, items []map[string]interface{}, agents []namedMessageAgent) {
	claims := GetUserFromContext(ctx)
	if claims == nil || claims.UserID != owner || claims.ExecutionPrincipal != nil || claims.Scope != "" || claims.BotRouteGrant != "" || claims.ExternalBuilderOperationID != "" || claims.Provider == "bot_owner" || claims.Provider == "bot_route" || !productpolicy.Enabled("bullpen") {
		return
	}
	zone, _ := ctx.Value(agentUsageTimezoneKey{}).(string)
	if zone == "" {
		zone = "UTC"
	}
	usage, err := ownerAgentUsage(ctx, owner, zone)
	attachments := map[string]string{}
	for _, a := range agents {
		if a.UserID == owner && a.Attachment != nil {
			attachments[a.ID] = a.Attachment.ID
		}
	}
	for _, item := range items {
		id, _ := item["agent_id"].(string)
		if err != nil {
			item["usage_unavailable"] = true
			continue
		}
		if u, ok := usage[id]; ok && u.AttachmentID == attachments[id] {
			item["usage"] = u
		}
	}
}
