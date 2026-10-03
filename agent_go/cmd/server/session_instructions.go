package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

type sessionInstructionSnapshot struct {
	Content       string    `json:"content"`
	WorkspacePath string    `json:"workspace_path,omitempty"`
	CapturedAt    time.Time `json:"captured_at"`
}

// These snapshots stay in private server state, never in the workspace or
// executable release. The authenticated owner's identity is part of the key.
func sessionInstructionPath(userID, sessionID string) (string, error) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("instruction owner and session are required")
	}
	stateRoot, err := workflowCLIStateRoot()
	if err != nil {
		return "", err
	}
	userKey := fmt.Sprintf("%x", sha256.Sum256([]byte(userID)))
	sessionKey := fmt.Sprintf("%x", sha256.Sum256([]byte(sessionID)))
	return filepath.Join(stateRoot, "instruction-snapshots", userKey, sessionKey+".json"), nil
}

func saveSessionInstructions(userID, sessionID, workspacePath, prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	path, err := sessionInstructionPath(userID, sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(sessionInstructionSnapshot{Content: prompt, WorkspacePath: workspacePath, CapturedAt: time.Now().UTC()})
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".instructions-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func (api *StreamingAPI) handleGetSessionInstructions(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(mux.Vars(r)["session_id"])
	if sessionID == "" {
		http.Error(w, "session is required", http.StatusBadRequest)
		return
	}
	// A shared-workflow grant (or admin role) never selects another user's
	// private snapshot. No client-supplied user ID or filesystem path is accepted.
	path, err := sessionInstructionPath(GetUserIDFromContext(r.Context()), sessionID)
	if err != nil {
		http.Error(w, "instructions unavailable", http.StatusInternalServerError)
		return
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		http.Error(w, "No prompt snapshot yet. Send a message in this chat to prepare its instructions.", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "instructions unavailable", http.StatusInternalServerError)
		return
	}
	var snapshot sessionInstructionSnapshot
	if json.Unmarshal(data, &snapshot) != nil {
		http.Error(w, "instructions unavailable", http.StatusInternalServerError)
		return
	}
	if _, _, allowed := chatHistoryWorkspaceAccess(r, snapshot.WorkspacePath); !allowed {
		http.Error(w, "workflow access denied", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = json.NewEncoder(w).Encode(snapshot)
}

func deleteSessionInstructions(userID, sessionID string) error {
	path, err := sessionInstructionPath(userID, sessionID)
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
