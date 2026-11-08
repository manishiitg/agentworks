package server

import (
	"context"
	"encoding/json"
	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/slack-go/slack/slackevents"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowtrigger"
	"time"
)

type recordingChannelHistory struct {
	channel string
	request services.ChannelHistoryRequest
	calls   int
}

func (r *recordingChannelHistory) ReadChannelHistory(_ context.Context, c string, q services.ChannelHistoryRequest) (*services.ChannelHistoryResult, error) {
	r.channel = c
	r.request = q
	r.calls++
	return &services.ChannelHistoryResult{ChannelID: c}, nil
}
func TestSlackTriggerContextIsSourceBound(t *testing.T) {
	r := &recordingChannelHistory{}
	e := &slackevents.MessageEvent{TimeStamp: "1000.123456"}
	raw, err := captureSlackTriggerContext(context.Background(), r, "CSOURCE", e, &services.SlackTriggerContext{Limit: 30, LookbackMinutes: 10, IncludeThreads: true})
	if err != nil || !json.Valid(raw) {
		t.Fatal(err)
	}
	if r.channel != "CSOURCE" || r.request.Before.Nanosecond() != 123456000 || r.request.Before.Sub(r.request.Since) != 10*time.Minute || r.request.Limit != 30 || !r.request.IncludeThreads {
		t.Fatalf("%+v", r)
	}
	e.TimeStamp = "NaN"
	if _, err = captureSlackTriggerContext(context.Background(), r, "CSOURCE", e, &services.SlackTriggerContext{Limit: 1, LookbackMinutes: 1}); err == nil || r.calls != 1 {
		t.Fatal("malformed timestamp reached transport")
	}
	if _, err = captureSlackTriggerContext(context.Background(), r, "CSOURCE", e, nil); err != nil || r.calls != 1 {
		t.Fatal("unconfigured context reached transport")
	}
}

func TestSlackTriggerSharesWebhookMappingExecutor(t *testing.T) {
	f, _ := newExternalCrewCommandFixture(t)
	binding := crewExecutableBinding{ID: "analyze", CrewID: "team", CrewRoot: externalCommandCrewRoot, Root: "Workflow/example", Kind: "script", RuntimeRoot: "scripts/analyze"}
	if err := registerCrewExecutable(binding); err != nil {
		t.Fatal(err)
	}
	// The trigger selects a real declared preset, never an input definition
	// supplied by its payload or a retired plan/group file.
	f.write(t, externalCommandCrewRoot+"/.agentworks/code/analyze/script.json", `{"id":"analyze","name":"Analyze","inputs":[{"name":"REGION","type":"string","value":"default"}],"input_presets":[{"name":"Production","values":{"REGION":"east"}}]}`)
	f.write(t, externalCommandCrewRoot+"/.agentworks/code/analyze/main.py", "print('fixture')\n")
	route := ChannelRoute{WorkspacePath: binding.Root, Trigger: &services.SlackTrigger{InputPreset: "Production", Inputs: map[string]string{"REGION": "west"}, PayloadMappings: &workflowtrigger.PayloadMappings{Step: &workflowtrigger.ValueMapping{Source: "text", Values: map[string]string{"incident": "analyze"}}}}}
	payload := []byte(`{"text":"incident","message":{"attachments":[{"title":"Production"}]},"group":"unauthorized","variables":{"SECRET":"untrusted"}}`)
	schedule, input, err := resolveSlackTriggerDelivery(context.Background(), route, "delivery", payload)
	if err != nil {
		t.Fatal(err)
	}
	if schedule.InputPreset != "Production" || schedule.Inputs["REGION"] != "west" || schedule.Webhook.StepID != "analyze" || input.Variables != nil || string(input.Payload) != string(payload) {
		t.Fatalf("bad resolved delivery: %+v %+v", schedule, input)
	}
	if _, _, err = resolveSlackTriggerDelivery(context.Background(), route, "delivery", []byte(`{"text":"other"}`)); err == nil {
		t.Fatal("unknown source selected execution target")
	}
	route.Trigger.PayloadMappings.Group = &workflowtrigger.ValueMapping{Source: "text", Values: map[string]string{"incident": "outside"}}
	if _, _, err = resolveSlackTriggerDelivery(context.Background(), route, "delivery", payload); err == nil {
		t.Fatal("retired group mapping selected execution")
	}
}
