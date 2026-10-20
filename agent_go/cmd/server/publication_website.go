package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

func deletePublicationState(ctx context.Context, root string, target ...string) error {
	agents, err := publishedAgents(contextWithoutPublication(ctx), root)
	if err != nil {
		return err
	}
	state, err := publicationStateRoot()
	if err != nil {
		return err
	}
	operations, err := filepath.Glob(filepath.Join(state, "uploads", "*", "operation.json"))
	if err != nil {
		return err
	}
	revisions := map[string]bool{}
	for _, p := range agents {
		revisions[p.Revision] = true
	}
	for _, file := range operations {
		raw, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		var upload publicationUpload
		if json.Unmarshal(raw, &upload) != nil || upload.Record == nil {
			continue
		}
		matches := canonicalCrewWorkspaceRoot(upload.Record.Root) == canonicalCrewWorkspaceRoot(root)
		if len(target) == 2 && upload.ProfileID == target[0] && (upload.WorkspaceID == target[1] || upload.NewWorkspaceID == target[1]) {
			matches = true
		}
		for _, p := range agents {
			if upload.ProfileID == p.ProfileID && (upload.WorkspaceID == p.WorkspaceID || upload.NewWorkspaceID == p.WorkspaceID) {
				matches = true
			}
		}
		if !matches {
			continue
		}
		revisions[upload.ID] = true
		if cancel, ok := publicationCancels.Load(upload.ID); ok {
			cancel.(context.CancelFunc)()
		}
		// Keep a small tombstone so an old lost-response retry cannot recreate
		// a project the person deliberately deleted. Remove all saved raw bytes.
		upload.State = "deleted"
		upload.Error = "This Crew was deleted. Start a new publication to create another Crew."
		if err := savePublicationUpload(&upload); err != nil {
			return err
		}
		dir := filepath.Dir(file)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() != "operation.json" {
				if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
					return err
				}
			}
		}
		if err := savePublicationUpload(&upload); err != nil {
			return err
		}
	}
	for revision := range revisions {
		if _, err := uuid.Parse(revision); err != nil {
			continue
		}
		if err := os.RemoveAll(filepath.Join(state, "history", revision)); err != nil {
			return err
		}
	}
	for _, p := range agents {
		// The parent of revision/source holds this agent's immutable versions.
		source := filepath.Dir(filepath.Dir(filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(p.sourceRoot()))))
		if err := os.RemoveAll(source); err != nil {
			return err
		}
		file, err := publicationRecordPath(p.Root, p.AgentID)
		if err != nil {
			return err
		}
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (api *StreamingAPI) handlePublicationHistory(w http.ResponseWriter, r *http.Request) {
	file, name, ok := api.openPublicationHistory(w, r, r.URL.Query().Get("workspace_id"), r.URL.Query().Get("agent_id"), r.URL.Query().Get("kind"))
	if !ok {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		externalFailure(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="imported-`+name+`"`)
	http.ServeContent(w, r, "imported-"+name, info.ModTime(), file)
}

// Website downloads and MCP reads share the same current Builder admission.
// The selected archive stays in private state and never becomes a shared file.
func (api *StreamingAPI) openPublicationHistory(w http.ResponseWriter, r *http.Request, workspaceID, agentID, kind string) (*os.File, string, bool) {
	claims := GetUserFromContext(r.Context())
	if err := publicationGate(claims); err != nil {
		externalError(w, 403, "forbidden", err.Error())
		return nil, "", false
	}
	binding, manifest, ok := api.resolveExternalWorkspace(r.Context(), claims, workspaceID)
	if !ok || !binding.OwnedByCaller || !publicationOriginTokenAllowed(claims, manifest.Product, manifest.ID) {
		externalError(w, 404, "not_found", "Open the Crew as a Builder to download its imported chat. Shared use is through its dashboard and Ask.")
		return nil, "", false
	}
	id := agentID
	if id == "main" {
		id = ""
	}
	p, err := readPublishedAgent(contextWithoutPublication(r.Context()), agentProfileRuntimeWorkspace(binding.OwnerID, binding.Binding.WorkspacePath), id)
	if err != nil || p == nil {
		externalError(w, 404, "not_found", "This agent has no imported chat. Choose one from the Crew's Agents list.")
		return nil, "", false
	}
	name := "raw.jsonl"
	if kind == "summary" {
		name = "summary.md"
	} else if kind != "" && kind != "raw" {
		externalError(w, 400, "invalid_arguments", "Choose raw or summary history.")
		return nil, "", false
	}
	dir, err := p.historyDir()
	if err != nil {
		externalFailure(w, err)
		return nil, "", false
	}
	file, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		externalError(w, 404, "not_found", "The imported chat is unavailable. Check this Crew's publication receipt.")
		return nil, "", false
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		externalError(w, 404, "not_found", "Imported chat unavailable.")
		return nil, "", false
	}
	return file, name, true
}
