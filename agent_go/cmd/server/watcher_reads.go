package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browserrelay"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/mcpagent/executor"
	"github.com/manishiitg/mcpagent/mcpclient"
	"github.com/manishiitg/mcpagent/toolguard"
)

// A Watcher reads, never writes (owner, 5 Nov: "yes read only"; widened to every connected app for Project activity,
// #3476). Its detector script gets a bridge session of its own for the length of one check, as a report script does
// (report_run.go). Through that session the script may call:
//   - the Crew's selected MCP apps (Slack, Linear, Notion...), only tools that read: Slack's known read tools, and any
//     other tool the app itself marks read-only (readOnlyHint), checked by the tool guard on every call;
//   - Google (Gmail, Drive, Sheets, Docs, Calendar) through gog, always read-only, on the Watcher owner's own
//     connection for this Crew;
//   - a page's text in the Crew's Chrome, as a backup when an app's connection fails (owner, 21 Nov: "it will use
//     only browser extension and work when its connected"): open one address in a tab of its own, read the text,
//     close the tab. No clicks or typing.
// The server makes each call with the owner's connection; no token reaches the script. Platform tools are refused.

const watcherReadSessionPrefix = "watcher-read-"

type watcherReadScope struct {
	jobID         string
	userID        string
	workspacePath string
	// expires is the check's own time limit with a margin: a check killed before its stop runs cannot leave a live
	// session (review, 5 Nov).
	expires time.Time
}

// watcherReadSessionLife is how long a check's Slack session lives at most: the check's 60-second limit plus margin.
const watcherReadSessionLife = 2 * time.Minute

// liveWatcherReadScope is the check's session while it is still live; an expired one is removed.
func (api *StreamingAPI) liveWatcherReadScope(sessionID string) (watcherReadScope, bool) {
	cached, found := api.watcherReadSessions.Load(sessionID)
	if !found {
		return watcherReadScope{}, false
	}
	scope := cached.(watcherReadScope)
	if time.Now().After(scope.expires) {
		api.watcherReadSessions.Delete(sessionID)
		log.Printf("[WATCHER_READ] job=%s result=refused reason=session_expired", scope.jobID)
		return watcherReadScope{}, false
	}
	return scope, true
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

// openWatcherReadSession gives one check its bridge session; the returned stop ends it (later calls are refused).
func (api *StreamingAPI) openWatcherReadSession(job productScheduleJob, root string) (map[string]string, func()) {
	base := strings.TrimRight(os.Getenv("MCP_API_URL"), "/")
	if api == nil || base == "" {
		return nil, func() {}
	}
	session := watcherReadSessionPrefix + uuid.NewString()
	api.watcherReadSessions.Store(session, watcherReadScope{jobID: job.ID(), userID: job.UserID, workspacePath: root, expires: time.Now().Add(watcherReadSessionLife)})
	env := map[string]string{"MCP_API_URL": base + "/s/" + session, "MCP_SESSION_ID": session}
	common.PopulateMCPBridgeShortEnv(env) // the session's own token
	delete(env, "MCP_CUSTOM")             // Reading apps only: no platform tools, no virtual tools
	delete(env, "MCP_VIRTUAL")
	return env, func() { api.watcherReadSessions.Delete(session) }
}

// resolveWatcherReadMCPServer scopes a Watcher check's bridge calls to read tools of the Crew's selected servers, as
// its owner, rechecked on every call. ok=false means the session is not a Watcher's.
func (api *StreamingAPI) resolveWatcherReadMCPServer(ctx context.Context, sessionID, server, tool string) (*executor.ResolvedMCPServer, bool, error) {
	if !strings.HasPrefix(sessionID, watcherReadSessionPrefix) {
		return nil, false, nil
	}
	scope, found := api.liveWatcherReadScope(sessionID)
	if !found {
		return nil, true, fmt.Errorf("this Watcher check has finished")
	}
	// Whether the tool reads is decided per call by watcherReadGuard, from the app's own tool list.
	selection, err := reportRunSelectionFor(ctx, scope.workspacePath)
	if err != nil {
		log.Printf("[WATCHER_READ] job=%s tool=%s result=refused reason=selection_unavailable", scope.jobID, tool)
		return nil, true, fmt.Errorf("this project's apps are unavailable")
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
	log.Printf("[WATCHER_READ] job=%s tool=%s result=%s", scope.jobID, tool, result)
	return resolved, true, err
}

// watcherReadPerson is the Watcher's owner for a check's session ("" when it is not one, or it has finished).
func (api *StreamingAPI) watcherReadPerson(sessionID string) (string, bool) {
	if !strings.HasPrefix(sessionID, watcherReadSessionPrefix) {
		return "", false
	}
	if scope, ok := api.liveWatcherReadScope(sessionID); ok {
		return scope.userID, true
	}
	return "", true
}

func init() { toolguard.Set(watcherReadGuard) }

// watcherReadGuard: a Watcher check's app call runs only when the tool reads. Every other session is untouched.
func watcherReadGuard(ctx context.Context, call toolguard.Call) toolguard.Decision {
	if !strings.HasPrefix(call.SessionID, watcherReadSessionPrefix) {
		return toolguard.Decision{}
	}
	refuse := func(reason string) toolguard.Decision {
		log.Printf("[WATCHER_READ] tool=%s result=refused reason=%s", call.Tool, reason)
		return toolguard.Decision{Stub: true, IsError: true, Result: "A Watcher can only read. " + call.Tool + " is not marked as read-only by its app, so a Watcher cannot use it; use a list, get or search tool instead."}
	}
	if call.Kind != toolguard.KindMCP {
		return refuse("not_an_app_tool")
	}
	if watcherSlackToolAllowed(call.Tool) {
		return toolguard.Decision{}
	}
	if call.Annotations == nil {
		return refuse("no_annotations")
	}
	annotations, found, err := call.Annotations(ctx)
	if err != nil || !found || annotations.ReadOnlyHint == nil || !*annotations.ReadOnlyHint {
		return refuse("not_marked_read_only")
	}
	return toolguard.Decision{}
}

// watcherBridgeRefusesPlatformTools: a Watcher check's session reaches apps only, never platform tools.
func watcherBridgeRefusesPlatformTools(w http.ResponseWriter, sessionID string) bool {
	if !strings.HasPrefix(sessionID, watcherReadSessionPrefix) {
		return false
	}
	writeUsersJSON(w, http.StatusForbidden, map[string]any{"success": false, "error": "A Watcher check can read connected apps only."})
	return true
}

// watcherGoogleServer is the bridge name a detector uses for Google: POST <bridge>/google/gog {"args": [...]}.
const watcherGoogleServer = "google"

// serveWatcherGoogle runs one read-only gog command for a live Watcher check, as its owner, in its Crew.
func (api *StreamingAPI) serveWatcherGoogle(w http.ResponseWriter, r *http.Request, sessionID, server string) bool {
	if server != watcherGoogleServer || !strings.HasPrefix(sessionID, watcherReadSessionPrefix) {
		return false
	}
	reply := func(status int, result, failure string) {
		writeUsersJSON(w, status, map[string]any{"success": failure == "", "result": result, "error": failure})
	}
	scope, found := api.liveWatcherReadScope(sessionID)
	if !found {
		reply(http.StatusForbidden, "", "this Watcher check has finished")
		return true
	}
	var body struct {
		Args         []string `json:"args"`
		ConnectionID string   `json:"connection_id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&body); err != nil || len(body.Args) == 0 {
		reply(http.StatusBadRequest, "", `send {"args": ["gmail", "search", "..."]}: the gog arguments, starting with gmail, drive, sheets, docs, slides or calendar`)
		return true
	}
	ctx := context.WithValue(r.Context(), common.UserIDKey, scope.userID)
	out, err := services.RunGoogleCLIIn(ctx, strings.TrimSpace(body.ConnectionID), body.Args, services.GmailUseScope{Workspace: scope.workspacePath, UserID: scope.userID, ReadOnly: true})
	service := strings.ToLower(strings.TrimSpace(body.Args[0]))
	result := "ok"
	failure := ""
	if err != nil {
		result, failure = "refused", err.Error()
	}
	log.Printf("[WATCHER_READ] job=%s google=%s result=%s", scope.jobID, service, result)
	reply(http.StatusOK, out, failure)
	return true
}

// watcherBrowserServer is the bridge name for the browser backup: POST <bridge>/browser/read {"url": "https://..."}.
const watcherBrowserServer = "browser"

// watcherBrowserTextLimit caps the page text a check gets back.
const watcherBrowserTextLimit = 50000

// serveWatcherBrowser reads one page's text in the Crew's Chrome for a live Watcher check, as its owner.
func (api *StreamingAPI) serveWatcherBrowser(w http.ResponseWriter, r *http.Request, sessionID, server string) bool {
	if server != watcherBrowserServer || !strings.HasPrefix(sessionID, watcherReadSessionPrefix) {
		return false
	}
	reply := func(status int, result, failure string) {
		writeUsersJSON(w, status, map[string]any{"success": failure == "", "result": result, "error": failure})
	}
	scope, found := api.liveWatcherReadScope(sessionID)
	if !found {
		reply(http.StatusForbidden, "", "this Watcher check has finished")
		return true
	}
	var body struct {
		URL string `json:"url"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&body)
	page, err := url.Parse(strings.TrimSpace(body.URL))
	if err != nil || (page.Scheme != "https" && page.Scheme != "http") || page.Host == "" {
		reply(http.StatusBadRequest, "", `send {"url": "https://..."}: the page to read`)
		return true
	}
	text, err := watcherBrowserRead(r.Context(), scope, page.String())
	result, failure := "ok", ""
	if err != nil {
		result, failure = "refused", err.Error()
	}
	log.Printf("[WATCHER_READ] job=%s browser=%s result=%s bytes=%d", scope.jobID, page.Host, result, len(text))
	reply(http.StatusOK, text, failure)
	return true
}

// watcherBrowserRead opens url in a tab of its own in the Crew's Chrome (the extension only, and only while it is
// connected), returns the page's visible text and closes the tab. The person's own tabs are never touched.
func watcherBrowserRead(ctx context.Context, scope watcherReadScope, page string) (string, error) {
	ctx = context.WithValue(ctx, common.UserIDKey, scope.userID)
	workspace := scope.workspacePath
	physical := workspace
	if ref, ok := resolveCrewPath(ctx, scope.userID, workspace); ok {
		physical = ref.Path()
	}
	settings, err := readWorkspaceBrowserSettings(ctx, physical)
	if err != nil || settings.Mode != "extension" {
		return "", errors.New("this project's browser is not Chrome with the AgentWorks extension")
	}
	browserScope := browserSessionForWorkspace(scope.userID, workspace)
	if browserScope == "" || !browserrelay.Default.Status(scope.userID, browserScope).Connected {
		return "", errors.New("Chrome is not connected")
	}
	owner := "watcher-browser-" + uuid.NewString()
	bindConversationBrowserIsolation(owner, scope.userID, workspace, nil)
	defer common.ClearSessionShellConfig(owner)
	// Runs first: closes only the tab this read opened, while the owner's browser binding still resolves.
	defer browser.ReleaseExtensionConversation(context.Background(), owner)
	run := virtualtools.CreateWorkspaceBrowserToolExecutorsWithRuntime(owner, browser.NewBrowserRuntimeConfig("extension", nil))["agent_browser"]
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if _, err := run(ctx, map[string]interface{}{"command": "open", "args": []interface{}{page}}); err != nil {
		return "", fmt.Errorf("could not open the page in Chrome: %w", err)
	}
	out, err := run(ctx, map[string]interface{}{"command": "eval", "args": []interface{}{fmt.Sprintf("document.body ? document.body.innerText.slice(0, %d) : ''", watcherBrowserTextLimit)}})
	if err != nil {
		return "", fmt.Errorf("could not read the page: %w", err)
	}
	var envelope struct {
		Data struct {
			Result string `json:"result"`
		} `json:"data"`
	}
	text := out
	if json.Unmarshal([]byte(out), &envelope) == nil && envelope.Data.Result != "" {
		text = envelope.Data.Result
	}
	if len(text) > watcherBrowserTextLimit {
		text = text[:watcherBrowserTextLimit]
	}
	return text, nil
}
