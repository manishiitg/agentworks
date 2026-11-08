package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// HTTP Relay tests retain their fixture until the product runtime is removed.
type externalRelayFixture struct {
	*builderOperationFixture
	mock *mockWorkspaceAPI
}

func newExternalRelayFixture(t *testing.T) *externalRelayFixture {
	t.Helper()
	f := newBuilderOperationFixture(t)
	withMemoryUserDirectory(t, `{"users":[{"id":"owner","username":"owner","role":"creator","products":["agentworks","relays"]},{"id":"outsider","username":"outsider","role":"editor","products":["agentworks","relays"]},{"id":"reader","username":"reader","role":"viewer","products":["agentworks","relays"]}]}`)
	m := NewWorkflowManifest("Relay")
	m.ID = "invoices"
	m.Kind = "relay"
	m.CreatedBy = "owner"
	m.RelayOutputStepID = "answer"
	m.Access = &WorkflowAccess{Owners: []string{"owner"}, Readers: []string{"reader", "outsider"}}
	m.Schedules = []WorkflowSchedule{{ID: "process", Name: "Process", Enabled: true, ScheduleType: "webhook", Kind: triggerKindFunction, WorkshopMode: "run", Function: &WorkflowFunctionSpec{Name: "process", Inputs: []string{"INPUT"}}}}
	raw, _ := json.Marshal(m)
	mock := &mockWorkspaceAPI{files: map[string]string{
		"Workflow/invoices/workflow.json":            string(raw),
		"Workflow/invoices/planning/plan.json":       `{"steps":[{"type":"agent","id":"answer","title":"Answer","description":"Reply","authored_prompt":true,"system_prompt":"Return JSON v1","next_step_id":"end","items":[{"id":"turn","type":"user_message","message":"{{input.name}}"}]}]}`,
		"Workflow/invoices/variables/variables.json": `{"variables":[{"name":"INPUT","type":"object","value":"{}"}]}`,
	}}
	ws := httptest.NewServer(mock)
	t.Cleanup(ws.Close)
	t.Setenv("WORKSPACE_API_URL", ws.URL)
	if err := saveWorkflowEntryActions(t.Context(), "Workflow/invoices", m); err != nil {
		t.Fatal(err)
	}
	return &externalRelayFixture{f, mock}
}
