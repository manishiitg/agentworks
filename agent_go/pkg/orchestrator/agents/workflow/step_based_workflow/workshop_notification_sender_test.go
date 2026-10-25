package step_based_workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// Exercise the real Builder save followed by group and child tool-session setup,
// without an AI turn, credentials, or an external message.
func TestWorkshopSenderSaveReachesSameTurnChild(t *testing.T) {
	for _, clear := range []bool{false, true} {
		name := "select"
		if clear {
			name = "clear"
		}
		t.Run(name, func(t *testing.T) {
			controller, folder := newPreValidationConcernTestOrchestrator(t)
			t.Setenv("WORKSPACE_API_URL", controller.WorkspaceClient.BaseURL)
			manifest := `{"id":"sender-test","capabilities":{"notifications":{"run_summary_recipients":["recipient@example.com"],"run_summary_gmail_connection_ids":["legacy-run"]}}}`
			if err := os.WriteFile(filepath.Join(folder, "workflow.json"), []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			previous := services.GetGmailService()
			svc := &services.GmailService{}
			services.SetGmailService(svc)
			t.Cleanup(func() { services.SetGmailService(previous) })
			if err := svc.SaveConfig(t.Context(), &services.GmailConfig{Enabled: false, Connections: []services.GmailConnection{{ID: "selected-mail", Personal: true, OwnerID: "owner", Email: "owner@example.com", Enabled: true, Workspaces: []string{controller.GetWorkspacePath()}}}}); err != nil {
				t.Fatal(err)
			}
			const parent = "sender-save-parent"
			const child = "sender-save-child"
			controller.SetMCPSessionID(parent)
			dest := &services.NotificationDestination{UserID: "owner", WorkspacePath: controller.GetWorkspacePath(), WorkflowName: "demo", RunSummaryGmailConnectionIDs: []string{"legacy-run"}}
			if clear {
				dest.Gmail = &services.GmailDest{Workspace: controller.GetWorkspacePath(), ConnectionIDs: []string{"selected-mail"}}
			}
			virtualtools.RegisterSessionNotificationDestination(parent, dest)
			t.Cleanup(func() {
				virtualtools.DeleteSessionNotificationDestination(parent)
				virtualtools.DeleteSessionNotificationDestination(child)
				common.ClearSessionShellConfig(child)
			})
			ctx := context.WithValue(t.Context(), common.UserIDKey, "owner")
			ctx = context.WithValue(ctx, common.ChatSessionIDKey, parent)
			ctx = context.WithValue(ctx, virtualtools.BotNotificationDestinationKey, dest)
			draft := newWorkshopDefinitionDraft()
			session := &WorkshopChatSession{controller: controller, mainSessionID: parent, StepRegistry: NewWorkshopStepRegistry(), config: &WorkshopConfig{WorkspacePath: controller.GetWorkspacePath()}}
			RegisterWorkshopChatTools(draft, session, workshopToolTestLogger{})
			sender := "selected-mail"
			if clear {
				sender = ""
			}
			if result, err := draft.tools["update_workflow_config"].Execute(ctx, map[string]interface{}{"notification_gmail_connection_id": sender}); err != nil {
				t.Fatalf("save: %s / %v", result, err)
			}
			data, err := os.ReadFile(filepath.Join(folder, "workflow.json"))
			if err != nil {
				t.Fatal(err)
			}
			var saved struct {
				Capabilities struct {
					Notifications struct {
						Sender     string   `json:"gmail_connection_id"`
						RunSenders []string `json:"run_summary_gmail_connection_ids"`
					} `json:"notifications"`
				} `json:"capabilities"`
			}
			if err := json.Unmarshal(data, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Capabilities.Notifications.Sender != sender {
				t.Fatalf("saved sender=%q", saved.Capabilities.Notifications.Sender)
			}
			if len(saved.Capabilities.Notifications.RunSenders) != 0 {
				t.Fatal("saved sender kept legacy overrides")
			}
			if !clear {
				cfg := svc.GetConfig()
				cfg.Connections = append(cfg.Connections, services.GmailConnection{ID: "outside-item", Personal: true, OwnerID: "owner", Enabled: true, Workspaces: []string{"Workflow/other"}})
				if err := svc.SaveConfig(t.Context(), cfg); err != nil {
					t.Fatal(err)
				}
				result, err := draft.tools["update_workflow_config"].Execute(ctx, map[string]interface{}{"notification_gmail_connection_id": "outside-item"})
				if err != nil || !strings.Contains(result, "added to this workflow") {
					t.Fatalf("wrong-item selection accepted: %s / %v", result, err)
				}
				choices, err := draft.tools["get_workflow_config"].Execute(ctx, map[string]interface{}{})
				if err != nil || strings.Contains(choices, "outside-item") {
					t.Fatalf("ineligible sender metadata advertised: %s / %v", choices, err)
				}
				available := ""
				for _, line := range strings.Split(choices, "\n") {
					if strings.Contains(line, "Available Gmail senders") {
						available = line
					}
				}
				if !strings.Contains(available, "selected-mail") {
					t.Fatalf("eligible sender missing from choices: %s", choices)
				}
			}
			release, err := controller.switchWorkshopGroupSession("default")
			if err != nil {
				t.Fatal(err)
			}
			group := controller.GetMCPSessionID()
			defer release()
			defer controller.CloseWorkshopGroupSessions()
			defer virtualtools.DeleteSessionNotificationDestination(group)
			controller.configureSubAgentSessionGuard(child, "agent", "email-person", nil, nil)
			got := virtualtools.NotificationDestinationFromContext(context.WithValue(ctx, common.ChatSessionIDKey, child))
			if got == nil {
				t.Fatal("child has no destination")
			}
			if len(got.RunSummaryGmailConnectionIDs) != 0 {
				t.Fatal("child differs from saved workflow: legacy overrides remain")
			}
			if !clear {
				if got.Gmail == nil || got.Gmail.Workspace != controller.GetWorkspacePath() || len(got.Gmail.ConnectionIDs) != 1 || got.Gmail.ConnectionIDs[0] != sender {
					t.Fatalf("saved sender did not reach same-turn child: %#v", got.Gmail)
				}
			} else if got.Gmail != nil && len(got.Gmail.ConnectionIDs) > 0 {
				t.Fatalf("cleared sender remained in child: %#v", got.Gmail)
			}
			if svc.IsEnabledForDestination(got) == clear {
				t.Fatalf("company-off eligibility after clear=%v: %#v", clear, got.Gmail)
			}
		})
	}
}
