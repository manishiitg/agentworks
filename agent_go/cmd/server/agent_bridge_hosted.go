package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The existing named Bridge registry owns hosted identities too. This binding
// selects a logical tab, never a guessed live session or an agent-supplied path.
type hostedBridgeBinding struct {
	Profile string `json:"profile"`
	Project string `json:"project"`
	Chat    string `json:"chat"`
}

func (api *StreamingAPI) hostedBridgeChat(ctx context.Context, owner string, binding hostedBridgeBinding) (codeChatProject, codeChat, error) {
	empty := codeChatProject{}
	if owner == "" || binding.Profile != crewProfileID || binding.Project == "" || binding.Chat == "" || crewFunctionTabKey(binding.Chat) {
		return empty, codeChat{}, fmt.Errorf("app agent unavailable")
	}
	profile, err := api.agentProfiles.Resolve(binding.Profile, 0, owner)
	if err != nil {
		return empty, codeChat{}, err
	}
	project, owned, err := resolveConversationBindingForUser(ctx, owner, profile, binding.Project)
	if err != nil || !owned {
		return empty, codeChat{}, fmt.Errorf("app agent unavailable")
	}
	tabs, err := crewOwnerChatRecords(ctx, owner, binding.Profile, binding.Project, project.WorkspacePath)
	if err != nil {
		return empty, codeChat{}, err
	}
	for _, tab := range tabs {
		r := tab.Record
		if tab.OwnerID != owner || r.ConversationKey != binding.Chat || hiddenFromTabStrip(r.ConversationKey) {
			continue
		}
		chat := codeChat{OwnerID: owner, Key: r.ConversationKey, ID: r.ConversationKey, SessionID: r.SessionID, Name: crewDirectoryChatName(tab), Purpose: tab.Identity.Purpose}
		return codeChatProject{OwnerID: owner, ProfileID: binding.Profile, ProjectID: binding.Project, Workspace: project.WorkspacePath, Binding: project, Self: chat}, chat, nil
	}
	return empty, codeChat{}, fmt.Errorf("app agent was removed or replaced")
}

// Registration is lazy and has no attachment or borrowed website login. Only
// the backend can supply the registry binding; CLI/MCP names cannot claim it.
func (api *StreamingAPI) registerHostedBridgeChat(ctx context.Context, owner string, binding hostedBridgeBinding) (namedMessageAgent, error) {
	project, chat, err := api.hostedBridgeChat(ctx, owner, binding)
	if err != nil {
		return namedMessageAgent{}, err
	}
	return registerHostedBridgeRecord(ctx, owner, hostedBridgeBinding{Profile: project.ProfileID, Project: project.ProjectID, Chat: chat.Key}, chat)
}

// Discovery already read this owner's current tabs. Reuse those verified
// records rather than re-reading every Crew/registry once per directory row.
func registerHostedBridgeRecord(ctx context.Context, owner string, binding hostedBridgeBinding, chat codeChat) (namedMessageAgent, error) {
	if owner == "" || chat.OwnerID != owner || binding.Profile != crewProfileID || chat.Key != binding.Chat || chat.SessionID == "" || hiddenFromTabStrip(chat.Key) || crewFunctionTabKey(chat.Key) {
		return namedMessageAgent{}, fmt.Errorf("app agent unavailable")
	}
	return withTeamBridgeRegistration(ctx, binding.Project, owner, binding.Chat, func(boundID string) (namedMessageAgent, error) {
		address := bridgeChatAddress(owner, binding.Profile, binding.Project, chat.Key)
		_, name, _ := strings.Cut(strings.TrimPrefix(address, "agent:"), "/")
		agentMessageMu.Lock()
		defer agentMessageMu.Unlock()
		store, err := readAgentMessageStore(ctx)
		if err != nil {
			return namedMessageAgent{}, err
		}
		for i := range store.Agents {
			agent := &store.Agents[i]
			if agent.UserID != owner || (boundID != "" && agent.ID != boundID) || (boundID == "" && agent.Name != name) {
				continue
			}
			if agent.Hosted == nil {
				// The owner's Mac tab registered this source first. Reuse its
				// identity and transport, never mint a second hosted/copy agent.
				if boundID == "" || agent.Attachment == nil || agent.Attachment.DeliveryRoute != appAgentDeliveryRoute {
					return namedMessageAgent{}, fmt.Errorf("this Bridge identity belongs to another agent")
				}
				return *agent, nil
			}
			if *agent.Hosted != binding {
				return namedMessageAgent{}, fmt.Errorf("this Bridge identity belongs to another chat")
			}
			label, purpose := cleanAgentLabel(chat.Name), cleanAgentPurpose(chat.Purpose)
			if agent.Label != label || agent.Purpose != purpose {
				agent.Label, agent.Purpose = label, purpose
				if err := saveAgentMessageStore(ctx, store); err != nil {
					return namedMessageAgent{}, err
				}
			}
			return *agent, nil
		}
		if boundID != "" {
			return namedMessageAgent{}, fmt.Errorf("Team Bridge registration is unavailable")
		}
		agent := namedMessageAgent{ID: "agent-" + uuid.NewString(), UserID: owner, Name: name, Label: cleanAgentLabel(chat.Name), Purpose: cleanAgentPurpose(chat.Purpose), CreatedAt: time.Now().UTC(), Hosted: &binding}
		store.Agents = append(store.Agents, agent)
		if err := saveAgentMessageStore(ctx, store); err != nil {
			return namedMessageAgent{}, err
		}
		log.Printf("[AGENT-MESSAGE] event=registered agent=%s chat=%s", agent.ID, chat.Key)
		return agent, nil
	})
}

// Named identity/grant admission is performed on the unmodified conversation
// before this translates the current binding into a normal owner-chat turn.
// The copy never changes the durable thread endpoints or mints another agent.
func (api *StreamingAPI) hostedBridgeDeliveryEndpoint(ctx context.Context, endpoint agentMessageEndpoint) (agentMessageEndpoint, error) {
	agent, err := namedAgentByID(ctx, endpoint.UserID, endpoint.NamedAgentID)
	if err != nil || agent.Hosted == nil || endpoint.Hosted == nil || *agent.Hosted != *endpoint.Hosted {
		return endpoint, admissionRefused("hosted agent binding changed")
	}
	project, chat, err := api.hostedBridgeChat(ctx, agent.UserID, *agent.Hosted)
	if err != nil {
		return endpoint, admissionRefused("hosted agent chat is no longer available")
	}
	current := targetMessageEndpoint(agent.UserID, codeChatTarget(project, chat, project.Workspace))
	// This is only an execution projection. Reply tools recover the original
	// named registration from the current chat rather than using this shape.
	current.Hosted = nil
	current.BridgeAddress = endpoint.BridgeAddress
	current.Mode, current.Guest = endpoint.Mode, endpoint.Guest
	return current, nil
}

// A named identity is not proof that its app tab still exists. Check the
// current registry and owner, also when it is the sender or a reply endpoint.
func currentHostedBridgeRecord(ctx context.Context, owner string, binding hostedBridgeBinding) (ProductConversationRecord, error) {
	records, err := defaultProductConversationRegistryStore().projectChats(ctx, owner, binding.Profile, binding.Project)
	if err != nil {
		return ProductConversationRecord{}, err
	}
	for _, record := range records {
		if record.ConversationKey == binding.Chat && record.SessionID != "" && !hiddenFromTabStrip(record.ConversationKey) && !crewFunctionTabKey(record.ConversationKey) && crewProjectOwnedByCallerStrict(owner, record.WorkspacePath) {
			return record, nil
		}
	}
	return ProductConversationRecord{}, admissionRefused("hosted app chat was removed or its ownership changed")
}

// A registered identity does not turn a shared/Run turn into its owner's
// private agent. Like the Mac path, it may reply only in its admitted inbox
// or start a request to an already-public agent, never borrow private reach.
func (api *StreamingAPI) admitHostedBridgeToolTurn(caller triggerLinkCaller, session string, parent QueryRequest, inbox string, target triggerTarget, read bool) error {
	if caller.BridgeOrigin == nil || caller.OwnerAuthority {
		return nil
	}
	api.lastQueryMu.RLock()
	current, known := api.lastQueryRequests[session]
	api.lastQueryMu.RUnlock()
	if !known {
		current = parent
	}
	if inbox != "" && inbox == current.AgentMessageInbox {
		return nil
	}
	if !read && inbox == "" && target.NamedAgentID != "" && appAgentPublished(context.Background(), target.NamedAgentID) {
		return nil
	}
	log.Printf("[AGENT-MESSAGE] action=refused agent=%s session=%s reason=restricted_turn", caller.NamedAgentID, session)
	return admissionRefused("Another person's request may only use its admitted AgentBridge thread or a public agent")
}
