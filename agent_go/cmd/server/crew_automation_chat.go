package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// Saved work retains its own run identity; the chat is its durable destination,
// never a replacement for a workflow checkpoint or a provider session handle.
func validateExecutableSchedule(ctx context.Context, sctx *ScheduleContext) error {
	if sctx.WorkflowKind != "relay" && sctx.Schedule.GroupNames != nil && len(normalizeScheduleGroupNames(sctx.Schedule.GroupNames)) == 0 {
		return fmt.Errorf("choose at least one input set, or omit the selection for automatic execution")
	}
	if sctx.CrewExecutable == nil {
		return nil
	}
	owner, ok := crewProjectOwnerID(sctx.CrewExecutable.CrewRoot)
	if !ok || owner == "" || sctx.OwnerUserID != owner || directoryUserIsDisabled(&UserClaims{UserID: owner}) {
		return fmt.Errorf("this Crew's execution owner is unavailable")
	}
	chat := strings.TrimSpace(sctx.Schedule.ChatID)
	if chat == "" || chat == "main" {
		return nil
	}
	_, err := projectScheduleChatKey(ctx, owner, "work", sctx.CrewExecutable.CrewID, chat)
	return err
}

func prepareCrewExecutableSchedules(ctx context.Context, root string, manifest *WorkflowManifest) error {
	binding, err := findCrewExecutable(root)
	if err != nil {
		return err
	}
	if binding == nil {
		return nil
	}
	manifest.crewExecutable = binding
	for i := range manifest.Schedules {
		schedule := &manifest.Schedules[i]
		if owner := workflowExecutionOwnerUserID(manifest); schedule.RunAs != "" && schedule.RunAs != owner {
			return fmt.Errorf("automation must run as its Crew owner")
		}
		if strings.TrimSpace(schedule.ChatID) == "" {
			schedule.ChatID = "main"
		}
		if err := validateExecutableSchedule(ctx, buildScheduleContext(root, manifest, *schedule)); err != nil {
			return err
		}
	}
	return nil
}

func (s *SchedulerService) openCrewAutomationChat(ctx context.Context, sctx *ScheduleContext) (*ProductConversationRecord, error) {
	if sctx.CrewExecutable == nil {
		return nil, nil
	}
	if s.api == nil || s.api.projectActions == nil || s.api.projectActions.registry == nil {
		return nil, fmt.Errorf("Crew chats are unavailable")
	}
	profile, err := s.api.projectActions.registry.Resolve("work", 0, sctx.OwnerUserID)
	if err != nil {
		return nil, err
	}
	key := sctx.CrewExecutable.CrewID
	if id := strings.TrimSpace(sctx.Schedule.ChatID); id != "" && id != "main" {
		key, err = projectScheduleChatKey(ctx, sctx.OwnerUserID, "work", key, id)
		if err != nil {
			return nil, err
		}
	}
	binding, err := resolveProductConversationBinding(ctx, sctx.OwnerUserID, profile, key)
	if err != nil {
		return nil, err
	}
	if canonicalCrewWorkspaceRoot(binding.WorkspacePath) != sctx.CrewExecutable.CrewRoot {
		return nil, fmt.Errorf("automation chat belongs to another Crew")
	}
	record, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, sctx.OwnerUserID, profile, binding, "")
	if err != nil {
		return nil, err
	}
	return &record, nil
}

// Append through the normal conversation journal. No agent call, transcript
// rewrite or client-side queue is needed for a script/program notification.
func (s *SchedulerService) appendCrewAutomationMessage(ctx context.Context, sctx *ScheduleContext, target *ProductConversationRecord, runID, status, detail string) error {
	file, found, err := s.api.liveConversationPath(sctx.OwnerUserID, target.SessionID, target.WorkspacePath)
	if err != nil {
		return err
	}
	if !found {
		var ok bool
		file, ok = workProjectChatHistoryConversationPath(sctx.OwnerUserID, target.WorkspacePath, target.SessionID, time.Now())
		if !ok {
			return fmt.Errorf("automation chat history is unavailable")
		}
	}
	lock := chatConversationMutex(file)
	lock.Lock()
	defer lock.Unlock()
	raw, exists, err := readConversationTailFromWorkspace(ctx, file, 100)
	if err != nil {
		return err
	}
	record := map[string]interface{}{}
	if exists {
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return err
		}
		if stringFromRecord(record, "user_id") != sctx.OwnerUserID || stringFromRecord(record, "session_id") != target.SessionID {
			return fmt.Errorf("automation chat history identity mismatch")
		}
	}
	receiptID := runID + ":" + status
	var previous []map[string]interface{}
	if data, ok := record["conversation_history"]; ok {
		encoded, _ := json.Marshal(data)
		_ = json.Unmarshal(encoded, &previous)
	}
	for _, message := range previous {
		if message["automation_receipt_id"] == receiptID {
			return nil
		}
	}
	role := llmtypes.ChatMessageTypeAI
	if status == "started" {
		role = llmtypes.ChatMessageTypeHuman
	}
	text := fmt.Sprintf("%s · %s", sctx.Schedule.Name, status)
	if detail != "" {
		text += "\n\n" + detail
	}
	message := llmtypes.MessageContent{Role: role, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}}}
	encoded, err := json.Marshal(message)
	if err != nil {
		return err
	}
	var event map[string]interface{}
	_ = json.Unmarshal(encoded, &event)
	event["automation_receipt_id"] = receiptID
	event["automation"] = map[string]string{"run_id": runID, "workflow_id": sctx.WorkflowID, "trigger_id": sctx.Schedule.ID, "status": status}
	encoded, err = json.Marshal(event)
	if err != nil {
		return err
	}
	now := time.Now()
	runtime := &ChatHistoryAgentRuntime{WorkspacePath: target.WorkspacePath}
	if !exists {
		record = map[string]interface{}{"session_id": target.SessionID, "user_id": sctx.OwnerUserID, "agent_mode": "agent_profile", "title": target.Title, "runtime": runtime, "conversation_history": []json.RawMessage{encoded}, "updated_at": now.Format(time.RFC3339Nano)}
		content, e := json.Marshal(record)
		if e != nil {
			return e
		}
		if e = writeRawFileToWorkspace(ctx, file, string(content)); e != nil {
			return e
		}
	} else {
		advanceChatConversationRevision(record)
		if err := appendConversationHistoryInWorkspace(ctx, file, []json.RawMessage{encoded}, map[string]interface{}{"updated_at": now.Format(time.RFC3339Nano), "revision": record["revision"]}); err != nil {
			return err
		}
	}
	total := len(previous) + 1
	if count, ok := record["history_total"].(float64); ok {
		total = int(count) + 1
	}
	if err := updatePersistedChatHistoryIndexWithCount(sctx.OwnerUserID, target.SessionID, "agent_profile", []llmtypes.MessageContent{message}, total, runtime, file, 0, now, nil); err != nil {
		return err
	}
	publishSessionsChanged()
	return nil
}

func crewExecutableChatChoices(manifest *WorkflowManifest) []ScheduleRunDestination {
	if manifest == nil || manifest.crewExecutable == nil {
		return nil
	}
	owner := workflowExecutionOwnerUserID(manifest)
	profile := "work"
	choices := projectRunDestinations(productScheduleJob{UserID: owner, Profile: agentprofiles.Profile{ID: profile}, ProjectID: manifest.crewExecutable.CrewID, WorkspacePath: manifest.crewExecutable.CrewRoot})
	out := []ScheduleRunDestination{}
	for _, choice := range choices {
		if choice.ID != "isolated" {
			out = append(out, choice)
		}
	}
	return out
}
