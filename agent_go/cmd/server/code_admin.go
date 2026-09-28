package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// Admin inspection of Code workspaces (docs/design/code_product.md, "Admin
// inspection"). A product opts in with admin_inspection in its product.yaml;
// Code does, Crew and personal chats do not, so chatHistoryVisibleTo stays
// owner-only for them.
//
// Admins read everyone's Code chats, files and sharing, read-only: these
// endpoints only read, and nothing here resumes a session or writes a file.
// Every call is appended to an audit log that admins can read too.

// codeAdminHiddenTopSegments are never shown to an admin: the Code's HOME
// (git and CLI logins) and dependency trees.
var codeAdminHiddenTopSegments = map[string]bool{".sandbox-cache": true, "node_modules": true, ".git": true}

const (
	codeAdminFileContentCap = 512 << 10
	codeAdminFileTreeCap    = 5000
)

// codeAdminAuditEntry is one admin view.
type codeAdminAuditEntry struct {
	At            string `json:"at"`
	AdminID       string `json:"admin_id"`
	AdminUsername string `json:"admin_username,omitempty"`
	Action        string `json:"action"`
	OwnerID       string `json:"owner_id,omitempty"`
	ProjectID     string `json:"project_id,omitempty"`
	Target        string `json:"target,omitempty"`
}

// codeAdminAuditPath keeps one file per month so the log is never trimmed.
func codeAdminAuditPath(t time.Time) string {
	return "config/code-admin-audit/" + t.UTC().Format("2006-01") + ".jsonl"
}

var codeAdminAuditMu sync.Mutex

// recordCodeAdminView appends one entry. A view that cannot be recorded is
// refused, so no admin view goes unaudited.
func recordCodeAdminView(ctx context.Context, claims *UserClaims, action, ownerID, projectID, target string) error {
	now := time.Now().UTC()
	entry := codeAdminAuditEntry{At: now.Format(time.RFC3339), Action: action, OwnerID: ownerID, ProjectID: projectID, Target: target}
	if claims != nil {
		entry.AdminID, entry.AdminUsername = claims.UserID, claims.Username
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	codeAdminAuditMu.Lock()
	defer codeAdminAuditMu.Unlock()
	file := codeAdminAuditPath(now)
	existing, _, err := readFileFromWorkspace(ctx, file)
	if err != nil {
		return err
	}
	if err := writeFileToWorkspace(ctx, file, existing+string(raw)+"\n"); err != nil {
		return err
	}
	log.Printf("[CODE_ADMIN] %s by %s owner=%s project=%s target=%s", action, entry.AdminID, ownerID, projectID, target)
	return nil
}

func readCodeAdminAudit(ctx context.Context, month time.Time) ([]codeAdminAuditEntry, error) {
	content, exists, err := readFileFromWorkspace(ctx, codeAdminAuditPath(month))
	if err != nil || !exists {
		return []codeAdminAuditEntry{}, err
	}
	entries := []codeAdminAuditEntry{}
	for _, line := range strings.Split(content, "\n") {
		var entry codeAdminAuditEntry
		if strings.TrimSpace(line) != "" && json.Unmarshal([]byte(line), &entry) == nil {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// codeAdminProfile returns the Code profile when this caller is an admin and
// the product has admin inspection on.
func (api *StreamingAPI) codeAdminProfile(w http.ResponseWriter, r *http.Request) (agentprofiles.Profile, *UserClaims, bool) {
	claims := GetUserFromContext(r.Context())
	if claims == nil || !currentUserIsAdmin(r) || api == nil || api.agentProfiles == nil {
		writeWorkflowPermissionDenied(w, "admin")
		return agentprofiles.Profile{}, nil, false
	}
	profile, err := api.agentProfiles.Resolve(codeproduct.ProfileID, 0, claims.UserID)
	if err != nil || !profile.AdminInspection {
		writeAgentProfileError(w, http.StatusNotFound, "admin inspection is not enabled for Code")
		return agentprofiles.Profile{}, nil, false
	}
	return profile, claims, true
}

type codeAdminWorkspace struct {
	OwnerID       string               `json:"owner_id"`
	OwnerUsername string               `json:"owner_username,omitempty"`
	ID            string               `json:"id"`
	Title         string               `json:"title"`
	WorkspacePath string               `json:"workspace_path"`
	UpdatedAt     string               `json:"updated_at,omitempty"`
	Shares        []codeShareGrantView `json:"shares"`
}

// GET /api/admin/code/workspaces — every user's Code workspaces.
func (api *StreamingAPI) handleAdminListCodeWorkspaces(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	profile, claims, ok := api.codeAdminProfile(w, r)
	if !ok {
		return
	}
	if err := recordCodeAdminView(r.Context(), claims, "list_workspaces", "", "", ""); err != nil {
		writeAgentProfileError(w, http.StatusServiceUnavailable, "the admin audit log is unavailable")
		return
	}
	owners := append([]string{sanitizeUserIDForPath(claims.UserID)}, crewProjectOwnerCandidates(claims.UserID)...)
	rows := []codeAdminWorkspace{}
	for _, ownerID := range owners {
		for _, row := range listSharedProjectsForOwner(r.Context(), claims, profile, ownerID) {
			rows = append(rows, codeAdminWorkspace{
				OwnerID: ownerID, OwnerUsername: crewOwnerDisplayName(ownerID), ID: row.ID, Title: row.Title,
				WorkspacePath: row.WorkspacePath, UpdatedAt: row.UpdatedAt,
				Shares: codeSharesView(r.Context(), ownerID, row.ID, codeRoleOwner).Grants,
			})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].OwnerUsername != rows[j].OwnerUsername {
			return rows[i].OwnerUsername < rows[j].OwnerUsername
		}
		return rows[i].UpdatedAt > rows[j].UpdatedAt
	})
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"workspaces": rows})
}

// codeAdminProject resolves {owner}/{project_id} to that owner's Code root.
func (api *StreamingAPI) codeAdminProject(w http.ResponseWriter, r *http.Request) (agentprofiles.Profile, *UserClaims, string, string, string, bool) {
	profile, claims, ok := api.codeAdminProfile(w, r)
	if !ok {
		return profile, nil, "", "", "", false
	}
	vars := mux.Vars(r)
	ownerID := sanitizeUserIDForPath(strings.TrimSpace(vars["owner"]))
	projectID := strings.TrimSpace(vars["project_id"])
	if ownerID == "" || projectID == "" {
		writeAgentProfileError(w, http.StatusNotFound, "Code workspace not found")
		return profile, nil, "", "", "", false
	}
	binding, err := resolveProductProjectBindingWithStore(r.Context(), ownerID, profile, projectID, defaultProductProjectStore())
	if err != nil {
		writeAgentProfileError(w, http.StatusNotFound, "Code workspace not found")
		return profile, nil, "", "", "", false
	}
	return profile, claims, ownerID, projectID, agentProfileRuntimeWorkspace(ownerID, binding.WorkspacePath), true
}

// GET /api/admin/code/workspaces/{owner}/{project_id}/files
func (api *StreamingAPI) handleAdminCodeFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_, claims, ownerID, projectID, root, ok := api.codeAdminProject(w, r)
	if !ok {
		return
	}
	if err := recordCodeAdminView(r.Context(), claims, "list_files", ownerID, projectID, ""); err != nil {
		writeAgentProfileError(w, http.StatusServiceUnavailable, "the admin audit log is unavailable")
		return
	}
	listing, exists, err := listWorkspaceFolder(r.Context(), root, 8)
	if err != nil || !exists {
		writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"files": []sharedProjectFileEntry{}})
		return
	}
	var paths []string
	collectWorkspaceFilePaths(listing, &paths)
	prefix := strings.TrimSuffix(root, "/") + "/"
	entries := []sharedProjectFileEntry{}
	seen := map[string]bool{}
	truncated := false
	for _, full := range paths {
		rel := strings.TrimPrefix(strings.Trim(full, "/"), prefix)
		if rel == strings.Trim(full, "/") || rel == "" || codeAdminHidden(rel) || seen[rel] {
			continue
		}
		if len(entries) >= codeAdminFileTreeCap {
			truncated = true
			break
		}
		seen[rel] = true
		entries = append(entries, sharedProjectFileEntry{Path: rel, Type: "file"})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"files": entries, "truncated": truncated})
}

func codeAdminHidden(rel string) bool {
	top := strings.SplitN(strings.Trim(rel, "/"), "/", 2)[0]
	return codeAdminHiddenTopSegments[top]
}

// GET /api/admin/code/workspaces/{owner}/{project_id}/file?path=<rel>
func (api *StreamingAPI) handleAdminCodeFile(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_, claims, ownerID, projectID, root, ok := api.codeAdminProject(w, r)
	if !ok {
		return
	}
	rel := strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(r.URL.Query().Get("path"))), "/")
	if rel == "" || rel == "." || strings.HasPrefix(rel, "..") || codeAdminHidden(rel) {
		writeAgentProfileError(w, http.StatusNotFound, "file not found")
		return
	}
	if err := recordCodeAdminView(r.Context(), claims, "read_file", ownerID, projectID, rel); err != nil {
		writeAgentProfileError(w, http.StatusServiceUnavailable, "the admin audit log is unavailable")
		return
	}
	content, exists, err := readFileFromWorkspace(r.Context(), strings.TrimSuffix(root, "/")+"/"+rel)
	if err != nil || !exists {
		writeAgentProfileError(w, http.StatusNotFound, "file not found")
		return
	}
	binary := isSharedProjectBinaryContent(content)
	truncated := len(content) > codeAdminFileContentCap
	if binary {
		content = ""
	} else if truncated {
		content = content[:codeAdminFileContentCap]
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"path": rel, "content": content, "binary": binary, "truncated": truncated})
}

type codeAdminChat struct {
	UserID       string `json:"user_id"`
	Username     string `json:"username,omitempty"`
	SessionID    string `json:"session_id"`
	Title        string `json:"title,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	MessageCount int    `json:"message_count"`
}

// codeChatParticipants are the owner and everyone the Code is shared with:
// the only people who can have chats of it.
func codeChatParticipants(ctx context.Context, ownerID, projectID string) []string {
	people := []string{ownerID}
	for _, grant := range codeSharesView(ctx, ownerID, projectID, codeRoleOwner).Grants {
		people = append(people, grant.UserID)
	}
	return people
}

// GET /api/admin/code/workspaces/{owner}/{project_id}/chats — the owner's and
// every sharer's chats of this Code.
func (api *StreamingAPI) handleAdminCodeChats(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_, claims, ownerID, projectID, root, ok := api.codeAdminProject(w, r)
	if !ok {
		return
	}
	if err := recordCodeAdminView(r.Context(), claims, "list_chats", ownerID, projectID, ""); err != nil {
		writeAgentProfileError(w, http.StatusServiceUnavailable, "the admin audit log is unavailable")
		return
	}
	chats := []codeAdminChat{}
	for _, userID := range codeChatParticipants(r.Context(), ownerID, projectID) {
		sessions, err := ListChatHistorySessionsByKind(userID, "", 0, 0, root)
		if err != nil {
			continue
		}
		for _, session := range sessions {
			if !workspacePathsMatchForUser(userID, chatHistorySessionWorkspace(session), root) && !strings.HasPrefix(normalizeConversationWorkspace(session.ConversationPath), normalizeConversationWorkspace(root)+"/") {
				continue
			}
			chats = append(chats, codeAdminChat{UserID: userID, Username: crewOwnerDisplayName(sanitizeUserIDForPath(userID)), SessionID: session.SessionID, Title: session.Title, UpdatedAt: session.UpdatedAt, MessageCount: session.MessageCount})
		}
	}
	sort.SliceStable(chats, func(i, j int) bool { return chats[i].UpdatedAt > chats[j].UpdatedAt })
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"chats": chats})
}

// GET /api/admin/code/workspaces/{owner}/{project_id}/chats/{session_id}?user=<id>
// — one chat, read-only: messages, tool calls and their terminal output.
func (api *StreamingAPI) handleAdminCodeChat(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_, claims, ownerID, projectID, root, ok := api.codeAdminProject(w, r)
	if !ok {
		return
	}
	sessionID := strings.TrimSpace(mux.Vars(r)["session_id"])
	userID := strings.TrimSpace(r.URL.Query().Get("user"))
	if userID == "" {
		userID = ownerID
	}
	allowed := false
	for _, person := range codeChatParticipants(r.Context(), ownerID, projectID) {
		if person == userID {
			allowed = true
		}
	}
	if !allowed || sessionID == "" {
		writeAgentProfileError(w, http.StatusNotFound, "chat not found")
		return
	}
	if err := recordCodeAdminView(r.Context(), claims, "read_chat", ownerID, projectID, userID+"/"+sessionID); err != nil {
		writeAgentProfileError(w, http.StatusServiceUnavailable, "the admin audit log is unavailable")
		return
	}
	data, err := ReadChatHistoryConversation(userID, sessionID, root)
	if err != nil {
		writeAgentProfileError(w, http.StatusNotFound, "chat not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// GET /api/admin/code/audit?month=YYYY-MM — the admin view log. Reading it
// is itself recorded.
func (api *StreamingAPI) handleAdminCodeAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_, claims, ok := api.codeAdminProfile(w, r)
	if !ok {
		return
	}
	month := time.Now().UTC()
	if raw := strings.TrimSpace(r.URL.Query().Get("month")); raw != "" {
		parsed, err := time.Parse("2006-01", raw)
		if err != nil {
			writeAgentProfileError(w, http.StatusBadRequest, "month must be YYYY-MM")
			return
		}
		month = parsed
	}
	if err := recordCodeAdminView(r.Context(), claims, "read_audit", "", "", month.Format("2006-01")); err != nil {
		writeAgentProfileError(w, http.StatusServiceUnavailable, "the admin audit log is unavailable")
		return
	}
	entries, err := readCodeAdminAudit(r.Context(), month)
	if err != nil {
		writeAgentProfileError(w, http.StatusInternalServerError, fmt.Sprintf("read audit log: %v", err))
		return
	}
	writeAgentProfileJSON(w, http.StatusOK, map[string]interface{}{"month": month.Format("2006-01"), "entries": entries})
}
