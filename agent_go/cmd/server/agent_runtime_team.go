package server

import (
	"context"
)

type reachableAgentRuntime struct {
	Reach crewTeamReachableAgent
	Info  *agentRuntimeInfo
	Doing string
}

// Permission comes only from the canonical reach set. Registry/model/preferences
// reads enrich those exact identities; neither a model nor a metadata field
// can add an agent or a viewer to that set.
func (api *StreamingAPI) reachableAgentRuntimeInfo(ctx context.Context, viewer string) map[string]reachableAgentRuntime {
	out := map[string]reachableAgentRuntime{}
	docs := map[string]productConversationRegistryDocument{}
	groups := map[string]menuGroups{}
	snapshot, _ := readAgentMessageSnapshot(ctx)
	for _, reach := range crewTeamReachableAgents(ctx, viewer) {
		team, err := readCrewTeam(reach.Team)
		if err != nil {
			continue
		}
		registered, ok := team.Agents[reach.ID]
		if !ok && reach.Kind == "team_agent" && team.OnlineHome != "" {
			if home, err := readCrewTeam(team.OnlineHome); err == nil {
				registered, ok = home.Agents[reach.ID]
			}
		}
		if !ok {
			continue
		}
		row := reachableAgentRuntime{Reach: reach, Info: &agentRuntimeInfo{CrewName: reach.TeamName}}
		owner := registered.OwnerID
		if registered.Place == "mac" {
			for _, agent := range snapshot.Agents {
				if agent.UserID != owner || agent.Attachment == nil || agent.Attachment.DeliveryRoute != appAgentDeliveryRoute {
					continue
				}
				info := agent.Attachment.RuntimeInfo
				if info != nil && info.TeamProject == reach.Team && info.TeamTab == registered.Tab {
					row.Info = info
					break
				}
			}
		} else {
			doc, loaded := docs[owner]
			if !loaded {
				doc, _ = defaultProductConversationRegistryStore().loadDocument(ctx, productConversationRegistryPath(owner))
				docs[owner] = doc
				// Explicit owner-authorized server projection of organization
				// names to this reachable audience; never passed to the model
				// as an owner credential or used for message admission.
				ownerCtx := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: owner})
				groups[owner], _ = updateMenuGroups(ownerCtx, nil)
			}
			key := productConversationRegistryEntryKey(crewProfileID, registered.ChatKey)
			if record, found := doc.Entries[key]; found {
				row.Info = api.runtimeInfoForRecord(ctx, owner, record, doc.Agents[key], groups[owner])
				if reach.ChatShare == crewChatShareLive {
					row.Info.OwnerStatus = api.ownerStatusForRecord(ctx, owner, record)
				}
			}
		}
		posts := crewTeamActivityPostsFor(team, viewer)
		if post, ok := posts.Doing[reach.ID]; ok {
			row.Doing = crewTeamMessageLine(post.Line)
		}
		out[reach.Address] = row
	}
	return out
}
