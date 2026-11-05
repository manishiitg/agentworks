package server

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpclient"
)

// A Watcher reads Slack, read-only (owner, 5 Nov: "yes read only"). Its detector script gets a bridge session of its
// own for the length of one check, as a report script does (report_run.go): the script calls the Crew's selected Vault
// Slack MCP through that session, the server makes the call with the Watcher owner's connection, and no Slack token
// reaches the script. Only reading tools are offered; sending, creating, scheduling or changing anything is refused.

const watcherSlackSessionPrefix = "watcher-slack-"

type watcherSlackScope struct {
	jobID         string
	userID        string
	workspacePath string
}

// watcherSlackReadTools are the Slack MCP tools a Watcher may call: each reads, none sends or changes anything.
var watcherSlackReadTools = map[string]bool{
	"slack_read_channel": true, "slack_read_thread": true, "slack_search_public_and_private": true, "slack_search_public": true,
	"slack_list_user_channels": true, "slack_read_user_profile": true, "slack_search_users": true, "slack_search_channels": true,
}

// watcherSlackToolAllowed: the tool (its upstream name, after a Vault prefix such as slack_c-<hash>__) reads Slack.
func watcherSlackToolAllowed(tool string) bool {
	name := strings.TrimSpace(tool)
	if _, upstream, prefixed := strings.Cut(name, "__"); prefixed {
		name = upstream
	}
	return watcherSlackReadTools[name]
}

// openWatcherSlackSession gives one check its bridge session; the returned stop ends it (later calls are refused).
func (api *StreamingAPI) openWatcherSlackSession(job productScheduleJob, root string) (map[string]string, func()) {
	base := strings.TrimRight(os.Getenv("MCP_API_URL"), "/")
	if api == nil || base == "" {
		return nil, func() {}
	}
	session := watcherSlackSessionPrefix + uuid.NewString()
	api.watcherSlackSessions.Store(session, watcherSlackScope{jobID: job.ID(), userID: job.UserID, workspacePath: root})
	env := map[string]string{"MCP_API_URL": base + "/s/" + session, "MCP_SESSION_ID": session}
	common.PopulateMCPBridgeShortEnv(env) // the session's own token
	delete(env, "MCP_CUSTOM")             // Slack reading only: no platform tools, no virtual tools
	delete(env, "MCP_VIRTUAL")
	return env, func() { api.watcherSlackSessions.Delete(session) }
}

// resolveWatcherSlackMCPServer scopes a Watcher check's bridge calls to read tools of the Crew's selected servers, as
// its owner, rechecked on every call. ok=false means the session is not a Watcher's.
func (api *StreamingAPI) resolveWatcherSlackMCPServer(ctx context.Context, sessionID, server, tool string) (*executor.ResolvedMCPServer, bool, error) {
	if !strings.HasPrefix(sessionID, watcherSlackSessionPrefix) {
		return nil, false, nil
	}
	cached, found := api.watcherSlackSessions.Load(sessionID)
	if !found {
		return nil, true, fmt.Errorf("this Watcher check has finished")
	}
	scope := cached.(watcherSlackScope)
	if !watcherSlackToolAllowed(tool) {
		log.Printf("[WATCHER_SLACK] job=%s tool=%s result=refused reason=not_a_read_tool", scope.jobID, tool)
		return nil, true, fmt.Errorf("a Watcher can only read Slack (read a channel or thread, search, list your conversations, look up people)")
	}
	selection, err := reportRunSelectionFor(ctx, scope.workspacePath)
	if err != nil {
		log.Printf("[WATCHER_SLACK] job=%s tool=%s result=refused reason=selection_unavailable", scope.jobID, tool)
		return nil, true, fmt.Errorf("this project's Slack connection is unavailable")
	}
	catalog, err := mcpclient.LoadMergedConfig(api.mcpConfigPath, api.logger)
	if err != nil {
		return nil, true, fmt.Errorf("load current MCP configuration: %w", err)
	}
	resolved, err := api.resolveScopedGovernedMCP(ctx, catalog, runtimeMCPServers(selection.servers), selection.tools, scope.userID, server, tool)
	result := "ok"
	if err != nil {
		result = "refused"
	}
	log.Printf("[WATCHER_SLACK] job=%s tool=%s result=%s", scope.jobID, tool, result)
	return resolved, true, err
}

// watcherSlackPerson is the Watcher's owner for a check's session ("" when it is not one, or it has finished).
func (api *StreamingAPI) watcherSlackPerson(sessionID string) (string, bool) {
	if !strings.HasPrefix(sessionID, watcherSlackSessionPrefix) {
		return "", false
	}
	if cached, ok := api.watcherSlackSessions.Load(sessionID); ok {
		return cached.(watcherSlackScope).userID, true
	}
	return "", true
}
