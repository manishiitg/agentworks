package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Published agent definitions live in server state, separately from personal
// chat slots. A Builder opens a private slot bound to this project-level ID.
func publicationAgentID(key string) string {
	_, side, ok := strings.Cut(key, codeChatSideMarker+"agent-")
	if !ok {
		return ""
	}
	if _, err := uuid.Parse(side); err != nil {
		return ""
	}
	return side
}

func (api *StreamingAPI) publicationTarget(ctx context.Context, claims *UserClaims, profileID, workspaceID, agentID, projectID, name string) (*publishedProject, error) {
	if len([]rune(strings.TrimSpace(name))) > productAgentNameMaxRunes {
		return nil, fmt.Errorf("Give the Crew or agent a name of at most %d characters.", productAgentNameMaxRunes)
	}
	if err := publicationGate(claims); err != nil {
		return nil, err
	}
	var choice *publishedProject
	if workspaceID != "" {
		binding, manifest, ok := api.resolveExternalWorkspace(ctx, claims, workspaceID)
		if !ok || !binding.OwnedByCaller || !publicationOriginTokenAllowed(claims, manifest.Product, manifest.ID) {
			return nil, fmt.Errorf("Choose a Crew you may build, or publish a new Crew.")
		}
		if codeProjectMode(ctx, binding.Binding.WorkspacePath) == "local" {
			return nil, fmt.Errorf("Choose a hosted Crew. Your Local project remains on your computer.")
		}
		root := agentProfileRuntimeWorkspace(binding.OwnerID, binding.Binding.WorkspacePath)
		agents, err := publishedAgents(ctx, root)
		if err != nil {
			return nil, err
		}
		for _, p := range agents {
			if (agentID == "" || agentID == p.AgentID || agentID == "main" && p.AgentID == "") && p.ProjectID == projectID {
				copy := p
				choice = &copy
				break
			}
		}
		if choice == nil && agentID != "" {
			return nil, fmt.Errorf("The publish link belongs to a different agent. Choose the matching agent or add a new one.")
		}
		if choice == nil {
			choice, err = api.publicationChoice(ctx, claims, manifest.Product, true)
			if err != nil {
				return nil, err
			}
			choice.Root, choice.WorkspaceID, choice.OwnerID = root, manifest.ID, binding.OwnerID
			choice.AgentID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(root+"\x00"+projectID)).String()
			// The project agent is new. Its private tab still counts against the ordinary cap.
			chats, err := defaultProductConversationRegistryStore().projectChats(ctx, claims.UserID, manifest.Product, manifest.ID)
			if err != nil {
				return nil, err
			}
			n := 0
			for _, chat := range chats {
				if isSideChatConversationKey(chat.ConversationKey) {
					n++
				}
			}
			if n >= externalCrewMaxSideChats {
				return nil, fmt.Errorf("Close one of this Crew's %d side chats, then add the agent.", externalCrewMaxSideChats)
			}
		}
	} else {
		var err error
		choice, err = api.findPublishedProject(ctx, claims, projectID)
		if err != nil {
			return nil, err
		}
		if choice == nil {
			var err error
			choice, err = api.publicationChoice(ctx, claims, profileID)
			if err != nil {
				return nil, err
			}
		}
	}
	if !publicationOriginTokenAllowed(claims, choice.ProfileID, choice.WorkspaceID) {
		return nil, fmt.Errorf("This connection needs Builder access to publish here.")
	}
	choice.Name, choice.PublisherID = strings.TrimSpace(name), sanitizeUserIDForPath(claims.UserID)
	return choice, nil
}

func publishedAgents(ctx context.Context, root string) ([]publishedProject, error) {
	state, err := publicationStateRoot()
	if err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(state, "projects", "*.json"))
	if err != nil {
		return nil, err
	}
	result := []publishedProject{}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		var p publishedProject
		if json.Unmarshal(raw, &p) != nil || canonicalCrewWorkspaceRoot(p.Root) != canonicalCrewWorkspaceRoot(root) {
			continue
		}
		checked, err := readPublishedAgent(contextWithoutPublication(ctx), root, p.AgentID)
		if err != nil {
			return nil, err
		}
		if checked != nil {
			result = append(result, *checked)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AgentID < result[j].AgentID })
	return result, nil
}

func (api *StreamingAPI) openPublishedAgent(r *http.Request, p *publishedProject) (ProductConversationRecord, error) {
	claims := GetUserFromContext(r.Context())
	binding, _, ok := api.resolveExternalWorkspace(r.Context(), claims, p.ProfileID+":"+p.WorkspaceID)
	if !ok || !binding.OwnedByCaller || !userAccessForClaims(claims).CanEdit {
		return ProductConversationRecord{}, fmt.Errorf("Open this agent from a Crew you may build. Shared use continues through the Crew's dashboard and functions.")
	}
	profile, err := api.agentProfiles.Resolve(p.ProfileID, 0, claims.UserID)
	if err != nil {
		return ProductConversationRecord{}, err
	}
	chat, err := api.resolveAgentProfileConversation(r, profile, p.conversationKey())
	if err != nil {
		return chat, err
	}
	name := p.Name
	purpose := "Published from your computer; continues from a private summary."
	_, err = defaultProductConversationRegistryStore().setAgentIdentity(r.Context(), claims.UserID, p.ProfileID, chat.ConversationKey, &name, &purpose, claims.UserID)
	if err == nil {
		err = RenameChatHistorySession(claims.UserID, chat.SessionID, binding.Binding.WorkspacePath, name)
	}
	return chat, err
}

func (api *StreamingAPI) externalPublishedAgents(w http.ResponseWriter, r *http.Request, args map[string]any, binding crewProjectBinding, manifest productProjectManifest) {
	claims := GetUserFromContext(r.Context())
	if !binding.OwnedByCaller || !userAccessForClaims(claims).CanEdit || !publicationOriginTokenAllowed(claims, manifest.Product, manifest.ID) {
		externalError(w, 403, "forbidden", "Open an agent from a Crew you may build. You can use shared work through its dashboard and functions.")
		return
	}
	root := agentProfileRuntimeWorkspace(binding.OwnerID, binding.Binding.WorkspacePath)
	agents, err := publishedAgents(r.Context(), root)
	if err != nil {
		externalFailure(w, err)
		return
	}
	if externalArg(args, "action") == "agents" {
		out := []map[string]any{}
		for _, p := range agents {
			id := p.AgentID
			if id == "" {
				id = "main"
			}
			out = append(out, map[string]any{"agent_id": id, "name": p.Name, "revision": p.Revision, "continuity": "summary", "source": "published", "account": map[string]any{"provider": p.Provider, "model": p.ModelID, "kind": "company"}})
		}
		externalJSON(w, map[string]any{"workspace_id": manifest.Product + ":" + manifest.ID, "agents": out})
		return
	}
	id := externalArg(args, "agent_id")
	if id == "main" {
		id = ""
	}
	for _, p := range agents {
		if p.AgentID != id {
			continue
		}
		chat, err := api.openPublishedAgent(r, &p)
		if err != nil {
			externalError(w, 409, "chat_unavailable", err.Error())
			return
		}
		externalJSON(w, map[string]any{"workspace_id": manifest.Product + ":" + manifest.ID, "agent_id": externalArg(args, "agent_id"), "chat_id": strings.TrimPrefix(chat.ConversationKey, manifest.ID+codeChatSideMarker), "conversation_key": chat.ConversationKey, "conversation_id": chat.ConversationID, "session_id": chat.SessionID, "name": p.Name, "provider": p.Provider, "model_id": p.ModelID, "connection_id": p.ConnectionID})
		return
	}
	externalError(w, 404, "not_found", "Choose an agent with workspace action=agents.")
}
