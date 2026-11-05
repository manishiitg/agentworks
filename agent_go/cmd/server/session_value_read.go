package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// A coding CLI's shell reads a secret's current value with `mcpbridge secret NAME` (owner, 5 Nov, the decisions log):
// SECRET_* is copied into a program when it starts, so a secret added or changed while a chat runs never reached it.
// The read answers from the same selection the launch uses, re-read now: a turn that gets no SECRET_* gets nothing
// here (Run turns, a profile with secrets turned off), a Team member gets the owner's lent secrets, and the same plain
// name wins as in the environment. The value goes only to the caller and is never logged.

type sessionSecretSource struct {
	User          string
	Folder        string    // req.SelectedFolder: whose project secrets and lent Vault apply (projectSecretsOwner)
	WorkspacePath string    // req.secretsWorkspacePath: where this turn's selected secrets are stored
	ProfileID     string    // a product profile's ID for a Crew chat
	CrewProfile   bool      // a Crew/product chat: its selection lives in the product manifest
	RunMode       bool      // crewAskOnly: no secrets at all
	Disabled      bool      // the profile turned secrets off
	Globals       *[]string // the launch's global selection, used when the current one cannot be read
}

var sessionSecretSources sync.Map

// recordSessionSecretSource notes, where a turn builds its CLI's SECRET_* environment, how to resolve it again.
func recordSessionSecretSource(sessionID string, source sessionSecretSource) {
	if strings.TrimSpace(sessionID) != "" {
		sessionSecretSources.Store(sessionID, source)
	}
}

type resolvedSecret = struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// currentSessionSecrets re-reads the session's secret selection now, the same way the launch built SECRET_*.
func (api *StreamingAPI) currentSessionSecrets(ctx context.Context, source sessionSecretSource) []resolvedSecret {
	if source.RunMode || source.Disabled {
		return nil
	}
	ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: source.User, Username: source.User})
	globals := source.Globals
	var scoped []resolvedSecret
	path := strings.TrimSpace(source.WorkspacePath)
	if caps, found, err := LoadManifestForExecution(ctx, path); path != "" && err == nil && found {
		// A workflow or Crew turn whose manifest holds its selection (handleQuery's manifest branch).
		owner := source.User
		if isCrewProjectPath(path) && crewProjectOwnedByCaller(source.User, path) {
			owner = api.projectSecretsOwner(source.User, path)
		}
		scoped = api.loadSelectedSecrets(ctx, owner, path, caps.SelectedSecrets)
		if caps.SelectedGlobalSecretNames != nil {
			globals = caps.SelectedGlobalSecretNames
		}
	} else if source.CrewProfile && api.chatStore != nil {
		// A product chat (agent_profile_runtime.go): its selection is in the product manifest.
		person := api.crewSecretsPerson(source.User, source.Folder, "agent_profile")
		store := path
		if store == "" {
			store = agentProfileRuntimeWorkspace(source.User, source.Folder)
		}
		if selected, err := productSelectedGlobalSecrets(ctx, source.ProfileID, source.Folder); err == nil {
			globals = selected
		}
		if names, _, err := productSelectedSecrets(ctx, source.ProfileID, source.Folder); err == nil && len(names) > 0 {
			scoped = api.loadSelectedSecrets(ctx, person, store, names)
		}
	}
	return api.mergeGlobalSecretsFor(ctx, api.projectSecretsOwner(source.User, source.Folder), scoped, globals)
}

// sessionSecretsResolver is how the read resolves a session's secrets (replaced in tests only).
var sessionSecretsResolver = (*StreamingAPI).currentSessionSecrets

var secretNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,128}$`)

// handleSessionSecretRead answers `mcpbridge secret NAME` for the session its own bridge token names.
func (api *StreamingAPI) handleSessionSecretRead(w http.ResponseWriter, r *http.Request) {
	reply := func(status int, body map[string]string) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	}
	// Only the chat's own read-only token ("<session>#secret-read", minted for its shell): the process-wide token and
	// the bridge's tool token never read a secret, and the read-only token reaches no tool route.
	tokenSession, ok := common.VerifyBridgeToken(strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")))
	sessionID, readOnly := strings.CutSuffix(tokenSession, mcpagent.SecretReadTokenSuffix)
	if !ok || !readOnly || sessionID == "" {
		reply(http.StatusUnauthorized, map[string]string{"error": "this needs the chat's own secret-read token"})
		return
	}
	// The token lives with the chat's reply: only while a turn runs in it, and never once it is a Run turn.
	if !api.sessionTurnRunning(sessionID) {
		log.Printf("[SECRET_READ] session=%s result=refused reason=no_running_turn", sessionID)
		reply(http.StatusForbidden, map[string]string{"error": "secrets can be read only while this chat's reply is running"})
		return
	}
	if cfg := common.GetSessionShellConfig(sessionID); cfg != nil && cfg.CrewAskReadOnly {
		log.Printf("[SECRET_READ] session=%s result=refused reason=run_turn", sessionID)
		reply(http.StatusForbidden, map[string]string{"error": "this chat gets no secrets"})
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&in) != nil || !secretNamePattern.MatchString(strings.TrimPrefix(strings.TrimSpace(in.Name), "SECRET_")) {
		reply(http.StatusBadRequest, map[string]string{"error": "give a secret name, for example GITHUB_TOKEN"})
		return
	}
	name := strings.TrimPrefix(strings.TrimSpace(in.Name), "SECRET_")
	raw, found := sessionSecretSources.Load(sessionID)
	if !found {
		log.Printf("[SECRET_READ] session=%s secret=%s result=not_found reason=no_turn", sessionID, name)
		reply(http.StatusNotFound, map[string]string{"error": "this chat has no secrets set up yet"})
		return
	}
	source := raw.(sessionSecretSource)
	if source.RunMode || source.Disabled {
		log.Printf("[SECRET_READ] session=%s secret=%s result=refused reason=no_secrets_for_this_turn", sessionID, name)
		reply(http.StatusForbidden, map[string]string{"error": "this chat gets no secrets"})
		return
	}
	for _, secret := range sessionSecretsResolver(api, r.Context(), source) {
		if secret.Name == name && secret.Value != "" {
			log.Printf("[SECRET_READ] session=%s secret=%s result=ok", sessionID, name)
			reply(http.StatusOK, map[string]string{"value": secret.Value})
			return
		}
	}
	log.Printf("[SECRET_READ] session=%s secret=%s result=not_found", sessionID, name)
	reply(http.StatusNotFound, map[string]string{"error": name + " isn't selected for this chat. Add it to this project's secrets in Vault, or ask the project's owner to lend it."})
}

// secretsTurnDisabled: the profile turned secrets off for its turns.
func secretsTurnDisabled(profile *resolvedAgentProfile) bool {
	return profile != nil && profile.Definition.Runtime.Capabilities.Secrets == agentprofiles.CapabilityDisabled
}
