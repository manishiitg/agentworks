package server

import (
	"context"
	"strings"
)

// What a schedule or webhook starts, as the one plain line the Automations pages print (the server decides it, the
// page shows it). A Crew has one workflow, so a run is "Runs the workflow" plus the saved setting (input preset) it
// uses; a message schedule names the agent that gets the message.

const automationStartsWorkflow = "Runs the workflow"

// automationStartsWorkflowLine is "Runs the workflow · <saved setting>", or "Runs the workflow" with none.
func automationStartsWorkflowLine(inputPreset string) string {
	if preset := strings.TrimSpace(inputPreset); preset != "" {
		return automationStartsWorkflow + " · " + preset
	}
	return automationStartsWorkflow
}

// automationStartsMessageLine is "Sends a message to <agent>".
func automationStartsMessageLine(agent string) string {
	return "Sends a message to " + firstNonEmptyTrimmed(agent, "Main")
}

// workflowScheduleStartsLine is the line for a schedule or webhook saved on a workflow. A Crew workflow always runs
// itself. An older workflow can also run one step, or send its own messages to the workflow's Builder chat.
func workflowScheduleStartsLine(manifest *WorkflowManifest, sched WorkflowSchedule) string {
	if manifest != nil && manifest.crewExecutable != nil {
		return automationStartsWorkflowLine(sched.InputPreset)
	}
	if sched.Webhook != nil && strings.TrimSpace(sched.Webhook.StepID) != "" {
		return "Runs one step of the workflow"
	}
	instructions := strings.ToLower(strings.Join(sched.Messages, "\n"))
	switch {
	case strings.Contains(instructions, "execute_step") || strings.Contains(instructions, "run only step-"):
		return "Runs one step of the workflow"
	case strings.TrimSpace(instructions) != "" && !strings.Contains(instructions, "run_full_workflow") && sched.WorkshopMode == "workshop" && sched.ScheduleType != "webhook":
		return automationStartsMessageLine("the Builder")
	}
	line := automationStartsWorkflowLine(sched.InputPreset)
	routes := []string{}
	for _, route := range sched.RouteSelections {
		if route = strings.TrimSpace(route); route != "" {
			routes = append(routes, route)
		}
	}
	if len(routes) == 1 {
		line += " · " + routes[0] + " route"
	} else if len(routes) > 1 {
		line += " · chosen routes"
	}
	return line
}

// productScheduleStartsLine is the line for a Crew (or product) schedule. A schedule with a workflow target runs the
// Crew's workflow; any other schedule sends its messages to an agent: the tab (named agent) it was set in, else Main.
// A Watcher keeps checking something and is listed on its own page, so it has no line here.
func productScheduleStartsLine(job productScheduleJob) string {
	sched := job.Effective()
	switch {
	case sched.Condition != nil:
		return ""
	case strings.TrimSpace(sched.WorkflowID) != "":
		return automationStartsWorkflowLine(sched.InputPreset)
	case strings.TrimSpace(job.ProjectID) == "":
		return automationStartsMessageLine(job.Profile.Name)
	}
	chatKey := scheduleChatKey(job)
	if chatKey == "" || job.Schedule.Isolated {
		return automationStartsMessageLine("Main")
	}
	return automationStartsMessageLine(projectScheduleAgentName(job, chatKey))
}

// projectScheduleAgentName is the name of the tab a schedule sends to, as the Crew's tab list shows it.
func projectScheduleAgentName(job productScheduleJob, chatKey string) string {
	id := strings.TrimPrefix(chatKey, strings.TrimSpace(job.ProjectID)+codeChatSideMarker)
	if chats, err := crewOwnerChatRecords(context.Background(), job.UserID, job.Profile.ID, job.ProjectID, job.WorkspacePath); err == nil {
		for _, chat := range chats {
			if chat.Record.ConversationKey != chatKey {
				continue
			}
			if chat.Record.IsMainChat {
				return "Main"
			}
			return firstNonEmptyTrimmed(chat.Identity.Name, job.AgentName, "Chat "+id)
		}
	}
	return firstNonEmptyTrimmed(job.AgentName, "Chat "+id)
}
