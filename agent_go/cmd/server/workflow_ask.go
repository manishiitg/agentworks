package server

// Former builtin ask metadata is retained only to reject legacy function requests.
const workflowAskCreatedBy = "platform (workflow assistant)"

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
