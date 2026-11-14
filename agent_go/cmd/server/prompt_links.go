package server

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

const promptLinkMaxText = 8000
const promptLinkMaxURL = 8192

func promptLinkTextOK(text string) bool {
	return strings.TrimSpace(text) != "" && utf8.ValidString(text) && utf8.RuneCountInString(text) <= promptLinkMaxText
}

// A prompt link carries text and identities only. Opening delegates to the
// ordinary Ask resolver, which creates no turn and preserves caller ownership.
func (api *StreamingAPI) handlePromptLinkOpen(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if !externalWorkspaceAllowed(claims) || claims.Scope != "" || claims.Provider == "bot_route" {
		log.Printf("[PROMPT_LINK] opened crew=unknown agent=unknown allowed=false reason=sign_in_required")
		externalError(w, 403, "forbidden", "Sign in to open this prompt. A link does not give access to a Crew.")
		return
	}
	var input struct {
		Crew  string `json:"crew"`
		Agent string `json:"agent"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		externalError(w, 400, "invalid_arguments", "Choose the Crew and agent for this prompt.")
		return
	}
	crew, agent := workspaceTokenProjectID(input.Crew), strings.TrimSpace(input.Agent)
	if crew == "" {
		own, err := api.ensureAgentWorks(r.Context(), claims.UserID)
		if err != nil {
			externalError(w, 409, "unavailable", "Could not open your AgentWorks chat. Return to your workspace and try again.")
			return
		}
		crew = own.CrewID
	}
	if !productConversationKeyPattern.MatchString(crew) || len(crew) > 256 || len(agent) > 512 || strings.ContainsAny(agent, "\r\n\x00/\\") {
		log.Printf("[PROMPT_LINK] opened crew=invalid agent=invalid allowed=false")
		externalError(w, 400, "invalid_arguments", "This prompt link has an invalid Crew or agent. Choose one from your workspace.")
		return
	}
	allowed := false
	defer func() {
		log.Printf("[PROMPT_LINK] opened crew=%s agent=%s allowed=%t", crew, logPromptLinkAgent(agent), allowed)
	}()
	project, manifest, ok := api.readExternalWorkspace(r.Context(), claims, crew)
	if !ok {
		externalError(w, 404, "not_found", "This Crew is unavailable. Ask its owner for access, or choose a Crew from your workspace.")
		return
	}
	key := agent
	if key == "" || key == "main" {
		key = crewOwnerMainChatKey(crew)
	} else if !strings.HasPrefix(key, crew+codeChatSideMarker) {
		key = crewAgentPublishKey(crew, key)
	}
	if !ordinaryProjectChatKey(crew, key) {
		externalError(w, 404, "agent_unavailable", "Choose an agent you can use in this Crew.")
		return
	}
	raw, _ := json.Marshal(map[string]any{"workspace": project.Binding.WorkspacePath, "agent_key": key})
	next := r.Clone(r.Context())
	next.Body = io.NopCloser(bytes.NewReader(raw))
	next.ContentLength = int64(len(raw))
	response := httptest.NewRecorder()
	api.handleProjectAskConversation(response, next)
	var body map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	allowed = response.Code == http.StatusOK && body["conversation"] != nil
	if response.Code == http.StatusOK && !allowed {
		externalError(w, 404, "agent_unavailable", "This agent is unavailable. Choose an agent you can use from your workspace.")
		return
	}
	if allowed {
		body["crew_name"] = manifest.displayTitle()
		conversation, _ := body["conversation"].(map[string]any)
		title, _ := conversation["title"].(string)
		body["agent_name"] = firstNonEmptyTrimmed(title, "Agent")
		externalJSON(w, body)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(response.Code)
	_, _ = w.Write(response.Body.Bytes())
}

func logPromptLinkAgent(agent string) string {
	if agent == "" {
		return "main"
	}
	// IDs in logs only; whitespace cannot forge another event.
	return strings.Map(func(r rune) rune {
		if r <= ' ' || r == 127 {
			return '_'
		}
		return r
	}, agent)
}

func (api *StreamingAPI) handlePromptLinkCreate(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Crew   string `json:"crew"`
		Agent  string `json:"agent"`
		Prompt string `json:"prompt"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF {
		externalError(w, 400, "invalid_arguments", "Supply a Crew, agent and prompt.")
		return
	}
	api.createPromptLink(w, r, input.Crew, input.Agent, input.Prompt)
}

// The native UI and workspace prompt_link action share this read-only path.
func (api *StreamingAPI) createPromptLink(w http.ResponseWriter, r *http.Request, crew, agent, prompt string) {
	w.Header().Set("Cache-Control", "no-store")
	claims := GetUserFromContext(r.Context())
	if !externalWorkspaceAllowed(claims) {
		externalError(w, 403, "forbidden", "Use a Crew you can access to make a prompt link.")
		return
	}
	if !promptLinkTextOK(prompt) {
		externalError(w, 400, "invalid_prompt", "Write a prompt of up to 8,000 characters. A prompt link opens a draft and sends nothing.")
		return
	}
	project, manifest, ok := api.readExternalWorkspace(r.Context(), claims, crew)
	if !ok {
		externalError(w, 404, "not_found", "Choose a Crew you can use from your workspace.")
		return
	}
	chats, err := api.workspaceOwnerChats(r, claims.UserID, manifest.ID, project.Binding)
	if err != nil {
		externalError(w, 503, "unavailable", "Could not load this Crew's agents. Try again.")
		return
	}
	if agent == "" {
		agent = "main"
	}
	var chat *codeChat
	for i := range chats {
		if chats[i].Key == agent || chats[i].ID == agent || agent == "main" && chats[i].IsMain {
			// Keep the exact key, including a teammate's own Main. The general
			// chat-name resolver rewrites Main to a Crew-wide alias.
			chat = &chats[i]
			break
		}
	}
	if chat == nil {
		externalError(w, 404, "agent_unavailable", "Choose an existing agent you can use in this Crew.")
		return
	}
	base := strings.TrimRight(effectiveShareBaseURL(), "/")
	id := manifest.ID
	if IsLocalMode() {
		if binding, err := crewroot.ReadSync(id); err == nil && binding != nil && binding.Side == "local" && binding.PeerServer != "" {
			base, id = strings.TrimRight(binding.PeerServer, "/"), binding.PeerProjectID
		}
	}
	if base == "" {
		externalError(w, 409, "server_required", "Connect this Crew to a server before copying a prompt link.")
		return
	}
	agentID := strings.TrimPrefix(chat.Key, manifest.ID+codeChatSideMarker)
	if chat.Key == manifest.ID {
		agentID = "main"
	}
	link := base + "/new?" + url.Values{"prompt": {prompt}, "crew": {id}, "agent": {agentID}}.Encode()
	if len(link) > promptLinkMaxURL {
		externalError(w, 400, "prompt_too_long", "This prompt is too long for a link. Shorten it and try again.")
		return
	}
	externalJSON(w, map[string]any{"url": link, "next": "The link grants no access and sends nothing. Its recipient reviews the draft and presses Send."})
}
