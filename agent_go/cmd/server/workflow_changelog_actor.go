package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	step_based_workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// changelogActor is the authenticated person behind a workflow change: the
// signed-in caller of a website, Builder chat or MCP request, or the owner an
// automatic job runs as.
func changelogActor(ctx context.Context) (userID, username string) {
	if claims := GetUserFromContext(ctx); claims != nil {
		userID, username = strings.TrimSpace(claims.UserID), strings.TrimSpace(claims.Username)
	} else {
		userID, _ = ctx.Value(common.UserIDKey).(string)
		username, _ = ctx.Value(common.UsernameKey).(string)
		userID, username = strings.TrimSpace(userID), strings.TrimSpace(username)
	}
	if username == missingServerLogContextValue {
		username = ""
	}
	if userID != "" && username == "" {
		username = logUsernameForUserID(userID)
	}
	return userID, username
}

// withChangelogActor attributes change log entries written under ctx to the
// authenticated person. Settings saves (write_workflow_manifest) reached the
// change log without a plan-tool origin and so recorded nobody.
func withChangelogActor(ctx context.Context) context.Context {
	userID, username := changelogActor(ctx)
	return step_based_workflow.WithWorkflowSettingsOrigin(ctx, userID, username)
}

// knowledgeChangelogTextLimit bounds how much note text one entry keeps; larger
// files are recorded by size and hash only.
const knowledgeChangelogTextLimit = 4000

func knowledgeChangelogValue(content string, exists bool) map[string]interface{} {
	if !exists {
		return map[string]interface{}{"exists": false}
	}
	sum := sha256.Sum256([]byte(content))
	value := map[string]interface{}{"exists": true, "size": len(content), "sha256": hex.EncodeToString(sum[:])}
	if len(content) <= knowledgeChangelogTextLimit && utf8.ValidString(content) {
		value["text"] = content
	}
	return value
}

// logWorkflowKnowledgeChange records a Builder or MCP write to a workflow's
// knowledgebase/ in the workflow change log, with the same origin shape as plan
// edits. Run-time step writes do not come through these managed paths.
func logWorkflowKnowledgeChange(ctx context.Context, workspacePath, rel, tool, before string, beforeExists bool, after string) {
	rel = path.Clean(strings.TrimPrefix(rel, "/"))
	if !strings.HasPrefix(rel, "knowledgebase/") || (beforeExists && before == after) {
		return
	}
	logCtx := withChangelogActor(context.WithValue(context.WithoutCancel(ctx), teamCommitPermitKey{}, true))
	step_based_workflow.LogCanonicalArtifactChange(
		logCtx, workspacePath, tool, "Knowledge base file "+rel+" changed.",
		[]step_based_workflow.PlanFieldChange{{Field: rel, OldValue: knowledgeChangelogValue(before, beforeExists), NewValue: knowledgeChangelogValue(after, true)}},
		workflowManifestChangelogReader, writeFileToWorkspace, createServerLogger(),
		rel, knowledgeChangelogValue(before, beforeExists), knowledgeChangelogValue(after, true),
	)
}
