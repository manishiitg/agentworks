package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/browser"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/wsauth"
	"github.com/manishiitg/coding-agent-loop/workspace/models"
)

type crewChatContextRequest struct {
	Source          string `json:"source"`
	Operation       string `json:"operation"`
	ConversationKey string `json:"conversation_key"`
	SessionID       string `json:"session_id"`
	BrowserSession  string `json:"browser_session"`
	TerminalTab     int    `json:"terminal_tab"`
	CommandID       string `json:"command_id"`
	Text            string `json:"text"`
	StartLine       int    `json:"start_line"`
	EndLine         int    `json:"end_line"`
	models.BrowserChatContextRequest
}

type crewChatContextFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Type string `json:"type"`
}

// A selection produces ordinary completed chat uploads, never a send, queue,
// browser navigation or terminal input. The UI stages the returned files only
// when the person chooses Add to chat. Cancelled previews can leave ordinary
// unused uploads, just like cancelling after choosing a file in the composer.
func (api *StreamingAPI) handleCrewChatContext(w http.ResponseWriter, r *http.Request) {
	var in crewChatContextRequest
	project := strings.TrimSpace(mux.Vars(r)["project_id"])
	actor, sourceID, reason, ok, size := "", "", "invalid_request", false, 0
	auditProject, auditChat, auditSession, auditSource, auditOperation := "", "", "", "", ""
	defer func() {
		log.Printf("[CHAT_CONTEXT_ADD] actor=%q crew=%q chat=%q session=%q source=%q source_id=%q op=%q bytes=%d ok=%t reason=%s", actor, auditProject, auditChat, auditSession, auditSource, sourceID, auditOperation, size, ok, reason)
	}()
	if mux.Vars(r)["id"] != "work" || json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&in) != nil || (in.Source != "browser" && in.Source != "terminal") || (in.Operation != "probe" && in.Operation != "add") {
		writeAgentProfileError(w, 400, "Choose an element in this Crew's current browser.")
		return
	}
	auditSource, auditOperation = in.Source, in.Operation
	claims := GetUserFromContext(r.Context())
	if claims == nil || claims.UserID == "" || userAccessForClaims(claims).Disabled {
		reason = "account"
		writeAgentProfileError(w, 403, "Sign in to an available account, then add the selection again.")
		return
	}
	actor = claims.UserID
	crew, _, _, found := api.externalCrewResolve(r.Context(), claims, project)
	if !found {
		reason = "crew_access"
		writeAgentProfileError(w, 404, "Open an available Crew, then add the selection again.")
		return
	}
	auditProject = project
	if token := claims.AccessToken; token != nil && !token.Allows("crews:run") && !token.Allows("crews:write") {
		reason = "scope"
		writeAgentProfileError(w, 403, "Allow this connection to use the Crew, then try again.")
		return
	}
	// Source access is independent of being able to upload a file in Run mode.
	// Capturing the owner's browser does not follow from receiving a dashboard.
	workspace := crew.Binding.WorkspacePath
	if !crew.OwnedByCaller || (in.Source == "browser" && !api.canControlLiveBrowser(r.Context(), claims, workspace)) {
		reason = "source_access"
		writeAgentProfileError(w, 403, "Only this Crew's owner can add its context.")
		return
	}
	if status, _ := externalCrewAuthorGate(claims); status != 0 {
		reason = "upload_access"
		writeAgentProfileError(w, status, "This account cannot add Crew files. Ask an administrator to allow uploads.")
		return
	}
	if !api.admitPendingChatUpload(w, r, project, in.ConversationKey, in.SessionID) {
		reason = "chat_access"
		return
	}
	auditChat, auditSession = in.ConversationKey, in.SessionID
	if in.Source == "terminal" {
		sourceID, size, ok, reason = api.addTerminalChatContext(w, r, claims, project, workspace, in)
		return
	}
	sourceID = browserSessionForWorkspace(actor, workspace)
	if sourceID == "" || (in.BrowserSession != "" && in.BrowserSession != sourceID) || !browser.IsUserBrowserSession(sourceID) {
		reason = "source_binding"
		writeAgentProfileError(w, 409, "Choose the current live browser in this Crew, then pick the element again.")
		return
	}
	view := r.Clone(r.Context())
	address := *r.URL
	query := address.Query()
	query.Set("workspace_path", workspace)
	address.RawQuery = query.Encode()
	view.URL = &address
	active := false
	for _, item := range api.liveBrowserSessions(view) {
		if item["browser_session"] == sourceID {
			active = true
			break
		}
	}
	if !active {
		reason = "source_unavailable"
		writeAgentProfileError(w, 409, "Open this Crew's browser, then pick the element again.")
		return
	}
	if (in.Operation == "probe" && (in.X != nil || in.Y != nil)) || (in.Operation == "add" && (in.BrowserSession == "" || in.X == nil || in.Y == nil || len(in.DocumentToken) != 64)) {
		writeAgentProfileError(w, 400, "Pick the element again before adding it.")
		return
	}
	release, available := browser.TryTakeBrowserControl(sourceID)
	if !available {
		reason = "browser_busy"
		writeAgentProfileError(w, 409, "The browser is busy. Pick the element again when it is ready.")
		return
	}
	defer release()
	capture, err := requestBrowserChatContext(r.Context(), sourceID, actor, in.BrowserChatContextRequest)
	if err != nil {
		reason = "capture_changed_or_unavailable"
		writeAgentProfileError(w, 409, "The page changed or this element could not be captured. Pick it again.")
		return
	}
	capture.BrowserSession = sourceID
	if in.Operation == "probe" {
		ok, reason = true, "ready"
		writeAgentProfileJSON(w, 200, capture)
		return
	}
	if !api.canControlLiveBrowser(r.Context(), claims, workspace) {
		reason = "access_changed"
		writeAgentProfileError(w, 403, "Browser access changed. Open an available Crew, then try again.")
		return
	}
	if !api.admitPendingChatUpload(w, r, project, in.ConversationKey, in.SessionID) {
		reason = "access_changed"
		return
	}
	image, err := base64.StdEncoding.DecodeString(capture.ImageBase64)
	if err != nil || len(image) == 0 || len(image) > 2<<20 {
		reason = "invalid_capture"
		writeAgentProfileError(w, 502, "The element image was unavailable. Pick it again.")
		return
	}
	id := uuid.NewString()
	imageName := "page-element-" + id + ".png"
	metadata, _ := json.MarshalIndent(map[string]any{"source": "browser", "untrusted_context": true, "url": capture.URL, "selector": capture.Selector, "text": capture.Text, "rect": capture.Rect, "screenshot": imageName}, "", "  ")
	files := []crewChatContextFile{}
	for _, file := range []struct {
		name string
		data []byte
	}{{imageName, image}, {"page-element-" + id + ".json", metadata}} {
		rel := path.Join("uploads", file.name)
		buffer := &externalBufferedResponse{}
		api.externalWriteCrewFile(buffer, r, claims, map[string]any{"crew_id": project, "path": rel, "content_base64": base64.StdEncoding.EncodeToString(file.data)})
		if buffer.status != http.StatusOK {
			reason = "upload_failed"
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(buffer.status)
			_, _ = w.Write(buffer.body.Bytes())
			return
		}
		size += len(file.data)
		files = append(files, crewChatContextFile{Name: file.name, Path: path.Join(workspace, rel), Type: "file"})
	}
	capture.ImageBase64 = ""
	ok, reason = true, "attached_files_ready"
	writeAgentProfileJSON(w, 200, map[string]any{"source": "browser", "capture": capture, "files": files, "preview_path": files[0].Path})
}

func requestBrowserChatContext(ctx context.Context, session, actor string, input models.BrowserChatContextRequest) (models.BrowserChatContext, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	body, _ := json.Marshal(input)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(workspaceBrowserEndpoint(), "/")+"/api/browser/live/"+url.PathEscape(session)+"/context", bytes.NewReader(body))
	if err != nil {
		return models.BrowserChatContext{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(wsauth.HeaderName, wsauth.Token())
	req.Header.Set("X-User-ID", actor)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		return models.BrowserChatContext{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return models.BrowserChatContext{}, fmt.Errorf("capture unavailable")
	}
	var result models.BrowserChatContext
	if json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&result) != nil {
		return result, fmt.Errorf("invalid capture response")
	}
	return result, nil
}

// The experience catalog exposes this same handler to chat/MCP. It returns
// attachment references, never a chat send or an automatic composer action.
func (api *StreamingAPI) externalWorkspaceAddContext(w http.ResponseWriter, r *http.Request, args map[string]any) {
	project := workspaceTokenProjectID(externalArg(args, "workspace_id"))
	if project == "" {
		externalError(w, 400, "invalid_arguments", "Choose a Crew workspace before adding context.")
		return
	}
	body, err := json.Marshal(map[string]any{"source": externalArg(args, "context_source"), "operation": externalArg(args, "context_op"), "conversation_key": externalArg(args, "conversation_key"), "session_id": externalArg(args, "session_id"), "browser_session": externalArg(args, "browser_session"), "document_token": externalArg(args, "document_token"), "x": args["x"], "y": args["y"], "terminal_tab": args["terminal_tab"], "command_id": externalArg(args, "command_id"), "text": externalArg(args, "text"), "start_line": args["start_line"], "end_line": args["end_line"]})
	if err != nil {
		externalError(w, 400, "invalid_arguments", "Use a current browser selection.")
		return
	}
	request := r.Clone(r.Context())
	request.Body = io.NopCloser(bytes.NewReader(body))
	request = mux.SetURLVars(request, map[string]string{"id": "work", "project_id": project})
	api.handleCrewChatContext(w, request)
}
