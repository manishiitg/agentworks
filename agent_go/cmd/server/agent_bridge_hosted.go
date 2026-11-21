package server

import (
	"context"
	"fmt"
	"log"
	"slices"
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

func sameHostedBridgeBinding(a, b *hostedBridgeBinding) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

func (api *StreamingAPI) hostedBridgeChat(ctx context.Context, owner string, binding hostedBridgeBinding) (codeChatProject, codeChat, error) {
	empty := codeChatProject{}
	if api.agentProfiles == nil || owner == "" || binding.Profile != crewProfileID || binding.Project == "" || binding.Chat == "" || crewFunctionTabKey(binding.Chat) {
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
		chat := codeChat{OwnerID: owner, Key: r.ConversationKey, ID: r.ConversationKey, SessionID: r.SessionID, Name: crewDirectoryChatName(tab), Examples: tab.Identity.Examples, Purpose: tab.Identity.Purpose}
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
	return func() (namedMessageAgent, error) {
		address := bridgeChatAddress(owner, binding.Profile, binding.Project, chat.Key)
		_, name, _ := strings.Cut(strings.TrimPrefix(address, "agent:"), "/")
		label, purpose := cleanAgentLabel(chat.Name), cleanAgentPurpose(chat.Purpose)
		snapshot, err := readAgentMessageSnapshot(ctx)
		if err != nil {
			return namedMessageAgent{}, err
		}
		existing, found, err := existingHostedBridgeRegistration(snapshot, owner, binding, name)
		if err != nil {
			return namedMessageAgent{}, err
		}
		if found && (existing.Hosted == nil || existing.Label == label && existing.Purpose == purpose && slices.Equal(existing.Examples, chat.Examples)) {
			return existing, nil
		}
		// The common discovery read above never clones message histories or
		// writes. Serialize and recheck only a new identity or a real rename.
		agentMessageMu.Lock()
		defer agentMessageMu.Unlock()
		store, err := readAgentMessageStore(ctx)
		if err != nil {
			return namedMessageAgent{}, err
		}
		existing, found, err = existingHostedBridgeRegistration(store, owner, binding, name)
		if err != nil {
			return namedMessageAgent{}, err
		}
		if found {
			if existing.Hosted == nil {
				return existing, nil
			}
			if existing.Label != label || existing.Purpose != purpose || !slices.Equal(existing.Examples, chat.Examples) {
				existing.Label, existing.Purpose = label, purpose
				existing.Examples = chat.Examples
				for i := range store.Agents {
					if store.Agents[i].ID == existing.ID {
						store.Agents[i] = existing
						break
					}
				}
				if err := saveAgentMessageStore(ctx, store); err != nil {
					return namedMessageAgent{}, err
				}
			}
			return existing, nil
		}
		agent := namedMessageAgent{ID: "agent-" + uuid.NewString(), UserID: owner, Name: name, Label: cleanAgentLabel(chat.Name), Purpose: cleanAgentPurpose(chat.Purpose), Examples: chat.Examples, CreatedAt: time.Now().UTC(), Hosted: &binding}
		store.Agents = append(store.Agents, agent)
		if err := saveAgentMessageStore(ctx, store); err != nil {
			return namedMessageAgent{}, err
		}
		log.Printf("[AGENT-MESSAGE] event=registered agent=%s chat=%s", agent.ID, chat.Key)
		return agent, nil
	}()
}

// A generated app name cannot claim a CLI registration or a different tab.
func existingHostedBridgeRegistration(store agentMessageStore, owner string, binding hostedBridgeBinding, name string) (namedMessageAgent, bool, error) {
	var match *namedMessageAgent
	for _, agent := range store.Agents {
		if agent.UserID != owner {
			continue
		}
		if agent.Name == name && (agent.Hosted == nil || *agent.Hosted != binding) {
			return namedMessageAgent{}, false, fmt.Errorf("this Bridge identity belongs to another chat")
		}
		if agent.Hosted != nil && *agent.Hosted == binding && match == nil {
			copy := agent
			match = &copy
		}
	}
	if match != nil {
		return *match, true, nil
	}
	return namedMessageAgent{}, false, nil
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
	current.TeamAgentID, current.OfflineCopyTeam = endpoint.TeamAgentID, endpoint.OfflineCopyTeam
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
			live, checkErr := currentOwnedAppCrew(internalBotRequestContext(ctx, owner), owner, record)
			if checkErr != nil {
				return ProductConversationRecord{}, checkErr
			}
			if !live {
				return ProductConversationRecord{}, admissionRefused("hosted app Crew was removed or replaced")
			}
			return record, nil
		}
	}
	return ProductConversationRecord{}, admissionRefused("hosted app chat was removed or its ownership changed")
}

// A registered identity does not turn a shared/Run turn into its owner's
// private agent. Like the Mac path, it may reply only in its admitted inbox
// or start a request to an already-public agent, never borrow private reach.
func (api *StreamingAPI) admitHostedBridgeToolTurn(caller triggerLinkCaller, session string, parent QueryRequest, inbox string, target triggerTarget, read bool) error {
	api.lastQueryMu.RLock()
	current, known := api.lastQueryRequests[session]
	api.lastQueryMu.RUnlock()
	if !known {
		current = parent
	}
	// Another person's message now runs in the agent's own chat (one agent, one chat, #3469): that turn is restricted
	// even though the chat itself has its owner's authority.
	owner := ""
	if caller.Chat != nil {
		owner = caller.Chat.OwnerID
	}
	if !desktopAppTurnRestricted(current, owner) && (caller.OwnerAuthority || caller.BridgeOrigin == nil && (caller.Chat == nil || !hiddenFromTabStrip(caller.Chat.Key))) {
		return nil
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

// PR A leaves the old product controls alone. Only addressed Bridge sends or
// an existing named inbox select this identity; PR B removes the other routes.
func hostedBridgeToolCall(ctx context.Context, args map[string]interface{}) bool {
	if strings.TrimSpace(externalArg(args, "target")) != "" {
		return true
	}
	id := externalArg(args, "inbox_id")
	if id == "" {
		return false
	}
	snapshot, err := readAgentMessageSnapshot(ctx)
	if err != nil {
		return false
	}
	for _, c := range snapshot.Conversations {
		if c.ID == id {
			return c.Endpoints[0].Hosted != nil || c.Endpoints[1].Hosted != nil
		}
	}
	return false
}
