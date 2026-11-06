package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	step "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

type crewGoalRequest struct {
	Action          string                 `json:"action"`
	Goal            step.CrewGoal          `json:"goal"`
	Metrics         []step.GoalMetric      `json:"metrics"`
	Observations    []step.GoalObservation `json:"observations"`
	SourceWorkspace string                 `json:"source_workspace"`
}

// The UI and native Crew tool use this one admission/mutation path. Run people
// can read shared outcome evidence, but cannot change targets or submit readings.
func (api *StreamingAPI) handleCrewGoals(w http.ResponseWriter, r *http.Request) {
	if api.projectActions == nil || api.projectActions.registry == nil {
		http.Error(w, "Crew services are unavailable", 503)
		return
	}
	caller := GetUserIDFromContext(r.Context())
	claims := GetUserFromContext(r.Context())
	if caller == "" || claims == nil || userAccessForClaims(claims).Disabled {
		http.Error(w, "Sign in to open this Crew.", 401)
		return
	}
	profile, err := api.projectActions.registry.Resolve("work", 0, caller)
	if err != nil {
		http.Error(w, "Crew is unavailable", 404)
		return
	}
	project, err := resolveCrewProjectBinding(r.Context(), caller, profile, r.URL.Query().Get("crew_id"), "")
	if err != nil {
		http.Error(w, "This Crew is unavailable. Open a Crew shared with you.", 403)
		return
	}
	root := project.Binding.WorkspacePath
	if isExternalCrewDirectCall(r.Context()) && !api.externalCrewReferenceAllowed(r.Context(), claims, root, r.Method != http.MethodGet) {
		http.Error(w, "This Crew is outside the connection's access.", 403)
		return
	}
	if r.Method == http.MethodPost {
		if !project.OwnedByCaller || !userAccessForClaims(claims).CanEdit {
			http.Error(w, "Ask a Crew owner to update its goals.", 403)
			return
		}
		if refuseCrewOwnerOnline(w, r, crewProjectIDOfRoot(root), caller) {
			return
		}
		var req crewGoalRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil {
			http.Error(w, "Enter a valid goal or measurement update.", 400)
			return
		}
		switch req.Action {
		case "save":
			if req.Goal.ID == "" {
				req.Goal.ID = uuid.NewString()
			}
			for _, id := range req.Goal.WorkflowIDs {
				binding, e := findCrewExecutableByID(id)
				if e != nil || binding == nil || binding.ID != id || binding.CrewRoot != root || (binding.Kind != "workflow" && binding.Kind != "script") {
					http.Error(w, "Choose workflows or scripts belonging to this Crew.", 400)
					return
				}
			}
			if err := step.SaveCrewGoal(r.Context(), root, req.Goal, req.Metrics); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		case "record":
			if err := recordCrewGoalObservations(r.Context(), root, req.SourceWorkspace, req.Observations); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		default:
			http.Error(w, "Choose save or record.", 400)
			return
		}
	}
	goals, err := step.LoadCrewGoals(r.Context(), root)
	if err != nil {
		http.Error(w, "Could not load goals.", 500)
		return
	}
	ledger, err := step.LoadGoalMeasurements(r.Context(), root, 500)
	if err != nil {
		http.Error(w, "Could not load measurements.", 500)
		return
	}
	progress := map[string]step.CrewGoalProgress{}
	for _, goal := range goals {
		progress[goal.ID] = step.SummarizeCrewGoal(goal, ledger, time.Now())
	}
	// Keep migrated dashboards on their original workflow/database. The Crew
	// offers them through the same report viewer, without copying SQL or files.
	dashboards := []map[string]string{}
	seen := map[string]bool{}
	defaultDashboard := root
	_, parentReport, reportErr := readFileFromWorkspace(r.Context(), root+"/db/reports/index.html")
	if reportErr != nil {
		http.Error(w, "Could not load dashboards.", 500)
		return
	}
	for _, goal := range goals {
		for _, id := range goal.WorkflowIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			binding, err := findCrewExecutableByID(id)
			if err != nil {
				http.Error(w, "Could not load dashboards.", 500)
				return
			}
			if binding == nil || binding.CrewRoot != root {
				continue
			}
			_, exists, err := readFileFromWorkspace(r.Context(), binding.Root+"/db/reports/index.html")
			if err != nil {
				http.Error(w, "Could not load dashboards.", 500)
				return
			}
			if !exists {
				continue
			}
			dashboards = append(dashboards, map[string]string{"workspace_path": binding.Root, "name": goal.Name})
			if !parentReport && defaultDashboard == root {
				defaultDashboard = binding.Root
			}
		}
	}
	writeAIJSON(w, map[string]any{"goals": goals, "measurements": ledger, "progress": progress, "can_edit": project.OwnedByCaller && userAccessForClaims(claims).CanEdit, "workspace_path": root, "dashboards": dashboards, "default_dashboard_workspace": defaultDashboard})
}

func recordCrewGoalObservations(ctx context.Context, root, source string, observations []step.GoalObservation) error {
	goals, err := step.LoadCrewGoals(ctx, root)
	if err != nil {
		return err
	}
	var binding *crewExecutableBinding
	if source != "" {
		binding, err = findCrewExecutable(source)
		if err != nil || binding == nil || binding.Root != source || binding.CrewRoot != root || (binding.Kind != "workflow" && binding.Kind != "script") {
			return fmt.Errorf("choose a workflow from this Crew")
		}
	}
	for i := range observations {
		o := &observations[i]
		found := false
		for _, goal := range goals {
			if !slices.Contains(goal.MetricIDs, o.Metric) {
				continue
			}
			found = true
			if binding != nil && !slices.Contains(goal.WorkflowIDs, binding.ID) {
				return fmt.Errorf("link this workflow to the goal before recording its measurements")
			}
		}
		if !found {
			return fmt.Errorf("measurement %s does not belong to a Crew goal", o.Metric)
		}
		if strings.TrimSpace(o.RunID) == "" {
			return fmt.Errorf("include the source run or collection ID")
		}
		hasEvidence := false
		for _, evidence := range o.Evidence {
			if strings.TrimSpace(evidence) != "" {
				hasEvidence = true
			}
		}
		if !hasEvidence {
			return fmt.Errorf("include the original source evidence for each measurement")
		}
		if binding != nil {
			// Namespace source IDs so two workflows' run-1 cannot collide. Preserve
			// the original reference in the evidence shown to the person.
			o.Evidence = append(o.Evidence, "Workflow "+binding.ID+", source run "+o.RunID)
			o.RunID = binding.ID + ":" + o.RunID
		}
	}
	_, err = step.RecordGoalObservations(ctx, root, observations)
	return err
}

func (api *StreamingAPI) registerCrewGoalTools(registrar definitionToolRegistrar, userID, crewRoot string) error {
	metric, observation := goalMetricSchemas()
	return registrar.RegisterCustomTool("crew_goals", "Track goals inside this Crew. list returns progress and evidence. save updates one goal and its complete metrics list, preserving other goals and observations. record adds source-backed measurements. Reuse IDs; never invent targets or evidence, or infer achievement from successful runs.", map[string]any{
		"type": "object", "required": []string{"action"}, "properties": map[string]any{
			"action": map[string]any{"type": "string", "enum": []string{"list", "save", "record"}},
			"goal": map[string]any{"type": "object", "properties": map[string]any{
				"id": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"}, "outcome": map[string]any{"type": "string"}, "status": map[string]any{"type": "string", "enum": []string{"active", "achieved", "stopped"}}, "workflow_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}},
			"metrics": map[string]any{"type": "array", "items": metric}, "observations": map[string]any{"type": "array", "items": observation}, "source_workspace": map[string]any{"type": "string"},
		},
	}, func(ctx context.Context, args map[string]any) (string, error) {
		ctx = internalBotRequestContext(ctx, userID)
		root := canonicalCrewWorkspaceRoot(crewRoot)
		identity, ok := projectIdentityOf(root)
		if !ok {
			return "", fmt.Errorf("open a Crew first")
		}
		record, ok := defaultProjectOwners().Lookup(identity.Product, identity.Folder)
		if !ok {
			return "", fmt.Errorf("Crew is unavailable")
		}
		method := http.MethodPost
		action, _ := args["action"].(string)
		if action == "list" {
			method = http.MethodGet
		}
		body, err := json.Marshal(args)
		if err != nil {
			return "", err
		}
		request, _ := http.NewRequestWithContext(ctx, method, "/api/crew-goals?crew_id="+url.QueryEscape(record.ProjectID), strings.NewReader(string(body)))
		response := &internalResponseCapture{header: http.Header{}}
		api.handleCrewGoals(response, request)
		if response.status >= 400 {
			return "", fmt.Errorf("%s", strings.TrimSpace(response.body.String()))
		}
		return response.body.String(), nil
	}, "work_workflow_reference_tools")
}
