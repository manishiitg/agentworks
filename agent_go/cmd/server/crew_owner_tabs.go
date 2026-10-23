package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// Tab control belongs to the person whose registry holds the conversation.
// Owners share the Crew and can observe every owner's tabs; they never adopt
// another owner's conversation to type into it. Messages keep their own sender.

func crewOwnerDefaultChatKey(project, user string) string {
	sum := sha256.Sum256([]byte(sanitizeUserIDForPath(user)))
	return project + codeChatSideMarker + "owner-" + hex.EncodeToString(sum[:8])
}

type crewOwnerChat struct {
	Record   ProductConversationRecord
	Identity ProductAgentIdentity
	OwnerID  string
}

func crewOwnerTabPeople(caller, workspace string) []string {
	if !isCrewProjectPath(workspace) || !crewProjectOwnedByCaller(caller, workspace) {
		return nil
	}
	owner, ok := crewProjectOwnerID(workspace)
	if !ok {
		return nil
	}
	return append([]string{owner}, crewCoOwnerIDs(workspace)...)
}

func crewOwnerChatRecords(ctx context.Context, caller, profile, project, workspace string) ([]crewOwnerChat, error) {
	people := crewOwnerTabPeople(caller, workspace)
	if len(people) == 0 {
		people = []string{caller}
	}
	store := defaultProductConversationRegistryStore()
	out := []crewOwnerChat{}
	for _, person := range people {
		records, err := store.projectChats(ctx, person, profile, project)
		if err != nil {
			return nil, err
		}
		identities, err := store.agentIdentities(ctx, person, profile, project)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if canonicalCrewWorkspaceRoot(record.WorkspacePath) != canonicalCrewWorkspaceRoot(workspace) {
				continue
			}
			out = append(out, crewOwnerChat{record, identities[record.ConversationKey], person})
		}
	}
	return out, nil
}

func crewTabForKey(ctx context.Context, caller, profile, key, workspace string) (crewOwnerChat, bool, error) {
	project := crewResourceProjectID(key)
	tabs, err := crewOwnerChatRecords(ctx, caller, profile, project, workspace)
	if err != nil {
		return crewOwnerChat{}, false, err
	}
	for _, tab := range tabs {
		if tab.Record.ConversationKey == key {
			return tab, true, nil
		}
	}
	return crewOwnerChat{}, false, nil
}

func crewTabWriteError(ctx context.Context, caller, profile, key, workspace string) error {
	tab, found, err := crewTabForKey(ctx, caller, profile, key, workspace)
	if err != nil {
		return err
	}
	if found && tab.OwnerID != caller {
		return fmt.Errorf("%s controls this tab. You can view it, use your own tab, or send its agent a message.", crewOwnerDisplayName(tab.OwnerID))
	}
	return nil
}

// Called after project admission, for the default address only. The main
// owner's address keeps routing channels; a co-owner's default is their own
// durable named tab. Rotating its conversation keeps the same owner and name.
func crewOwnerDefaultBinding(caller string, binding productConversationBinding) productConversationBinding {
	if owner, ok := crewProjectOwnerID(binding.WorkspacePath); ok && owner != caller && crewIsCoOwner(binding.WorkspacePath, caller) {
		binding.ProjectMainChatKey = crewOwnerDefaultChatKey(binding.ResourceID, caller)
		if binding.ConversationKey == binding.ResourceID {
			binding.ConversationKey = binding.ProjectMainChatKey
		}
	}
	return binding
}

func crewConversationTabAccess(ctx context.Context, caller string, profile agentprofiles.Profile, conversation ProductConversationRecord) (string, string, bool) {
	people := crewOwnerTabPeople(caller, conversation.WorkspacePath)
	if len(people) == 0 {
		return "", "", true
	}
	tab, found, err := crewTabForKey(ctx, caller, profile.ID, conversation.ConversationKey, conversation.WorkspacePath)
	if err != nil || !found {
		return caller, crewOwnerDisplayName(caller), true
	}
	return tab.OwnerID, crewOwnerDisplayName(tab.OwnerID), tab.OwnerID == caller
}

// Session views use the same owner roster as the tab list, including after a
// restart. Only actual Builder tabs qualify; unrelated private chats stay out.
func (api *StreamingAPI) crewSessionTabView(r *http.Request, session string) bool {
	if tab, _, ok := api.goalTabForSession(r.Context(), session); ok {
		return !tab.CanWrite
	}
	caller := GetUserIDFromContext(r.Context())
	owner := ""
	if active, ok := api.getActiveSession(session); ok {
		owner = active.UserID
	}
	if owner == "" && api.eventStore != nil {
		owner = api.eventStore.GetSessionOwner(session)
	}
	if owner == "" && api.eventStore != nil {
		owner, _ = api.eventStore.DurableChatOwner(session)
	}
	if caller == "" || owner == "" || owner == caller {
		return false
	}
	doc, err := defaultProductConversationRegistryStore().loadDocument(r.Context(), productConversationRegistryPath(owner))
	if err != nil {
		return false
	}
	for _, record := range doc.Entries {
		if record.SessionID != session || !isSideChatConversationKey(record.ConversationKey) {
			continue
		}
		workspace := record.WorkspacePath
		if !crewProjectOwnedByCaller(caller, workspace) || !crewProjectOwnedByCaller(owner, workspace) {
			return false
		}
		if token := GetUserFromContext(r.Context()).AccessToken; token != nil && (!token.AllowsCrew(record.ResourceID) || !token.Allows("crews:read")) {
			return false
		}
		return true
	}
	return false
}

func (api *StreamingAPI) crewSessionCanControl(r *http.Request, session string) bool {
	if tab, _, allowed := api.goalTabForSession(r.Context(), session); allowed {
		return tab.CanWrite
	}
	if _, _, registered := api.goalSessionRecord(r.Context(), session); registered {
		return false
	}
	if active, ok := api.getActiveSession(session); ok && isCrewProjectPath(active.WorkspacePath) {
		return active.UserID == GetUserIDFromContext(r.Context())
	}
	if api.eventStore != nil {
		owner := api.eventStore.GetSessionOwner(session)
		if owner == "" {
			owner, _ = api.eventStore.DurableChatOwner(session)
		}
		if owner != "" && owner != GetUserIDFromContext(r.Context()) {
			return !api.crewSessionTabView(r, session)
		}
	}
	return true
}

func crewQueryTabWriteError(ctx context.Context, caller, profile, workspace, session string) error {
	if len(crewOwnerTabPeople(caller, workspace)) == 0 {
		return nil
	}
	product, _, ok := projectProductForPath(workspace)
	if !ok || product.ProfileID != profile {
		return nil
	}
	raw, found, err := readFileFromWorkspace(ctx, strings.TrimSuffix(workspace, "/")+"/product.json")
	if err != nil || !found {
		return err
	}
	var manifest productProjectManifest
	if err = json.Unmarshal([]byte(raw), &manifest); err != nil {
		return err
	}
	tabs, err := crewOwnerChatRecords(ctx, caller, profile, manifest.ID, workspace)
	if err != nil {
		return err
	}
	for _, tab := range tabs {
		if tab.Record.SessionID == session && tab.OwnerID != caller {
			return fmt.Errorf("%s controls this tab. Use your own tab or send its agent a message.", crewOwnerDisplayName(tab.OwnerID))
		}
	}
	return nil
}

func ensureCrewOwnerDefaultTab(ctx context.Context, user string, profile agentprofiles.Profile, project string) error {
	binding, owned, err := resolveConversationBindingForUser(ctx, user, profile, project)
	if err != nil {
		return err
	}
	if !owned {
		return fmt.Errorf("Crew owner is unavailable")
	}
	record, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, user, profile, binding, "")
	if err != nil {
		return err
	}
	if identity, e := defaultProductConversationRegistryStore().agentIdentity(ctx, user, profile.ID, record.ConversationKey); e == nil && identity.Name != "" {
		return nil
	}
	return setCodeTabName(ctx, user, record.ConversationKey, crewOwnerDisplayName(user)+" · Builder", profile.ID)
}
