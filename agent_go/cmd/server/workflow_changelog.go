package server

import (
	"encoding/json"
	"log"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// planChangelogFieldChange mirrors PlanFieldChange written by the planning agent
// (planning_agent.go). Defined locally so the server package stays decoupled from
// the orchestrator package; the JSON tags are the contract.
type planChangelogFieldChange struct {
	StepID   string      `json:"step_id"`
	Field    string      `json:"field"`
	OldValue interface{} `json:"old_value"`
	NewValue interface{} `json:"new_value"`
}

// planChangelogEntry mirrors PlanChangelogEntry. One entry per successful
// plan-mod tool call, appended to planning/changelog/changelog-*.json.
type planChangelogEntry struct {
	Timestamp      string                       `json:"timestamp"`
	Tool           string                       `json:"tool"`
	Reason         string                       `json:"reason"`
	StepIDs        []string                     `json:"step_ids,omitempty"`
	Changes        []planChangelogFieldChange   `json:"changes,omitempty"`
	ArtifactReview *planChangelogArtifactReview `json:"artifact_review,omitempty"`
	Actor          string                       `json:"actor,omitempty"`
	Origin         planChangelogOrigin          `json:"origin,omitempty"`
	// Source changelog file this entry came from (added by the server, not on disk).
	File string `json:"file,omitempty"`
}

type planChangelogOrigin struct {
	Type      string `json:"type,omitempty"`
	AgentName string `json:"agent_name,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	UserID    string `json:"user_id,omitempty"`
	Username  string `json:"username,omitempty"`
}

type planChangelogArtifactReview struct {
	Done          bool   `json:"done"`
	ReviewedAt    string `json:"reviewed_at,omitempty"`
	ReviewedBy    string `json:"reviewed_by,omitempty"`
	Result        string `json:"result,omitempty"`
	ReportEntryID string `json:"report_entry_id,omitempty"`
}

type planChangelogFile struct {
	Entries []planChangelogEntry `json:"entries"`
}

// PlanChangelogResponse is the JSON shape of GET /api/workflow/plan-changelog.
type PlanChangelogResponse struct {
	Success bool                 `json:"success"`
	Entries []planChangelogEntry `json:"entries"`
	Count   int                  `json:"count"`
	Error   string               `json:"error,omitempty"`
}

// planChangelogMaxEntries caps how many entries the feed returns so a long-lived
// workflow doesn't ship thousands of rows to the History view in one response.
const planChangelogMaxEntries = 300

// handleGetPlanChangelog serves the merged, newest-first plan-edit audit trail
// from planning/changelog/*.json — the granular "Plan edits" feed of the
// History view. Each entry carries the agent's mandatory reason plus per-field
// old→new diffs.
func (api *StreamingAPI) handleGetPlanChangelog(w http.ResponseWriter, r *http.Request) {
	if !setupCORS(w, r, http.MethodGet) {
		return
	}
	workspacePath, ok := requireWorkspacePath(w, r)
	if !ok {
		return
	}
	if !requireWorkflowVisible(w, r, workspacePath) {
		return
	}

	folder := path.Join(strings.Trim(workspacePath, "/"), "planning", "changelog")
	listing, exists, err := listWorkspaceFolder(r.Context(), folder, 1)
	if err != nil {
		writeAIJSON(w, PlanChangelogResponse{Success: false, Error: err.Error()})
		return
	}
	if !exists {
		// No changelog folder yet — an empty feed is a valid, non-error state.
		writeAIJSON(w, PlanChangelogResponse{Success: true, Entries: []planChangelogEntry{}, Count: 0})
		return
	}

	var filePaths []string
	collectWorkspaceFilePaths(listing, &filePaths)

	entries := make([]planChangelogEntry, 0, 64)
	for _, fullPath := range filePaths {
		if !strings.HasSuffix(strings.ToLower(fullPath), ".json") {
			continue
		}
		content, fileExists, readErr := readFileFromWorkspace(r.Context(), fullPath)
		if readErr != nil || !fileExists || strings.TrimSpace(content) == "" {
			continue
		}
		var parsed planChangelogFile
		if err := json.Unmarshal([]byte(content), &parsed); err != nil {
			continue // skip malformed changelog files rather than failing the whole feed
		}
		fileName := path.Base(fullPath)
		for _, entry := range parsed.Entries {
			entry.File = fileName
			entries = append(entries, entry)
		}
	}

	// Newest first. Timestamps are RFC3339 UTC, so a lexical descending sort
	// is chronological.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Timestamp > entries[j].Timestamp
	})

	// Optional ?limit=N (1..planChangelogMaxEntries) lets change-detection
	// callers fetch just the head entry instead of the full feed.
	maxEntries := planChangelogMaxEntries
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n < maxEntries {
		maxEntries = n
	}

	total := len(entries)
	if len(entries) > maxEntries {
		entries = entries[:maxEntries]
	}

	writeAIJSON(w, PlanChangelogResponse{Success: true, Entries: entries, Count: total})
}

// handlePrunePlanChangelog deletes plan-changelog files older than a cutoff —
// the "consolidate" / drop-old-edits action. Each file is named
// changelog-YYYY-MM-DD-HH-MM-SS.json; we delete whole files older than the
// cutoff (the agent can't shell-write planning/, so the server prunes here).
// POST body: {workspace_path, older_than_days}.
func (api *StreamingAPI) handlePrunePlanChangelog(w http.ResponseWriter, r *http.Request) {
	if !setupCORS(w, r, http.MethodPost) {
		return
	}
	var req struct {
		WorkspacePath string `json:"workspace_path"`
		OlderThanDays int    `json:"older_than_days"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	req.WorkspacePath = strings.TrimSpace(req.WorkspacePath)
	if req.WorkspacePath == "" {
		http.Error(w, "workspace_path is required", http.StatusBadRequest)
		return
	}
	if req.OlderThanDays <= 0 {
		http.Error(w, "older_than_days must be > 0", http.StatusBadRequest)
		return
	}

	cutoff := time.Now().AddDate(0, 0, -req.OlderThanDays)
	folder := path.Join(strings.Trim(req.WorkspacePath, "/"), "planning", "changelog")
	listing, exists, err := listWorkspaceFolder(r.Context(), folder, 1)
	if err != nil {
		writeAIJSON(w, map[string]interface{}{"success": false, "error": err.Error()})
		return
	}
	if !exists {
		writeAIJSON(w, map[string]interface{}{"success": true, "deleted": 0})
		return
	}

	var filePaths []string
	collectWorkspaceFilePaths(listing, &filePaths)
	deleted := 0
	for _, fp := range filePaths {
		base := path.Base(fp)
		if !strings.HasPrefix(base, "changelog-") || !strings.HasSuffix(strings.ToLower(base), ".json") {
			continue
		}
		stamp := strings.TrimSuffix(strings.TrimPrefix(base, "changelog-"), ".json")
		t, perr := time.Parse("2006-01-02-15-04-05", stamp)
		if perr != nil {
			continue // unrecognized name — keep it to be safe
		}
		if t.Before(cutoff) {
			if derr := deleteWorkspaceFile(r.Context(), fp); derr != nil {
				log.Printf("[PLAN-CHANGELOG] failed to prune %s: %v", fp, derr)
				continue
			}
			deleted++
		}
	}
	writeAIJSON(w, map[string]interface{}{"success": true, "deleted": deleted})
}
