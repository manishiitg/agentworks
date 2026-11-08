package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/skills"
)

// The person's own AgentWorks agent (PLAT-825-99): an ordinary Crew the platform creates once per person and marks
// with role "agentworks". It works in its main chat only (tabs are made only by the person). It runs no routine of its
// own: it reaches the person's other agents and Crews, does what they ask, and sets up a schedule only when asked. It
// cannot be deleted.

// agentWorksRole is the product.json role of the person's AgentWorks Crew.
const agentWorksRole = "agentworks"

const agentWorksName = "AgentWorks"

// agentWorksBuiltinSkills are the platform's AgentWorks skills (internal/workproduct/skills/agentworks-*).
var agentWorksBuiltinSkills = []string{
	"agentworks-inspect-crew", "agentworks-review-architecture",
	"agentworks-review-qa", "agentworks-schedules-watchers", "agentworks-suggest-and-build",
}

var errAgentWorksDeleteRefused = errors.New("AgentWorks can't be deleted. It does nothing on its own, so there is nothing to pause; ask it in its chat to stop anything you don't want")

// agentWorksCrewID is the person's reserved AgentWorks identity, so two first visits can never make two.
func agentWorksCrewID(userID string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("agentworks\x00"+strings.TrimSpace(userID))).String()
}

var agentWorksLocks sync.Map

func lockAgentWorks(userID string) func() {
	value, _ := agentWorksLocks.LoadOrStore(strings.TrimSpace(userID), &sync.Mutex{})
	mutex := value.(*sync.Mutex)
	mutex.Lock()
	return mutex.Unlock
}

type agentWorksCrew struct {
	CrewID          string
	WorkspacePath   string
	ConversationKey string
	Created         bool
}

func agentWorksSpec() crewSpec {
	available := []string{}
	for _, name := range agentWorksBuiltinSkills {
		if skills.IsBuiltinSkillIn("Crew", name) {
			available = append(available, name)
		}
	}
	return crewSpec{
		Name:           agentWorksName,
		Purpose:        "Your own agent: reaches your other agents and Crews and does what you ask.",
		SelectedSkills: available,
		Files: map[string]string{
			"notes/overview.md": "# Overview (keep this file current)\n\nRead this at the start of a turn.\n\n## State\nNothing recorded yet.\n",
		},
	}
}

// ensureAgentWorks is the one place a person's AgentWorks is made and kept in shape: create it when missing (reserved
// id), re-add missing built-in skills. Every call site (first visit, the MCP action) goes through here.
func (api *StreamingAPI) ensureAgentWorks(ctx context.Context, userID string) (agentWorksCrew, error) {
	userID = strings.TrimSpace(userID)
	if api == nil || api.agentProfiles == nil || api.projectActions == nil || userID == "" {
		return agentWorksCrew{}, fmt.Errorf("AgentWorks is unavailable on this server")
	}
	ctx = internalBotRequestContext(ctx, userID)
	defer lockAgentWorks(userID)()
	crew := agentWorksCrew{CrewID: agentWorksCrewID(userID)}
	_, binding, manifest, err := api.projectActions.projectManifest(ctx, userID, crewProfileID, crew.CrewID)
	if err != nil {
		spec := agentWorksSpec()
		id, setupErr := api.createWorkspace(ctx, GetUserFromContext(ctx), crewProfileID, spec, true, workspaceCreation{ReservedID: crew.CrewID, Role: agentWorksRole})
		if id == "" {
			return crew, setupErr
		}
		if setupErr != nil {
			log.Printf("[AGENTWORKS] user=%s crew=%s created with setup warning: %v", userID, id, setupErr)
		}
		log.Printf("[AGENTWORKS] user=%s crew=%s created", userID, id)
		crew.Created = true
		if _, binding, manifest, err = api.projectActions.projectManifest(ctx, userID, crewProfileID, crew.CrewID); err != nil {
			return crew, fmt.Errorf("AgentWorks was created but could not be opened: %w", err)
		}
	}
	if manifest.Role != agentWorksRole {
		return crew, fmt.Errorf("AgentWorks is unavailable: its reserved project is not marked as AgentWorks")
	}
	crew.WorkspacePath = binding.WorkspacePath
	crew.ConversationKey = binding.ConversationKey
	if !crew.Created {
		missing := []string{}
		for _, name := range agentWorksSpec().SelectedSkills {
			if !slices.Contains(manifest.Capabilities.SelectedSkills, name) {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			if err := applyCrewCreationSelections(ctx, crewProfileID, crew.WorkspacePath, missing, nil, nil); err != nil {
				log.Printf("[AGENTWORKS] user=%s skills not re-added: %v", userID, err)
			}
		}
	}
	return crew, nil
}

// agentWorksSeen remembers who already has an AgentWorks in this process, so a visit costs nothing after the first.
var agentWorksSeen sync.Map

// ensureAgentWorksOnVisit gives every signed-in person an AgentWorks on their first visit, in the background.
func (api *StreamingAPI) ensureAgentWorksOnVisit(userID string) {
	userID = strings.TrimSpace(userID)
	if api == nil || api.projectActions == nil || userID == "" {
		return
	}
	if _, seen := agentWorksSeen.LoadOrStore(userID, true); seen {
		return
	}
	go func() {
		if _, err := api.ensureAgentWorks(context.Background(), userID); err != nil {
			log.Printf("[AGENTWORKS] user=%s not ensured on visit: %v", userID, err)
		}
	}()
}

// isAgentWorksCrew reports whether the project at root is someone's AgentWorks.
func isAgentWorksCrew(ctx context.Context, root string) bool {
	manifest, err := readCrewProjectManifests(ctx, crewProfileID, root)
	return err == nil && manifest.Role == agentWorksRole
}

// handleOpenAgentWorks is the app's AgentWorks entry: it returns (making it on first use) the signed-in person's own
// AgentWorks, the same ensure as a first visit and workspace action=agentworks. An optional {"message": "..."} is sent
// into its main chat as the person, through the app's own chat send (handleAgentProfileChatQuery), without waiting
// for the answer; the reply streams in the chat as usual.
func (api *StreamingAPI) handleOpenAgentWorks(w http.ResponseWriter, r *http.Request) {
	user := GetUserFromContext(r.Context())
	if user == nil || strings.TrimSpace(user.UserID) == "" {
		http.Error(w, "Sign in to open AgentWorks.", http.StatusUnauthorized)
		return
	}
	var input struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxAgentProfileRequestBytes)).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "Send your message as plain text.", http.StatusBadRequest)
		return
	}
	message := strings.TrimSpace(input.Message)
	if len([]rune(message)) > codeChatMessageMaxRune {
		http.Error(w, "That message is too long. Shorten it, or send the details in the chat in parts.", http.StatusBadRequest)
		return
	}
	crew, err := api.ensureAgentWorks(context.Background(), user.UserID)
	if err != nil {
		http.Error(w, "Your AgentWorks could not be opened: "+err.Error(), http.StatusConflict)
		return
	}
	out := map[string]any{"crew_id": crew.CrewID, "conversation_key": crew.ConversationKey, "name": agentWorksName, "created": crew.Created}
	if message != "" {
		// The owner's main chat is addressed by the project id, as the app's main chat send does.
		status, body := scheduleHandler(r, api.handleAgentProfileChatQuery, http.MethodPost, "/api/agent-profiles/"+crewProfileID+"/query", map[string]string{"id": crewProfileID}, nil, AgentProfileChatRequest{Message: message, ConversationKey: crew.CrewID})
		if status >= 300 {
			log.Printf("[AGENTWORKS] user=%s crew=%s message not sent: %d %s", user.UserID, crew.CrewID, status, strings.TrimSpace(string(body)))
			reason := "Open AgentWorks and try again in its chat."
			var failure struct{ Error, Message string }
			if json.Unmarshal(body, &failure) == nil && strings.TrimSpace(failure.Error+failure.Message) != "" {
				reason = strings.TrimSpace(failure.Error + " " + failure.Message)
			}
			http.Error(w, "Your message could not be sent to AgentWorks: "+reason, status)
			return
		}
		var receipt map[string]any
		if json.Unmarshal(body, &receipt) == nil {
			if queryID, _ := receipt["query_id"].(string); queryID != "" {
				out["query_id"] = queryID
			}
		}
		out["sent"] = true
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
