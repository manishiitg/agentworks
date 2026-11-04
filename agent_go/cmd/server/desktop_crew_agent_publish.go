package server

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/filesnapshots"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/coding-agent-loop/workspace/crewsync"
)

type crewAgentPublishPlan struct {
	Request        crewSyncRequest         `json:"request"`
	Preview        crewAgentPublishPreview `json:"preview"`
	SessionID      string                  `json:"session_id"`
	ConversationID string                  `json:"conversation_id"`
	UpdatedAt      string                  `json:"updated_at"`
}

func crewAgentPublishPlanPath(id, tab string) (string, error) {
	if !crewMacTabID(tab) {
		return "", fmt.Errorf("choose a Crew agent")
	}
	p, err := crewSyncManifestPath(id)
	return p + ".publish-" + tab, err
}

func (api *StreamingAPI) runDesktopCrewAgentPublish(w http.ResponseWriter, r *http.Request, connection desktopConnection, id, action, transfer string, selected *crewAgentPublishSelection) {
	claims := GetUserFromContext(r.Context())
	fail := func(err error) { externalError(w, 409, "publish_unavailable", err.Error()) }
	roots, err := api.crewSyncRoots(r.Context(), claims, id)
	if err != nil {
		fail(err)
		return
	}
	binding, err := crewroot.ReadSync(id)
	if err != nil {
		fail(err)
		return
	}
	if binding == nil || binding.Side != "local" || binding.ConnectionID != connection.ID || binding.Editing != "local" || binding.PendingAction != "" {
		fail(fmt.Errorf("connect this Crew to its server in Mac online first"))
		return
	}
	placements, err := readCrewAgentPlacements(id)
	if err != nil {
		fail(err)
		return
	}
	out := crewSyncReply{ProjectID: id, Binding: binding, Placements: placements}
	if action == "publish_agents" {
		rows, e := api.macAgentRows(r.Context(), claims, id)
		if e != nil {
			fail(e)
			return
		}
		out.Publish = &crewAgentPublishPreview{Agents: rows}
		externalJSON(w, out)
		return
	}
	if selected == nil {
		fail(fmt.Errorf("choose an agent to publish"))
		return
	}
	selection, err := cleanCrewAgentPublishSelection(*selected)
	if err != nil {
		fail(err)
		return
	}
	record, _, err := api.crewAgentPublishRecord(r.Context(), claims, roots, selection.Tab)
	if err != nil {
		fail(err)
		return
	}
	if place, ok := placements[selection.Tab]; ok && (place.Location == "server" || action == "publish_preview") {
		if place.Location != "server" {
			fail(fmt.Errorf("finish this agent's pending publish first"))
			return
		}
		externalJSON(w, out)
		return
	}
	planPath, err := crewAgentPublishPlanPath(id, selection.Tab)
	if err != nil {
		fail(err)
		return
	}
	var plan crewAgentPublishPlan
	if action == "publish_preview" {
		remote, e := desktopCrewSyncCall(r.Context(), claims.UserID, &connection, binding.PeerProjectID, crewSyncRequest{Action: "publish_accounts", PairID: binding.PairID, Provider: record.Provider, PublishAgent: &selection})
		if e != nil {
			fail(e)
			return
		}
		if remote.Publish == nil {
			fail(fmt.Errorf("update this server to publish one agent"))
			return
		}
		account := remote.Publish.Selection.AccountID
		remote.Publish.Selection = selection
		remote.Publish.Selection.AccountID = account
		out.Publish = remote.Publish
		if !remote.Publish.Ready {
			externalJSON(w, out)
			return
		}
		selection = remote.Publish.Selection
		stage, e := crewSyncStagingRoot(id)
		if e != nil {
			fail(e)
			return
		}
		snapshot, e := crewsync.CaptureSelected(r.Context(), roots, nil, stage, func(key string, dir bool) bool { return crewAgentPublishIncludes(selection, key, dir) })
		defer snapshot.Close()
		if e != nil {
			fail(e)
			return
		}
		main, e := api.crewHandoffMain(r.Context(), claims, id)
		if e != nil {
			fail(e)
			return
		}
		if e = api.captureCrewMoveData(r.Context(), claims, roots, main, &snapshot, selection); e != nil {
			fail(e)
			return
		}
		transfer = uuid.NewString()
		request := crewSyncRequest{Action: "publish_agent", PairID: binding.PairID, TransferID: transfer, SplitFiles: roots.SeparateFiles(), Snapshot: &snapshot, PublishAgent: &selection, Provider: record.Provider}
		preview := *out.Publish
		preview.TransferID, preview.Files = transfer, len(snapshot.Files)
		for _, file := range snapshot.Files {
			preview.Bytes += file.Size
		}
		e = readCrewMoveData(snapshot.StatePath, func(h crewMoveHeader, chat *crewMoveChat) error {
			if chat != nil {
				if chat.Conversation == nil || chat.Conversation.Native == nil {
					return fmt.Errorf("this agent has no portable native session")
				}
				request.Native = append(request.Native, chat.Conversation.Native)
				for _, file := range chat.Conversation.Native.Files {
					preview.NativeBytes += file.Size
				}
				return nil
			}
			for _, s := range h.Schedules {
				preview.Callers = append(preview.Callers, "Schedule: "+s.Name)
			}
			for _, s := range h.Functions {
				preview.Callers = append(preview.Callers, "Function: "+s.Name)
			}
			for _, s := range h.Callers {
				preview.Callers = append(preview.Callers, "Watcher: "+s.Definition.Schedule.ID)
			}
			for _, s := range h.Webhooks {
				preview.Callers = append(preview.Callers, "Webhook: "+s.Name)
			}
			if len(h.Webhooks) > 0 {
				preview.Warnings = append(preview.Warnings, "Webhooks get server addresses and new signing secrets. Update their senders after publishing.")
			}
			return nil
		})
		if e != nil {
			fail(e)
			return
		}
		if len(request.Native) != 1 {
			fail(fmt.Errorf("choose an agent with one current native conversation"))
			return
		}
		// A fresh per-transfer content baseline cannot pull another agent's files
		// into the bundle. Native journals use the existing rsync delta channel.
		contentPair := binding.PairID + "/agent/" + selection.Tab + "/" + transfer
		if e = (filesnapshots.Store{Root: roots.Files, Private: roots.Private}).EncodeSync(r.Context(), contentPair, roots, &snapshot); e != nil {
			fail(e)
			return
		}
		if e = freezeCrewPayloads(id, binding.PairID, transfer, "upload", &snapshot); e != nil {
			fail(e)
			return
		}
		record, _, e = api.crewAgentPublishRecord(r.Context(), claims, roots, selection.Tab)
		if e != nil {
			fail(e)
			return
		}
		plan = crewAgentPublishPlan{Request: request, Preview: preview, SessionID: record.SessionID, ConversationID: record.ConversationID, UpdatedAt: record.UpdatedAt}
		if e = fsutil.WriteJSONAtomic(planPath, plan, 0600); e != nil {
			fail(e)
			return
		}
		out.Publish = &preview
		externalJSON(w, out)
		return
	}
	raw, err := os.ReadFile(planPath)
	if err != nil || json.Unmarshal(raw, &plan) != nil || transfer == "" || plan.Request.TransferID != transfer || plan.Request.PairID != binding.PairID || plan.Request.PublishAgent == nil || plan.Request.PublishAgent.Tab != selection.Tab {
		fail(fmt.Errorf("review what this agent will move in Publish, then confirm"))
		return
	}
	if record.SessionID != plan.SessionID || record.ConversationID != plan.ConversationID || record.UpdatedAt != plan.UpdatedAt {
		fail(fmt.Errorf("this agent changed after the preview; review its latest work before publishing"))
		return
	}
	request := plan.Request
	entry := crewAgentPlacement{Tab: selection.Tab, Location: "publishing", PairID: binding.PairID, TransferID: transfer, SessionID: record.SessionID, ConversationID: record.ConversationID, SplitFiles: roots.SeparateFiles(), Selection: *request.PublishAgent}
	if err = writeCrewAgentPlacement(id, entry); err != nil {
		fail(err)
		return
	}
	retireProductCodingCLI(record.SessionID, "publishing this agent to its server")
	stage := request
	stage.Action = "stage_agent"
	if _, err = desktopCrewSyncCall(r.Context(), claims.UserID, &connection, binding.PeerProjectID, stage); err != nil {
		fail(err)
		return
	}
	if err = hydrateCrewPayloads(id, binding.PairID, transfer, "upload", request.Snapshot); err != nil {
		fail(err)
		return
	}
	for _, native := range request.Native {
		if err = bindCrewNativeStage(roots, native); err != nil {
			fail(err)
			return
		}
		if err = desktopCrewNativeSync(r.Context(), roots, connection, binding, native); err != nil {
			fail(err)
			return
		}
	}
	if err = desktopCrewPayloads(r.Context(), id, connection, binding, transfer, request.Snapshot); err != nil {
		fail(err)
		return
	}
	if err = api.disableCrewMoveSchedules(r.Context(), roots, request.Snapshot.StatePath); err != nil {
		fail(err)
		return
	}
	remote, err := desktopCrewSyncCall(r.Context(), claims.UserID, &connection, binding.PeerProjectID, request)
	if err != nil {
		fail(err)
		return
	}
	receipt, ok := remote.Placements[selection.Tab]
	if !ok || receipt.Location != "server" || receipt.TransferID != transfer {
		fail(fmt.Errorf("the server has not confirmed this publish; retry it"))
		return
	}
	entry.Location, entry.Since = receipt.Location, receipt.Since
	if err = writeCrewAgentPlacement(id, entry); err != nil {
		fail(err)
		return
	}
	out.Placements, err = readCrewAgentPlacements(id)
	if err != nil {
		fail(err)
		return
	}
	out.Publish = &plan.Preview
	log.Printf("[CREW_AGENT_PUBLISH] owner=%s project=%s tab=%s transfer=%s destination=%s result=moved local_folder_kept=true", claims.UserID, id, selection.Tab, transfer, binding.PeerProjectID)
	externalJSON(w, out)
}
