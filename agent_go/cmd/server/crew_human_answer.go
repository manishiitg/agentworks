package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

// Crew suggestions use the same decision lifecycle as workflow decisions, but
// only the person's own interactive Builder turn may record their answer.
func crewHumanAnswerTurn(session string, req QueryRequest, active *ActiveSessionInfo, readOnly bool) bool {
	policy := resolveWorkflowChatPolicy(session, req, active, readOnly)
	return isProjectProfileID(req.AgentProfileID) && !req.PinRunMode && registerWorkUIAllowed(req) &&
		req.CodeChatMode != "local" && req.CodeLocalFiles == nil &&
		policy.Mode == "builder" && policy.Origin == "interactive" &&
		(active == nil || !active.IsSyntheticTurn)
}

func (api *StreamingAPI) crewHumanAnswerScope(ctx context.Context, session, requested string) (string, *UserClaims, bool, error) {
	if api == nil {
		return "", nil, false, nil
	}
	api.lastQueryMu.RLock()
	req, found := api.lastQueryRequests[session]
	api.lastQueryMu.RUnlock()
	if !found || !isProjectProfileID(req.AgentProfileID) {
		return "", nil, false, nil
	}
	refuse := func() (string, *UserClaims, bool, error) {
		return "", nil, true, fmt.Errorf("only a person in this Crew's Builder chat can answer its decisions; open the Crew's Builder chat")
	}
	active, _ := api.getActiveSession(session)
	readOnly := false
	if cfg := common.GetSessionShellConfig(session); cfg != nil {
		readOnly = cfg.CrewReader || cfg.ReadOnlyAccess || cfg.WorkflowReadOnly
	}
	if !crewHumanAnswerTurn(session, req, active, readOnly) || api.eventStore == nil {
		return refuse()
	}
	actor := strings.TrimSpace(api.eventStore.GetSessionOwner(session))
	if actor == "" || actor != req.userID {
		return refuse()
	}
	claims := GetUserFromContext(ctx)
	if claims == nil {
		claims = &UserClaims{UserID: actor}
	}
	if claims.UserID != actor || !websiteDeviceClaims(claims) || claims.ExternalBuilderOperationID != "" {
		return refuse()
	}
	access := userAccessForClaims(claims)
	if access.Disabled || !access.CanEdit || directoryUserIsUnknown(claims) {
		return refuse()
	}
	ref, ok := resolveCrewPath(ctx, actor, req.SelectedFolder)
	if !ok || ref.Rest != "" || crewAccessFor(claims, ref) != crewAccessOwner {
		return refuse()
	}
	if requested = strings.TrimSpace(requested); requested != "" {
		target, valid := resolveCrewPath(ctx, actor, requested)
		if !valid || target.Rest != "" || target.Root != ref.Root {
			return "", nil, true, fmt.Errorf("workspace_path must be this Crew (%s)", ref.Root)
		}
	}
	return ref.Root, claims, true, nil
}

func (api *StreamingAPI) registerCrewHumanAnswerTool(registrar definitionToolRegistrar, gate *productToolGate) error {
	tools, executors, categories := createReportHumanInputTools()
	const name = "answer_human_input_request"
	for _, tool := range tools {
		if tool.Function == nil || tool.Function.Name != name {
			continue
		}
		var params map[string]interface{}
		raw, err := json.Marshal(tool.Function.Parameters)
		if err != nil {
			return err
		}
		if err = json.Unmarshal(raw, &params); err != nil {
			return err
		}
		gate.Declare(name)
		return registrar.RegisterCustomTool(name, tool.Function.Description, params,
			executors[name].(func(context.Context, map[string]interface{}) (string, error)), categories[name])
	}
	return fmt.Errorf("Crew decision answer tool is unavailable")
}
