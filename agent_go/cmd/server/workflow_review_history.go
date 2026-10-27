package server

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

type WorkflowReviewRecord struct {
	Revision   string               `json:"revision"`
	Trigger    string               `json:"trigger"`
	Reason     string               `json:"reason"`
	SessionID  string               `json:"session_id,omitempty"`
	StartedAt  string               `json:"started_at"`
	FinishedAt string               `json:"finished_at,omitempty"`
	Outcome    string               `json:"outcome"`
	Detail     string               `json:"detail,omitempty"`
	Unresolved []workflowReviewItem `json:"unresolved,omitempty"`
}

type workflowReviewItem struct {
	StepID string `json:"step_id"`
	Reason string `json:"reason"`
}

// LatestWorkflowReview reads old history without changing it or starting work.
func LatestWorkflowReview(workspacePath string) *WorkflowReviewRecord {
	path, err := fsutil.WorkspacePath(fsutil.WorkspaceDocsRoot(), strings.Trim(strings.TrimSpace(workspacePath), "/")+"/planning/workflow_review.json")
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var state struct {
		Latest *WorkflowReviewRecord `json:"latest"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return nil
	}
	return state.Latest
}
