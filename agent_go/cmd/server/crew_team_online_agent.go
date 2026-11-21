package server

import (
	"context"
	"path"
)

// A newly provisioned Team home starts with one ordinary named agent and Main
// pointer. Later calls resolve that saved Main; they never recreate a removed one.
func (api *StreamingAPI) createCrewTeamHomeMain(ctx context.Context, owner, project string) error {
	ctx = context.WithValue(ctx, UserContextKey, &UserClaims{UserID: owner, Username: owner})
	profile, err := api.agentProfiles.Resolve(crewProfileID, 0, owner)
	if err != nil {
		return err
	}
	binding, err := resolveProductProjectBinding(ctx, owner, profile, project+codeChatSideMarker+"main")
	if err != nil {
		return err
	}
	store := defaultProductConversationRegistryStore()
	record, err := store.resolveOrCreate(ctx, owner, profile, binding, "")
	if err != nil {
		return err
	}
	name := "Team agent"
	purpose := "The Team's always-online shared agent. Every member may set up Team schedules, Watchers and how we work, without owner approval. Run in this home as the virtual Team user with Team accounts/keys. Use team_sync way/way_add/way_remove, skills and rules. Ask for missing keys by name, never values."
	if _, err := store.setAgentIdentity(ctx, owner, crewProfileID, record.ConversationKey, &name, &purpose, owner); err != nil {
		return err
	}
	if err := store.writeMainChatPointer(ctx, path.Join(binding.WorkspacePath, "product.json"), record.ConversationKey, record.SessionID); err != nil {
		return err
	}
	// private=true with no RunPeople is not Everyone. Joined members receive
	// access through the normal derived Team roster (crewSharedWith/co-owners).
	return defaultProjectOwners().SetAudience(crewProfileID, path.Base(binding.WorkspacePath), true, nil, []string{record.ConversationKey})
}
