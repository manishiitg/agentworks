package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// Brain keeps its own secrets (owner, 2026-10-06: "Vault = platform secrets and everything is product specific"): the
// Git backup token and anything else Brain's folder needs, in the shared encrypted secret store under Brain's folder,
// managed by the people who own the whole Brain. Platform (Vault) secrets are not listed in Brain.
const brainSecretsPath = brainFolderName

var brainSecretName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// brainSecretsAPI is the server whose store holds Brain's secrets; set when routes are registered.
var brainSecretsAPI *StreamingAPI

func (api *StreamingAPI) brainSecretNames(ctx context.Context) ([]string, error) {
	secrets, err := api.chatStore.ListWorkflowSecrets(ctx, chathistory.SharedWorkflowSecretsUserID, brainSecretsPath)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(secrets))
	for _, s := range secrets {
		names = append(names, s.Name)
	}
	sort.Strings(names)
	return names, nil
}

func (api *StreamingAPI) brainSecretValue(ctx context.Context, name string) (string, bool) {
	secrets, err := api.chatStore.ListWorkflowSecrets(ctx, chathistory.SharedWorkflowSecretsUserID, brainSecretsPath)
	if err != nil {
		return "", false
	}
	for _, s := range secrets {
		if s.Name == name {
			value, err := decryptSharedWorkflowSecret(brainSecretsPath, s)
			return value, err == nil && value != ""
		}
	}
	return "", false
}

func (api *StreamingAPI) setBrainSecret(ctx context.Context, name, value string) error {
	if !brainSecretName.MatchString(name) {
		return fmt.Errorf("secret names use letters, digits and underscores, starting with a letter")
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("the secret value is empty")
	}
	return api.upsertSharedWorkflowSecret(ctx, brainSecretsPath, name, value)
}

// knowledgebaseBackupSecret resolves a secret Brain's backup names (pat_secret): Brain's own first. A token that so
// far lived only among the platform secrets is copied into Brain's store once, so a backup set up before Brain had
// secrets keeps working.
func knowledgebaseBackupSecret(name string) (string, bool) {
	api := brainSecretsAPI
	ctx := context.Background()
	if api != nil {
		if value, ok := api.brainSecretValue(ctx, name); ok {
			return value, true
		}
	}
	for _, secret := range getGlobalSecrets() {
		if secret.Name == name && secret.Value != "" {
			if api != nil {
				_ = api.setBrainSecret(ctx, name, secret.Value)
			}
			return secret.Value, true
		}
	}
	return "", false
}

// brainSecretsManager reports whether the caller manages Brain's secrets: Owner of the whole Brain (administrators).
func brainSecretsManager(r *http.Request) bool {
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" {
		return false
	}
	service, err := knowledgebaseService()
	if err != nil || knowledgebaseSyncIdentities(r.Context(), service) != nil {
		return false
	}
	return service.CanEditWholeBrain(knowledgebasePrincipal(r, claims))
}

// GET/PUT /api/knowledgebase/secrets and DELETE /api/knowledgebase/secrets/{name}: names only, never values.
func (api *StreamingAPI) handleBrainSecrets(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	if !brainSecretsManager(r) {
		http.Error(w, "Brain's secrets are managed by people who own the whole Brain", http.StatusForbidden)
		return
	}
	ctx := r.Context()
	switch r.Method {
	case http.MethodPut:
		var body struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
			http.Error(w, "name and value are required", http.StatusBadRequest)
			return
		}
		if err := api.setBrainSecret(ctx, strings.TrimSpace(body.Name), body.Value); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	case http.MethodDelete:
		name := mux.Vars(r)["name"]
		if err := api.deleteSharedWorkflowSecret(ctx, brainSecretsPath, name, ""); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	names, err := api.brainSecretNames(ctx)
	if err != nil {
		http.Error(w, "Could not list Brain's secrets", http.StatusInternalServerError)
		return
	}
	knowledgebaseWriteJSON(w, map[string]any{"secrets": names})
}

// brainSecretsToolName lets the Brain chat manage Brain's own secrets by name; values are never returned.
const brainSecretsToolName = "brain_secrets"

func (api *StreamingAPI) registerBrainSecretsTool(registrar definitionToolRegistrar, userID string) error {
	params := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{"type": "string", "enum": []string{"list", "set", "delete"}},
			"name":   map[string]interface{}{"type": "string", "description": "Secret name, for example BRAIN_GITHUB_PAT."},
			"value":  map[string]interface{}{"type": "string", "description": "For set: the value the person gave you. Never repeat it back."},
		},
		"required":             []string{"action"},
		"additionalProperties": false,
	}
	description := "Brain's own secrets (not Vault's platform secrets): list their names, set one (for example BRAIN_GITHUB_PAT, the Git backup token named by configure_backup's pat_secret) or delete one. Values are never shown. Only people who own the whole Brain manage them."
	return registrar.RegisterCustomTool(brainSecretsToolName, description, params, func(ctx context.Context, args map[string]interface{}) (string, error) {
		claims := GetUserFromContext(ctx)
		if claims == nil || claims.UserID != userID {
			return "", fmt.Errorf("Brain's secrets belong to the signed-in person's Brain chat")
		}
		r := (&http.Request{}).WithContext(ctx)
		if !brainSecretsManager(r) {
			return "", fmt.Errorf("Brain's secrets are managed by people who own the whole Brain")
		}
		action, _ := args["action"].(string)
		name, _ := args["name"].(string)
		name = strings.TrimSpace(name)
		switch action {
		case "set":
			value, _ := args["value"].(string)
			if err := api.setBrainSecret(ctx, name, value); err != nil {
				return "", err
			}
		case "delete":
			if err := api.deleteSharedWorkflowSecret(ctx, brainSecretsPath, name, ""); err != nil {
				return "", err
			}
		case "list":
		default:
			return "", fmt.Errorf("unknown action %q", action)
		}
		names, err := api.brainSecretNames(ctx)
		if err != nil {
			return "", err
		}
		out, _ := json.Marshal(map[string]any{"secrets": names})
		return string(out), nil
	}, "knowledgebase")
}
