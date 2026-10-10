package server

// Former builtin ask metadata is retained only to reject legacy function requests.
const workflowAskCreatedBy = "platform (workflow assistant)"

func workflowAskFunction() crewFunction {
	return crewFunction{
		Name:        crewFunctionAskName,
		Description: "Ask this workflow's assistant anything in free text: what it can do, what its runs did, or to run something (it picks the route and variables itself and reports the outcome). The answer is its final reply. Prefer a typed function when one fits.",
		InputSchema: map[string]interface{}{"type": "object", "required": []interface{}{"message"}, "properties": map[string]interface{}{
			"message": map[string]interface{}{"type": "string", "description": "Self-contained question or request; include every value a run needs (e.g. repo and PR number). The assistant does not see this conversation."},
		}},
		ResultSchema: map[string]interface{}{"type": "object", "required": []interface{}{"answer"}, "properties": map[string]interface{}{
			"answer": map[string]interface{}{"type": "string"},
		}},
		CreatedBy: workflowAskCreatedBy,
	}
}

func isWorkflowAsk(target triggerTarget, fn crewFunction) bool {
	return target.Kind == triggerCallerWorkflow && fn.Name == crewFunctionAskName && fn.CreatedBy == workflowAskCreatedBy
}

// workflowAskSessionExists reports whether the caller's assistant
// conversation already exists (live or in this workflow's saved history), so
// the next ask resumes it.
func (api *StreamingAPI) workflowAskSessionExists(sessionID, workspacePath string) bool {
	if _, ok := api.getActiveSession(sessionID); ok {
		return true
	}
	if _, exists, err := readWorkflowScopedChatHistoryConversationDirect(sessionID, workspacePath); err == nil && exists {
		return true
	}
	_, exists, err := readWorkflowScopedChatHistoryConversationFromWorkspace(sessionID, workspacePath)
	return err == nil && exists
}
