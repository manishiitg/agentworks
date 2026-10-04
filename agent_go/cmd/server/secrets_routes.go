package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gorilla/mux"
)

// globalSecretEntry holds a single env-based global secret (name + plaintext value)
type globalSecretEntry struct {
	Name    string
	Value   string
	Managed bool
}

// globalSecrets is populated once at startup from GLOBAL_SECRET_* env vars
var globalSecrets []globalSecretEntry

// loadGlobalSecrets scans os.Environ() for GLOBAL_SECRET_ prefix and populates globalSecrets
func loadGlobalSecrets() {
	managedGlobalsMu.Lock()
	defer managedGlobalsMu.Unlock()
	const prefix = "GLOBAL_SECRET_"
	globalSecrets = nil
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, prefix) {
			continue
		}
		eqIdx := strings.Index(env, "=")
		if eqIdx < 0 {
			continue
		}
		name := env[len(prefix):eqIdx]
		value := env[eqIdx+1:]
		if !globalSecretNamePattern.MatchString(name) {
			continue
		}
		globalSecrets = append(globalSecrets, globalSecretEntry{Name: name, Value: value})
	}
	if len(globalSecrets) > 0 {
		names := make([]string, len(globalSecrets))
		for i, s := range globalSecrets {
			names[i] = s.Name
		}
		log.Printf("[SECRETS] Loaded %d global secrets from environment: %v", len(globalSecrets), names)
	}
}

// getGlobalSecrets snapshots environment and encrypted admin-managed globals.
func getGlobalSecrets() []globalSecretEntry {
	return sortedGlobalSecrets()
}

// handleGetGlobalSecrets returns the names of global secrets (no values exposed)
// GET /api/secrets/global
func (api *StreamingAPI) handleGetGlobalSecrets(w http.ResponseWriter, r *http.Request) {
	type entry struct {
		Name    string `json:"name"`
		Managed bool   `json:"managed"`
	}
	w.Header().Set("Cache-Control", "no-store")
	userID := GetUserIDFromContext(r.Context())
	var globals []globalSecretEntry
	if r.URL.Query().Get("manage") == "true" {
		if !canManageGlobalSecrets(userID) {
			globalSecretError(w, errGlobalAdmin)
			return
		}
		if err := syncVaultSecretMetadata(r.Context(), userID); err != nil {
			writeUsersError(w, 503, err.Error())
			return
		}
		globals = getGlobalSecrets()
	} else {
		var err error
		globals, err = permittedGlobalSecrets(r.Context(), userID)
		if err != nil {
			writeUsersError(w, 503, err.Error())
			return
		}
	}
	result := make([]entry, len(globals))
	for i, s := range globals {
		result[i] = entry{Name: s.Name, Managed: s.Managed}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

// secretEncryptRequest is the request body for encrypting a secret value
type secretEncryptRequest struct {
	Value string `json:"value"`
}

// secretEncryptResponse is the response body with the encrypted value
type secretEncryptResponse struct {
	Encrypted string `json:"encrypted"`
}

// secretDecryptRequest is the request body for decrypting a secret value.
// WorkspacePath marks the blob as a shared workflow secret (bound to the
// workflow, not the caller) and makes the request a reveal that only the
// workflow's owners may perform.
type secretDecryptRequest struct {
	Encrypted     string `json:"encrypted"`
	WorkspacePath string `json:"workspace_path,omitempty"`
}

// secretDecryptResponse is the response body with the decrypted value
type secretDecryptResponse struct {
	Value string `json:"value"`
}

// deriveSecretsKey derives a 32-byte AES-256 key from AUTH_SECRET using HMAC-SHA256
func deriveSecretsKey() []byte {
	return deriveSecretsKeyFromSecret(GetAuthSecret())
}

// deriveSecretsKeyFromSecret derives a 32-byte AES-256 key from the provided secret using HMAC-SHA256.
func deriveSecretsKeyFromSecret(secret []byte) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("secrets-encryption-key"))
	return mac.Sum(nil) // 32 bytes = AES-256
}

// handleEncryptSecret encrypts a plaintext value using AES-256-GCM
// POST /api/secrets/encrypt
func (api *StreamingAPI) handleEncryptSecret(w http.ResponseWriter, r *http.Request) {
	var req secretEncryptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Value == "" {
		http.Error(w, "Value is required", http.StatusBadRequest)
		return
	}

	userID := GetUserIDFromContext(r.Context())

	key := deriveSecretsKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create cipher: %v", err), http.StatusInternalServerError)
		return
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create GCM: %v", err), http.StatusInternalServerError)
		return
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		http.Error(w, fmt.Sprintf("Failed to generate nonce: %v", err), http.StatusInternalServerError)
		return
	}

	// Use the caller id as additional authenticated data so only the same
	// session can unwrap this transport ciphertext before the server
	// re-binds it to a workflow box or managed global.
	aad := []byte(userID)
	ciphertext := aesGCM.Seal(nonce, nonce, []byte(req.Value), aad)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(secretEncryptResponse{
		Encrypted: base64.StdEncoding.EncodeToString(ciphertext),
	})
}

// handleDecryptSecret decrypts an AES-256-GCM encrypted value
// POST /api/secrets/decrypt
func (api *StreamingAPI) handleDecryptSecret(w http.ResponseWriter, r *http.Request) {
	var req secretDecryptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.Encrypted == "" {
		http.Error(w, "Encrypted value is required", http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.WorkspacePath) != "" {
		// Reveal of a shared workflow secret: owners only. Readers can run the
		// workflow with the value but never see it.
		workspacePath, err := secretWorkspacePathForRequest(r, req.WorkspacePath)
		if err != nil {
			http.Error(w, "Invalid workspace_path", http.StatusBadRequest)
			return
		}
		if !requireWorkflowOwner(w, r, workspacePath) {
			return
		}
		aad, err := sharedWorkflowSecretAAD(workspacePath)
		if err != nil {
			http.Error(w, "Invalid workspace_path", http.StatusBadRequest)
			return
		}
		plaintext, err := decryptSecretValueWithAAD(req.Encrypted, aad)
		if err != nil {
			http.Error(w, "Decryption failed — invalid key or data", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(secretDecryptResponse{Value: plaintext})
		return
	}

	userID := GetUserIDFromContext(r.Context())

	data, err := base64.StdEncoding.DecodeString(req.Encrypted)
	if err != nil {
		http.Error(w, "Invalid base64 encoding", http.StatusBadRequest)
		return
	}

	key := deriveSecretsKey()
	block, err := aes.NewCipher(key)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create cipher: %v", err), http.StatusInternalServerError)
		return
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create GCM: %v", err), http.StatusInternalServerError)
		return
	}

	nonceSize := aesGCM.NonceSize()
	if len(data) < nonceSize {
		http.Error(w, "Encrypted data too short", http.StatusBadRequest)
		return
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]

	// Use the caller id as AAD — transport ciphertext only unwraps in the
	// session that created it.
	aad := []byte(userID)
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		http.Error(w, "Decryption failed — invalid key or data", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(secretDecryptResponse{
		Value: string(plaintext),
	})
}

// decryptSecretValue unwraps transport ciphertext produced by
// /api/secrets/encrypt in the caller's session (caller id is the AAD).
// Callers re-bind the plaintext to a workflow box or managed global; the
// ciphertext itself is never stored as a secret.
func decryptSecretValue(encryptedBase64 string, userID string) (string, error) {
	return decryptSecretValueWithAAD(encryptedBase64, []byte(userID))
}

// secretWorkspacePathForRequest maps the product-relative workspace path used
// by the browser to the same private path used by the product runtime. Without
// this normalization the live agent writes _users/<id>/Chats/... while the
// setup pane reads a different, empty document keyed by Chats/....
func secretWorkspacePathForRequest(r *http.Request, raw string) (string, error) {
	userID := GetUserIDFromContext(r.Context())
	clean, err := cleanAgentProfileWorkspace(raw, userID)
	if err != nil {
		return "", err
	}
	return agentProfileRuntimeWorkspace(userID, clean), nil
}

// storeSecretRequest is the request body for storing a workflow secret server-side
type storeSecretRequest struct {
	Name           string `json:"name"`
	EncryptedValue string `json:"encrypted_value"`
	WorkspacePath  string `json:"workspace_path,omitempty"`
}

// handleStoreWorkflowSecret upserts a workflow secret in the shared,
// workflow-scoped store (see workflow_shared_secrets.go). Owners only.
// PUT /api/secrets/workflow/store
//
// The client keeps sending encrypted_value from /api/secrets/encrypt (bound to
// the caller); it is re-bound to the workflow here so every user with access
// resolves it.
func (api *StreamingAPI) handleStoreWorkflowSecret(w http.ResponseWriter, r *http.Request) {
	var req storeSecretRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if req.WorkspacePath == "" || req.Name == "" || req.EncryptedValue == "" {
		http.Error(w, "workspace_path, name, and encrypted_value are required", http.StatusBadRequest)
		return
	}
	workspacePath, err := secretWorkspacePathForRequest(r, req.WorkspacePath)
	if err != nil {
		http.Error(w, "Invalid workspace_path", http.StatusBadRequest)
		return
	}
	if !requireWorkflowOwner(w, r, workspacePath) {
		return
	}

	userID := GetUserIDFromContext(r.Context())
	plaintext, err := decryptSecretValue(req.EncryptedValue, userID)
	if err != nil {
		http.Error(w, "encrypted_value must be produced by /api/secrets/encrypt in this session", http.StatusBadRequest)
		return
	}
	if err := api.upsertSharedWorkflowSecret(r.Context(), workspacePath, req.Name, plaintext); err != nil {
		log.Printf("[SECRETS] Failed to store workflow secret: %v", err)
		http.Error(w, "Failed to store workflow secret", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleDeleteWorkflowSecret deletes a shared workflow secret. Owners only.
// DELETE /api/secrets/workflow/store/{name}?workspace_path=Workflow/foo
func (api *StreamingAPI) handleDeleteWorkflowSecret(w http.ResponseWriter, r *http.Request) {
	name := mux.Vars(r)["name"]
	workspacePath := r.URL.Query().Get("workspace_path")
	if name == "" || workspacePath == "" {
		http.Error(w, "Secret name and workspace_path are required", http.StatusBadRequest)
		return
	}
	var err error
	workspacePath, err = secretWorkspacePathForRequest(r, workspacePath)
	if err != nil {
		http.Error(w, "Invalid workspace_path", http.StatusBadRequest)
		return
	}
	if !requireWorkflowOwner(w, r, workspacePath) {
		return
	}

	userID := GetUserIDFromContext(r.Context())
	if err := api.deleteSharedWorkflowSecret(r.Context(), workspacePath, name, userID); err != nil {
		log.Printf("[SECRETS] Failed to delete workflow secret: %v", err)
		http.Error(w, "Failed to delete workflow secret", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

// handleListStoredWorkflowSecrets returns the workflow's shared secret names
// for anyone who can see the workflow. Owners additionally receive the
// ciphertext so the pane can reveal a value through /api/secrets/decrypt
// (which re-checks ownership); readers get names only.
// GET /api/secrets/workflow/stored?workspace_path=Workflow/foo
func (api *StreamingAPI) handleListStoredWorkflowSecrets(w http.ResponseWriter, r *http.Request) {
	workspacePath := r.URL.Query().Get("workspace_path")
	if workspacePath == "" {
		http.Error(w, "workspace_path is required", http.StatusBadRequest)
		return
	}
	var err error
	workspacePath, err = secretWorkspacePathForRequest(r, workspacePath)
	if err != nil {
		http.Error(w, "Invalid workspace_path", http.StatusBadRequest)
		return
	}
	level := currentUserWorkflowAccess(r, workspacePath)
	if level == WorkflowAccessNone {
		writeWorkflowPermissionDenied(w, "read")
		return
	}
	canReveal := level == WorkflowAccessOwner || level == WorkflowAccessWrite

	userID := GetUserIDFromContext(r.Context())
	secrets, err := api.ensureSharedWorkflowSecrets(r.Context(), workspacePath, userID)
	if err != nil {
		log.Printf("[SECRETS] Failed to list workflow secrets: %v", err)
		http.Error(w, "Failed to list workflow secrets", http.StatusInternalServerError)
		return
	}

	type entry struct {
		Name           string `json:"name"`
		EncryptedValue string `json:"encrypted_value,omitempty"`
	}
	result := make([]entry, len(secrets))
	for i, s := range secrets {
		result[i] = entry{Name: s.Name}
		if canReveal {
			result[i].EncryptedValue = s.EncryptedValue
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
