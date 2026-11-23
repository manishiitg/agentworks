package server

import (
	"context"
)

type reachableAgentRuntime struct {
	Helper map[string]any
	Reach  crewTeamReachableAgent
	Info   *agentRuntimeInfo
	Record *ProductConversationRecord
	Owner  string
}

// Permission comes only from the canonical reach set. Registry/model/preferences
// reads enrich those exact identities; neither a model nor a metadata field
// can add an agent or a viewer to that set.
func (api *StreamingAPI) reachableAgentRuntimeInfo(ctx context.Context, viewer string) map[string]reachableAgentRuntime {
	return api.runtimeInfoForAdmittedReach(ctx, crewTeamReachableAgents(ctx, viewer))
}

// runtimeInfoForAdmittedReach decorates exactly the canonical reach answer from
// this request. In particular an empty answer must not discover other Teams.
// It is not an admission entry point and must never receive request arguments.
func (api *StreamingAPI) runtimeInfoForAdmittedReach(ctx context.Context, reaches []crewTeamReachableAgent) map[string]reachableAgentRuntime {
	out := map[string]reachableAgentRuntime{}
	if len(reaches) == 0 {
		return out
	}
	docs := map[string]productConversationRegistryDocument{}
	snapshot, _ := readAgentMessageSnapshot(ctx)
	for _, reach := range reaches {
		team, err := readCrewTeam(reach.Team)
		if err != nil {
			continue
		}
		registered, ok := team.Agents[reach.ID]
		if !ok {
			continue
		}
		row := reachableAgentRuntime{Reach: reach, Info: &agentRuntimeInfo{CrewName: reach.TeamName}}
		owner := registered.OwnerID
		if registered.Kind == "terminal" && registered.Terminal != nil {
			if named, err := currentCrewTeamTerminal(ctx, team, registered); err == nil && named.Attachment != nil {
				row.Helper = map[string]any{}
				projectAgentHelper(row.Helper, named)
				row.Info.CLI = providerAccountProductName(named.Attachment.Provider)
				row.Info.LastActiveAt = named.Attachment.ActivityAt
				row.Info.Activity = "Idle"
				if named.Attachment.Activity == "busy" {
					row.Info.Activity = "Working"
				}
			}
		} else if registered.Place == "mac" {
			for _, agent := range snapshot.Agents {
				if agent.UserID != owner || agent.Attachment == nil || agent.Attachment.DeliveryRoute != appAgentDeliveryRoute {
					continue
				}
				info := agent.Attachment.RuntimeInfo
				if info != nil && info.TeamProject == reach.Team && info.TeamTab == registered.Tab {
					row.Helper = map[string]any{}
					projectAgentHelper(row.Helper, agent)
					row.Info = info
					break
				}
			}
		} else {
			doc, loaded := docs[owner]
			if !loaded {
				source, _ := api.agentDirectorySource(ctx, owner)
				doc = source.Document
				docs[owner] = doc
			}
			key := productConversationRegistryEntryKey(crewProfileID, registered.ChatKey)
			if record, found := doc.Entries[key]; found {
				row.Info = api.runtimeInfoForRecord(ctx, owner, record, doc.Agents[key])
				row.Record = &record
				row.Owner = owner
			}
		}

		out[reach.Address] = row
	}
	return out
}
