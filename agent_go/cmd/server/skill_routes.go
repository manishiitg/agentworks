package server

import (
	"encoding/json"
	"log"
	"net/http"
	"path"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"

	"github.com/gorilla/mux"
)

// RegisterSkillRoutes sets up skill API routes
func RegisterSkillRoutes(router *mux.Router, api *StreamingAPI) {
	workspaceAPIURL := getWorkspaceAPIURL()

	router.HandleFunc("/skills", workspaceSkillRoute(false, listSkillsHandler(workspaceAPIURL))).Methods("GET", "OPTIONS")
	router.HandleFunc("/skills/import", workspaceSkillRoute(true, importSkillHandler(workspaceAPIURL))).Methods("POST", "OPTIONS")
	router.HandleFunc("/skills/import-zip", workspaceSkillRoute(true, importSkillZipHandler(workspaceAPIURL))).Methods("POST", "OPTIONS")
	router.HandleFunc("/skills/validate", workspaceSkillRoute(false, validateSkillHandler(workspaceAPIURL))).Methods("POST", "OPTIONS")
	router.HandleFunc("/skills/validate-zip", workspaceSkillRoute(false, validateSkillZipHandler(workspaceAPIURL))).Methods("POST", "OPTIONS")
	router.HandleFunc("/skills/{name}", workspaceSkillRoute(false, getSkillHandler(workspaceAPIURL))).Methods("GET", "OPTIONS")
	router.HandleFunc("/skills/{name}", workspaceSkillRoute(true, deleteSkillHandler(workspaceAPIURL))).Methods("DELETE", "OPTIONS")
	router.HandleFunc("/skills/cli/install", workspaceSkillRoute(true, cliInstallHandler(workspaceAPIURL))).Methods("POST", "OPTIONS")
	router.HandleFunc("/skills/cli/available", cliAvailableHandler()).Methods("GET", "OPTIONS")
	router.HandleFunc("/skills/cli/search", cliSearchHandler()).Methods("GET", "OPTIONS")

}

// Skills always belong to the active workspace. Authorize before any inventory,
// migration or mutation; canonicalize private product paths once at the edge.
func workspaceSkillRoute(write bool, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		claims := GetUserFromContext(r.Context())
		if claims == nil {
			http.Error(w, "sign in to manage skills", http.StatusUnauthorized)
			return
		}
		if write && workflowAccessForClaims(claims) == WorkflowAccessRead {
			http.Error(w, "read-only accounts cannot change workspace skills", http.StatusForbidden)
			return
		}
		raw := strings.TrimSpace(r.URL.Query().Get("workspace_path"))
		clean := path.Clean(raw)
		if raw == "" || clean != raw || strings.Contains(raw, "..") || path.IsAbs(raw) {
			http.Error(w, "workspace_path is required and must identify the current workspace", 400)
			return
		}
		if crew, ok := resolveCrewPath(r.Context(), claims.UserID, clean); ok {
			access := crewAccessFor(claims, crew)
			if crew.Rest != "" || access == crewAccessNone || (write && access != crewAccessOwner) {
				http.Error(w, "Crew skill access denied", 403)
				return
			}
			clean = crew.Root
		} else if strings.HasPrefix(clean, "Workflow/") {
			manifest, exists, err := ReadWorkflowManifest(r.Context(), clean)
			if err != nil || !exists || !userAllowedWorkflowID(claims, manifest.ID) {
				http.Error(w, "choose a workflow you can access", 403)
				return
			}
			if write {
				if !requireWorkflowOwner(w, r, clean) {
					return
				}
			} else if !requireWorkflowVisible(w, r, clean) {
				return
			}
		} else {
			ref := workspaceref.MustParse(clean)
			if ref.Logical() != "Chats" && !strings.HasPrefix(ref.Logical(), "Chats/") {
				http.Error(w, "choose a product or workflow workspace", 400)
				return
			}
			validated, err := cleanAgentProfileWorkspace(clean, claims.UserID)
			if err != nil {
				http.Error(w, err.Error(), 403)
				return
			}
			clean = agentProfileRuntimeWorkspace(claims.UserID, validated)
		}
		query := r.URL.Query()
		query.Set("workspace_path", clean)
		r.URL.RawQuery = query.Encode()
		handler(w, r)
	}
}

func listSkillsHandler(workspaceAPIURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		inventory, err := skills.DiscoverSkillsIn(workspaceAPIURL, r.URL.Query().Get("workspace_path"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		response := skills.ListSkillsResponse{
			Skills: inventory.Skills,
			Total:  len(inventory.Skills),
			Usage:  inventory.Usage,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

func getSkillHandler(workspaceAPIURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		vars := mux.Vars(r)
		name := vars["name"]
		if name == "" {
			http.Error(w, "skill name is required", http.StatusBadRequest)
			return
		}

		skill, err := skills.GetSkillIn(workspaceAPIURL, r.URL.Query().Get("workspace_path"), name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(skill)
	}
}

func importSkillHandler(workspaceAPIURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		var req skills.ImportSkillRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.GitHubURL == "" {
			http.Error(w, "github_url is required", http.StatusBadRequest)
			return
		}

		result, err := skills.ImportGitHubSkillInto(workspaceAPIURL, req.GitHubURL, req.GitHubToken, path.Join(r.URL.Query().Get("workspace_path"), "skills"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if !result.Success {
			w.WriteHeader(http.StatusBadRequest)
		}
		json.NewEncoder(w).Encode(result)
	}
}

func validateSkillHandler(workspaceAPIURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		var req skills.ValidateSkillRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.GitHubURL == "" {
			http.Error(w, "github_url is required", http.StatusBadRequest)
			return
		}

		log.Printf("[VALIDATE] URL: %s, token provided: %v, token length: %d", req.GitHubURL, req.GitHubToken != "", len(req.GitHubToken))

		result, err := skills.ValidateGitHubSkill(workspaceAPIURL, req.GitHubURL, req.GitHubToken)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if result.Frontmatter != nil {
			_, err := skills.GetSkillIn(workspaceAPIURL, r.URL.Query().Get("workspace_path"), result.Frontmatter.Name)
			result.Exists = err == nil
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func deleteSkillHandler(workspaceAPIURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		vars := mux.Vars(r)
		name := vars["name"]
		if name == "" {
			http.Error(w, "skill name is required", http.StatusBadRequest)
			return
		}

		if err := skills.UninstallSkillIn(r.Context(), workspaceAPIURL, r.URL.Query().Get("workspace_path"), name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func validateSkillZipHandler(workspaceAPIURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Parse multipart form with 10MB limit
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "failed to parse form: "+err.Error(), http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "file is required", http.StatusBadRequest)
			return
		}
		defer file.Close()

		result, err := skills.ValidateZipSkill(workspaceAPIURL, file, header)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if result.Frontmatter != nil {
			_, err := skills.GetSkillIn(workspaceAPIURL, r.URL.Query().Get("workspace_path"), result.Frontmatter.Name)
			result.Exists = err == nil
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

// --- CLI-based handlers ---

func cliAvailableHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]bool{"available": skills.IsAvailable()})
	}
}

func cliSearchHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		query := r.URL.Query().Get("q")
		if query == "" {
			http.Error(w, "query parameter 'q' is required", http.StatusBadRequest)
			return
		}

		results, err := skills.FindSkills(r.Context(), query)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}
}

func cliInstallHandler(workspaceAPIURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		var req struct {
			Source string `json:"source"` // owner/repo, URL, or local path
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Source == "" {
			http.Error(w, "source is required (e.g., 'owner/repo' or GitHub URL)", http.StatusBadRequest)
			return
		}

		result, err := skills.ImportToWorkspaceDir(r.Context(), workspaceAPIURL, req.Source, path.Join(r.URL.Query().Get("workspace_path"), "skills"))
		if err != nil {
			log.Printf("[SKILLS CLI] Install failed for '%s': %v", req.Source, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}

func importSkillZipHandler(workspaceAPIURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Parse multipart form with 10MB limit
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "failed to parse form: "+err.Error(), http.StatusBadRequest)
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "file is required", http.StatusBadRequest)
			return
		}
		defer file.Close()

		result, err := skills.ImportZipSkillInto(workspaceAPIURL, file, header, path.Join(r.URL.Query().Get("workspace_path"), "skills"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if !result.Success {
			w.WriteHeader(http.StatusBadRequest)
		}
		json.NewEncoder(w).Encode(result)
	}
}
