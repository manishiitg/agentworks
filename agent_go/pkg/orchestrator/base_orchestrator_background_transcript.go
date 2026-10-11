package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/backgroundtranscript"
	orchevents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

// AppendBackgroundAgentTranscriptEvent implements BackgroundAgentTranscriptWriter
// (PLAT-164). It is best-effort by design, matching every other
// background-agent observability write in this codebase (background_agent_log,
// token persistence above it in this file's package): a transcript-append
// failure must not fail the turn it is merely recording. The caller
// (ContextAwareEventBridge.persistTranscriptAsync) surfaces the returned
// error through WaitForBackgroundTranscriptPersistence rather than swallowing
// it silently.
func (bo *BaseOrchestrator) AppendBackgroundAgentTranscriptEvent(ctx context.Context, sessionID, agentID string, evt orchevents.BackgroundAgentTranscriptEvent) error {
	workspacePath := bo.GetWorkspacePath()
	if workspacePath == "" || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(agentID) == "" {
		return nil
	}
	filePath := orchevents.BackgroundAgentTranscriptPath(workspacePath, sessionID, agentID)
	unlock, deleted := backgroundtranscript.Lock(workspacePath, sessionID)
	defer unlock()
	if deleted {
		return nil
	}

	existingContent, err := bo.ReadWorkspaceFile(ctx, filePath)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "not found") || strings.Contains(errStr, "no such file") {
			// No header exists for this execution owner. cmd/server's
			// emitBackgroundAgentStarted is the ONLY writer that creates one,
			// and it does so synchronously at registration, before the agent
			// it registers can produce any provider event — so a missing
			// header here does not mean "the write is still in flight", it
			// means this ParentExecutionIDKey was never registered as a
			// background agent at all.
			//
			// PLAT-164 scope-fix (2026-08-21): this bridge cannot reliably
			// tell a real background agent's id apart from a workflow step's
			// own "exec-<step-id>-<timestamp>" id by shape alone — both reach
			// HandleEvent as a bare ParentExecutionIDKey value (the
			// pre-existing "workflow-step:"-prefix check this bridge also
			// carries has never matched anything in this codebase; real step
			// ids use "workflow-step-" or "exec-..."). Gating on "does a
			// registered header already exist" is what actually distinguishes
			// them: a plain workflow step already gets its own durable
			// conversation+timing record from controller_execution.go /
			// controller_todo_task.go, so skipping here — instead of
			// fabricating a new, orphaned, never-terminal transcript for it —
			// is correct, not a dropped event.
			return nil
		}
		return fmt.Errorf("read background agent transcript %s: %w", filePath, err)
	}

	transcript, err := orchevents.ParseBackgroundAgentTranscript(existingContent)
	if err != nil {
		// A registered header exists but is corrupt. Fabricating a fresh one
		// here would silently discard whatever it already held — surface the
		// failure instead (requirement 5) so it shows up in
		// background_agent_log.transcript_status rather than looking like a
		// clean, complete transcript.
		return fmt.Errorf("parse background agent transcript %s: %w", filePath, err)
	}
	if transcript == nil {
		// Empty file content — the header write always has content, so this
		// should not happen in practice. Nothing to append to yet.
		return nil
	}
	transcript.AppendEvent(evt)

	content, err := transcript.Marshal()
	if err != nil {
		return fmt.Errorf("marshal background agent transcript %s: %w", filePath, err)
	}
	if err := bo.WriteWorkspaceFile(ctx, filePath, content); err != nil {
		return fmt.Errorf("write background agent transcript %s: %w", filePath, err)
	}
	return nil
}
