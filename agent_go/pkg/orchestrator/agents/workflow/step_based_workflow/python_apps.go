package step_based_workflow

import (
	"context"
	"fmt"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

type pythonScriptSessionKey struct{}

func pythonAppSelections(apps []string) ([]string, error) {
	var tools []string
	for _, app := range apps {
		switch app {
		case "google":
			tools = append(tools, "human_tools:integrations.google")
		case "github":
			tools = append(tools, "human_tools:integrations.github")
		case "slack":
			tools = append(tools, "human_tools:integrations.slack", "human_tools:messages.slack_send")
		default:
			return nil, fmt.Errorf("Choose an available app: google, slack or github; %q is not supported.", app)
		}
	}
	return tools, nil
}

// A typed app call selects one existing tool just as agent(tools=...) does.
// The ordinary tool filter and account-bound executor remain authoritative;
// no global/script registry is broadened and no account is selected here.
func (hcpo *StepBasedWorkflowOrchestrator) runPythonApp(ctx context.Context, action string, arguments map[string]interface{}) pythonHelperReply {
	app, name := action, ""
	switch action {
	case "google":
		name = "google_workspace_cli"
	case "github":
		name = "github"
	case "slack_read":
		app, name = "slack", "slack"
	default:
		return pythonHelperReply{Error: "Choose a supported app helper."}
	}
	session, _ := ctx.Value(pythonScriptSessionKey{}).(string)
	if session == "" {
		return pythonHelperReply{Error: "Run app helpers from a Crew automation."}
	}
	selections, err := pythonAppSelections([]string{app})
	if err != nil {
		return pythonHelperReply{Error: err.Error()}
	}
	_, executors := hcpo.prepareOrchestratorCustomTools(&AgentConfigs{EnabledCustomTools: selections}, DBAccessNone)
	execute, ok := executors[name].(func(context.Context, map[string]interface{}) (string, error))
	if !ok || execute == nil {
		return pythonHelperReply{Error: "This app is unavailable here. Connect it in this Crew or choose an available app."}
	}
	// This identity was captured when the server created this exact script's
	// bridge, never supplied by Python. The existing account guard reads it.
	toolCtx := context.WithValue(ctx, common.ChatSessionIDKey, session)
	text, err := execute(toolCtx, arguments)
	if err != nil {
		return pythonHelperReply{Error: err.Error()}
	}
	return pythonHelperReply{OK: true, Text: text}
}
