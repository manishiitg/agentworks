package server

import (
	"sync"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
)

// A retained coding CLI applies its environment once, at launch, and messages
// typed into it while it lives never pass through turn setup, where a changed
// secret would relaunch it. So a rotated workflow secret stayed old for as
// long as people kept talking to the live CLI (PLAT-658). These counters let
// live delivery see that the secrets a session launched with have changed.
var (
	secretScopeMu          sync.Mutex
	workflowSecretVersions = map[string]uint64{}
	sessionSecretScopes    = map[string]sessionSecretScope{}
)

type sessionSecretScope struct {
	path    string
	version uint64
}

func secretScopeKey(workspacePath string) string {
	if normalized, err := chathistory.NormalizeWorkflowSecretPath(workspacePath); err == nil {
		return normalized
	}
	return workspacePath
}

func markWorkflowSecretsChanged(workspacePath string) {
	key := secretScopeKey(workspacePath)
	secretScopeMu.Lock()
	workflowSecretVersions[key]++
	secretScopeMu.Unlock()
}

// recordSessionSecretScope notes, at turn setup, which workflow's secrets the
// session's CLI is launched or reused with. An empty path clears it.
func recordSessionSecretScope(sessionID, workspacePath string) {
	if sessionID == "" {
		return
	}
	secretScopeMu.Lock()
	defer secretScopeMu.Unlock()
	if workspacePath == "" {
		delete(sessionSecretScopes, sessionID)
		return
	}
	key := secretScopeKey(workspacePath)
	sessionSecretScopes[sessionID] = sessionSecretScope{path: key, version: workflowSecretVersions[key]}
}

func sessionSecretsChanged(sessionID string) bool {
	secretScopeMu.Lock()
	defer secretScopeMu.Unlock()
	scope, ok := sessionSecretScopes[sessionID]
	return ok && workflowSecretVersions[scope.path] != scope.version
}
