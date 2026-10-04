package server

import (
	"encoding/json"
	"net/http"
	"path"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/commands"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// RegisterCommandRoutes sets up user command API routes
func RegisterCommandRoutes(router *mux.Router, api *StreamingAPI) {
	workspaceAPIURL := getWorkspaceAPIURL()

	router.HandleFunc("/commands", listCommandsHandler(workspaceAPIURL)).Methods("GET", "OPTIONS")
	router.HandleFunc("/commands", createCommandHandler(workspaceAPIURL)).Methods("POST", "OPTIONS")
	router.HandleFunc("/commands/{name}", getCommandHandler(workspaceAPIURL)).Methods("GET", "OPTIONS")
	router.HandleFunc("/commands/{name}", updateCommandHandler(workspaceAPIURL)).Methods("PUT", "OPTIONS")
	router.HandleFunc("/commands/{name}", deleteCommandHandler(workspaceAPIURL)).Methods("DELETE", "OPTIONS")
}

func listCommandsHandler(workspaceAPIURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		commandsPath, ok := commandPathForRequest(w, r, false)
		if !ok {
			return
		}
		cmdList, err := commands.DiscoverCommandsAt(workspaceAPIURL, commandsPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		response := commands.ListCommandsResponse{
			Commands: cmdList,
			Total:    len(cmdList),
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	}
}

func getCommandHandler(workspaceAPIURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		vars := mux.Vars(r)
		name := vars["name"]
		if name == "" {
			http.Error(w, "command name is required", http.StatusBadRequest)
			return
		}

		commandsPath, ok := commandPathForRequest(w, r, false)
		if !ok {
			return
		}
		cmd, err := commands.GetCommandAt(workspaceAPIURL, commandsPath, name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cmd)
	}
}

func createCommandHandler(workspaceAPIURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		var req commands.CreateCommandRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Name == "" {
			http.Error(w, "name is required", http.StatusBadRequest)
			return
		}

		if req.Content == "" {
			http.Error(w, "content is required", http.StatusBadRequest)
			return
		}

		commandsPath, ok := commandPathForRequest(w, r, true)
		if !ok {
			return
		}
		cmd, err := commands.CreateCommandAt(workspaceAPIURL, commandsPath, req.Name, req.Content)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(cmd)
	}
}

func updateCommandHandler(workspaceAPIURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		vars := mux.Vars(r)
		name := vars["name"]
		if name == "" {
			http.Error(w, "command name is required", http.StatusBadRequest)
			return
		}

		var req commands.UpdateCommandRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		commandsPath, ok := commandPathForRequest(w, r, true)
		if !ok {
			return
		}
		cmd, err := commands.UpdateCommandAt(workspaceAPIURL, commandsPath, name, req.Content)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(cmd)
	}
}

func deleteCommandHandler(workspaceAPIURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		vars := mux.Vars(r)
		name := vars["name"]
		if name == "" {
			http.Error(w, "command name is required", http.StatusBadRequest)
			return
		}

		commandsPath, ok := commandPathForRequest(w, r, true)
		if !ok {
			return
		}
		if err := commands.DeleteCommandAt(workspaceAPIURL, commandsPath, name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

// commandPathForRequest binds custom commands to the active workspace. An
// omitted scope means the signed-in user's personal AgentWorks command set.
// Product paths are canonicalized into that user's private _users tree.
func commandPathForRequest(w http.ResponseWriter, r *http.Request, write bool) (string, bool) {
	requested := strings.Trim(strings.TrimSpace(r.URL.Query().Get("workspace_path")), "/")
	userID := GetUserIDFromContext(r.Context())
	if requested == "" {
		return workspaceref.PhysicalPath(userID, commands.CustomCommandsSubPath), true
	}
	clean := path.Clean(requested)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		http.Error(w, "invalid workspace_path", http.StatusBadRequest)
		return "", false
	}
	if clean == "Workflow" {
		http.Error(w, "workspace_path must identify a workflow", http.StatusBadRequest)
		return "", false
	}
	if strings.HasPrefix(clean, "Workflow/") {
		if write {
			if !requireWorkflowOwner(w, r, clean) {
				return "", false
			}
		} else if !requireWorkflowVisible(w, r, clean) {
			return "", false
		}
		return path.Join(clean, commands.CustomCommandsSubPath), true
	}
	if ref := workspaceref.MustParse(clean); !ref.HasOwner() && ref.Logical() != "Chats" && !strings.HasPrefix(ref.Logical(), "Chats/") {
		http.Error(w, "workspace_path must identify the current project or workflow", http.StatusBadRequest)
		return "", false
	}
	validated, err := cleanAgentProfileWorkspace(clean, userID)
	if err != nil {
		http.Error(w, "workspace_path is outside your workspace", http.StatusForbidden)
		return "", false
	}
	canonical := agentProfileRuntimeWorkspace(userID, validated)
	return path.Join(canonical, commands.CustomCommandsSubPath), true
}
