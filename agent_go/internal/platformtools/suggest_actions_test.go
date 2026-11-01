package platformtools

import (
	"context"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	orchestratorevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

func TestSuggestActionsEmitsTheBindingsKindTaggedWithTheProduct(t *testing.T) {
	var got []*orchestratorevents.ProductInteractionEvent
	rt := agentprofiles.ToolRuntimeContext{
		Product:     "example-app",
		Interaction: &agentprofiles.InteractionBinding{Kind: "next_steps", Render: "chat.suggestions"},
		Emit: func(ev any) {
			if e, ok := ev.(*orchestratorevents.ProductInteractionEvent); ok {
				got = append(got, e)
			}
		},
	}
	spec, err := SuggestActionsFactory()(rt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "suggest_actions" {
		t.Fatalf("tool name = %q", spec.Name)
	}
	out, err := spec.Execute(context.Background(), map[string]interface{}{"actions": []interface{}{
		map[string]interface{}{"label": "How's she doing?", "message": "progress"},
		map[string]interface{}{"label": "", "message": "dropped"},
	}})
	if err != nil || out != `{"count":1,"status":"ok"}` {
		t.Fatalf("out=%s err=%v", out, err)
	}
	if len(got) != 1 || got[0].Kind != "next_steps" || got[0].Product != "example-app" {
		t.Fatalf("event = %+v", got)
	}
	if actions, _ := got[0].Payload["actions"].([]map[string]interface{}); len(actions) != 1 || actions[0]["label"] != "How's she doing?" {
		t.Fatalf("payload = %+v", got[0].Payload)
	}
	if _, err := spec.Execute(context.Background(), map[string]interface{}{"actions": []interface{}{}}); err == nil {
		t.Fatal("no usable action must be an error, not a silent empty row")
	}
	// Without a binding the platform default kind applies.
	bare, _ := SuggestActionsFactory()(agentprofiles.ToolRuntimeContext{Emit: rt.Emit}, nil)
	got = nil
	_, _ = bare.Execute(context.Background(), map[string]interface{}{"actions": []interface{}{map[string]interface{}{"label": "a", "message": "b"}}})
	if len(got) != 1 || got[0].Kind != "suggestions" || got[0].Product != "platform" {
		t.Fatalf("default event = %+v", got)
	}
}
