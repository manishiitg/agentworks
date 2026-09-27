package step_based_workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRenderAuthoredPrompt(t *testing.T) {
	variables := map[string]string{"INPUT": `{"customer":{"name":"Asha"},"count":2,"enabled":true}`}
	got, err := renderAuthoredPrompt(`For {{input.customer.name}}, use {{input.count}} items; enabled={{input.enabled}}.`, variables)
	if err != nil || got != "For Asha, use 2 items; enabled=true." {
		t.Fatalf("render = %q, %v", got, err)
	}
	for _, prompt := range []string{"{{input.customer.missing}}", "{{steps.other.output}}", "{{input.customer.name}}"} {
		values := variables
		if strings.Contains(prompt, "name") {
			values = nil
		}
		if _, err := renderAuthoredPrompt(prompt, values); err == nil {
			t.Fatalf("%q should fail when its input is unavailable", prompt)
		}
	}
}

func TestAuthoredPromptPlanRoundTrip(t *testing.T) {
	const source = `{"type":"message_sequence","id":"answer","title":"Answer","description":"Answer the call","authored_prompt":true,"system_prompt":"Return JSON","items":[{"id":"turn","type":"user_message","message":"{{input.question}}"}]}`
	var step MessageSequencePlanStep
	if err := json.Unmarshal([]byte(source), &step); err != nil {
		t.Fatal(err)
	}
	if !step.AuthoredPrompt || step.SystemPrompt != "Return JSON" {
		t.Fatalf("authored fields lost: %+v", step)
	}
	if err := validateMessageSequenceStepFieldsTyped(&step); err != nil {
		t.Fatalf("authored step rejected: %v", err)
	}
	encoded, err := json.Marshal(&step)
	if err != nil || !strings.Contains(string(encoded), `"authored_prompt":true`) {
		t.Fatalf("round trip = %s, err = %v", encoded, err)
	}
}

func TestAuthoredPromptPlanUpdate(t *testing.T) {
	step := &MessageSequencePlanStep{SystemPrompt: "old"}
	enabled := true
	prompt := "Return JSON"
	updated := mergePartialStepUpdate(step, PartialPlanStep{AuthoredPrompt: &enabled, SystemPrompt: &prompt}).(*MessageSequencePlanStep)
	if !updated.AuthoredPrompt || updated.SystemPrompt != prompt || step.AuthoredPrompt || step.SystemPrompt != "old" {
		t.Fatalf("update did not preserve authored fields or mutated input: updated=%+v old=%+v", updated, step)
	}
}

func TestNormalizeAuthoredJSONResult(t *testing.T) {
	got, err := normalizeAuthoredJSONResult(" \n { \"ok\": true } \n")
	if err != nil || got != `{"ok":true}` {
		t.Fatalf("result = %q, %v", got, err)
	}
	if _, err := normalizeAuthoredJSONResult("Here is the result: {\"ok\":true}"); err == nil {
		t.Fatal("prose must not be published as JSON")
	}
}

func TestRenderAuthoredPromptWithStepOutput(t *testing.T) {
	loads := 0
	got, err := renderAuthoredPromptWithSteps(`Prior: {{steps.classify.output.category}}, full={{steps.classify.output}}`, nil, func(id string) (string, error) {
		loads++
		if id != "classify" {
			t.Fatalf("loaded unexpected step %q", id)
		}
		return `{"category":"urgent"}`, nil
	})
	if err != nil || got != `Prior: urgent, full={"category":"urgent"}` || loads != 1 {
		t.Fatalf("render = %q, loads = %d, err = %v", got, loads, err)
	}
	if _, err := renderAuthoredPromptWithSteps("{{steps...output.secret}}", nil, func(string) (string, error) {
		t.Fatal("invalid step ID reached loader")
		return "", nil
	}); err == nil {
		t.Fatal("invalid step ID accepted")
	}
}
