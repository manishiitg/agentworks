package server

import (
	"context"
	"fmt"
	"strings"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
)

// NotifyScript uses a server-known caller/destination, not a script-provided
// user or chat ID. Progress is appended through the existing conversation
// journal; it neither resumes an agent nor uses Slack/email destinations.
func (r *crewWorkflowResources) NotifyScript(ctx context.Context, workspace, sessionID, receiptID, text string) (string, error) {
	if r.crews == nil || r.crews.api == nil || r.crews.registry == nil {
		return "", nil // the caller still retains its progress in the helper log
	}
	binding, err := findCrewExecutable(workspace)
	if err != nil {
		return "", err
	}
	if binding == nil {
		return "", nil
	}
	owner, ok := crewProjectOwnerID(binding.CrewRoot)
	if !ok || owner != r.userID || directoryUserIsDisabled(&UserClaims{UserID: owner}) {
		return "", fmt.Errorf("this Crew's execution owner is unavailable")
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspace)
	if err != nil || !found {
		return "", fmt.Errorf("this automation is unavailable")
	}
	scheduler := r.crews.api.scheduler
	if scheduler == nil {
		scheduler = &SchedulerService{api: r.crews.api}
	}
	sctx := buildScheduleContext(workspace, manifest, WorkflowSchedule{Name: manifest.Label, ChatID: "main"})

	caller, callerUser := sessionID, r.userID
	if parent := virtualtools.GetParentChat(sessionID); parent != nil {
		caller, callerUser = parent.SessionID, parent.UserID
	} else {
		scheduleOrigins.mu.RLock()
		origin := scheduleOrigins.originBySchedule[sessionID]
		scheduleOrigins.mu.RUnlock()
		if origin != "" {
			caller = origin
			if active, found := r.crews.api.getActiveSession(origin); found {
				callerUser = active.UserID
			}
		}
	}

	var target *ProductConversationRecord
	if caller != "" && callerUser != "" {
		claims := &UserClaims{UserID: callerUser}
		profile, err := r.crews.registry.Resolve("work", 0, callerUser)
		if err == nil && userAllowedProduct(claims, "work") && !directoryUserIsDisabled(claims) {
			crew, accessErr := resolveCrewProjectBinding(ctx, callerUser, profile, binding.CrewID, binding.CrewRoot)
			if accessErr == nil && canonicalCrewWorkspaceRoot(crew.Binding.WorkspacePath) == binding.CrewRoot {
				chats, err := defaultProductConversationRegistryStore().projectChats(ctx, callerUser, "work", binding.CrewID)
				if err != nil {
					return "", err
				}
				for _, chat := range chats {
					if chat.SessionID == caller && canonicalCrewWorkspaceRoot(chat.WorkspacePath) == binding.CrewRoot {
						copy := chat
						target = &copy
						sctx.OwnerUserID = callerUser
						break
					}
				}
			}
		}
	}
	if target == nil {
		// Timer origins already point at the saved chat above. With no usable
		// origin (for example an external webhook), the Crew's Main chat is
		// the existing fallback; no arbitrary recipient can be selected here.
		target, err = scheduler.openCrewAutomationChat(ctx, sctx)
		if err != nil {
			return "", err
		}
	}
	if target == nil || strings.TrimSpace(target.SessionID) == "" {
		return "", nil
	}
	if err := scheduler.appendCrewAutomationMessage(ctx, sctx, target, receiptID, "update", text); err != nil {
		return "", err
	}
	return target.SessionID, nil
}
