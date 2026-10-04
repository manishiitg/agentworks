package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
)

type workflowChatPolicy struct {
	Mode         string
	Origin       string
	Capabilities map[string]bool
	AuthorityKey string
}

// readOnlyForRequest decides whether a turn runs with read-only treatment.
// A read-only workflow identity is always read-only; PinRunMode additionally
// lets a caller voluntarily take read-only treatment for one turn. The pin
// is downgrade-only — it keeps the Run-mode tool surface while withholding
// authoring, and grants nothing — so external execution-only callers use it
// to run without authoring, whatever their workflow access.
func readOnlyForRequest(access WorkflowAccessLevel, req QueryRequest) bool {
	return access == WorkflowAccessRead || req.PinRunMode
}

// Resolve the complete execution profile from access and server-maintained
// provenance. There is deliberately no caller-supplied mode input: workflow
// access is the authority boundary (writable => Builder, read-only => Run),
// while origin can only narrow the resulting capability set. A missing field
// on a resumed turn must never promote a schedule/child.
func resolveWorkflowChatPolicy(session string, req QueryRequest, active *ActiveSessionInfo, readOnly bool) workflowChatPolicy {
	origin := "interactive"
	switch {
	case req.ExternalBuilderOperationID != "":
		origin = "external_builder"
	case req.ParentSessionID != "" || req.SessionKind != "" || active != nil && (active.ParentSessionID != "" || active.SessionKind != ""):
		origin = "child"
	case req.BotPlatform != "" || active != nil && active.BotPlatform != "":
		origin = "bot"
	case req.IsAutoNotification || strings.EqualFold(strings.TrimSpace(req.TriggeredBy), "auto_notification"):
		origin = "notification"
	case req.PulseLifecycleTurn:
		origin = "pulse"
	case !req.UserInteractiveContinuation && (isScheduledSessionIdentity(session, req.TriggeredBy) || active != nil && isScheduledSessionIdentity(session, active.TriggeredBy)):
		origin = "scheduled"
	}
	// Conversational workflow authority is access-derived. Legacy/client mode
	// fields cannot widen or reduce the authenticated principal. The direct
	// headless workflow executor is not a conversation and remains Run.
	normalized := "builder"
	if readOnly || strings.TrimSpace(req.AgentMode) == "workflow" {
		normalized = "run"
	}
	return workflowChatPolicy{Mode: normalized, Origin: origin, Capabilities: agentworksproduct.ChatCapabilities(normalized, origin, readOnly), AuthorityKey: req.ExternalBuilderOperationID}
}

func (p workflowChatPolicy) allows(capability string) bool { return p.Capabilities[capability] }

func (p workflowChatPolicy) sessionKey() string {
	names := make([]string, 0, len(p.Capabilities))
	for name := range p.Capabilities {
		names = append(names, name)
	}
	sort.Strings(names)
	identity := p.Mode + "|" + p.Origin + "|" + strings.Join(names, ",")
	if p.AuthorityKey != "" {
		identity += "|" + p.AuthorityKey
	}
	sum := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("%x", sum[:16])
}

func (api *StreamingAPI) chatPolicySessionKey(p workflowChatPolicy, workflowKind ...string) string {
	h := sha256.New()
	h.Write([]byte(p.sessionKey()))
	if len(workflowKind) > 0 && workflowKind[0] == "relay" {
		key, err := relayproduct.BuilderDefinitionKey()
		if err != nil {
			key = "invalid-relay-builder:" + err.Error()
		}
		h.Write([]byte(key))
	} else {
		h.Write([]byte(agentworksproduct.ChatDefinitionKey(p.Mode)))
	}
	// Never log config contents or secret values. A changed install/auth/config
	// causes the existing durable-history reconnect path on the next user turn.
	for _, path := range []string{api.mcpConfigPath, api.getUserConfigPath()} {
		data, err := os.ReadFile(path)
		if err == nil {
			h.Write(data)
		}
		h.Write([]byte{0})
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// codingProviderReloadsInstructionsOnResume reports whether a coding CLI picks
// up a changed system prompt when AgentWorks relaunches it on the same native
// session. Verified 2026-09-24 with a rule changed between turns: claude-code
// (CLAUDE.md re-read per launch), codex-cli (AGENTS.md re-read on resume) and
// cursor-cli (the adapter resends a changed prompt inline, which it follows)
// do; muse-cli keeps the project rules the session started with and also
// ignores an inline override. pi-cli passes the prompt as a flag every launch.
func codingProviderReloadsInstructionsOnResume(provider string) bool {
	return !strings.EqualFold(strings.TrimSpace(provider), "muse-cli")
}

// chatPolicyRoleRequiresReconnect decides whether a coding-agent session must
// be replaced because the chat's role changed. Saved runtimes from before the
// role key existed carry no role information; their mode is still compared by
// the caller, so they resume rather than lose the conversation.
func chatPolicyRoleRequiresReconnect(codingProvider bool, previous, current string, known bool, saved *ChatHistoryAgentRuntime) bool {
	if !codingProvider {
		return false
	}
	if known {
		return previous != current
	}
	if saved == nil || strings.TrimSpace(saved.ChatPolicyRoleKey) == "" {
		return false
	}
	hasResume := saved.ExternalSessionID != "" || saved.AgentSessionHandle != nil && !saved.AgentSessionHandle.Empty()
	return hasResume && saved.ChatPolicyRoleKey != current
}

func chatPolicyRequiresReconnect(codingProvider bool, previous, current string, known bool, saved *ChatHistoryAgentRuntime) bool {
	if !codingProvider {
		return false
	}
	if known {
		return previous != current
	}
	if saved == nil {
		return false
	}
	hasResume := saved.ExternalSessionID != "" || saved.AgentSessionHandle != nil && !saved.AgentSessionHandle.Empty()
	return hasResume && saved.ChatPolicyKey != current
}

// Registration is shared by normal chat and workflow-phase construction, before
// finalization/catalog publication. Other products retain their manifest gate.
func (api *StreamingAPI) registerMCPToolsForChat(registrar definitionToolRegistrar, policy workflowChatPolicy, disabled func(string) bool) error {
	if policy.allows("mcp_management") {
		return api.registerMultiAgentMCPServerTools(registrar, func(name string) bool {
			return !agentworksproduct.ChatAllowsTool(policy.Mode, name) || disabled != nil && disabled(name)
		})
	}
	if !policy.allows("mcp_inspection") {
		return nil
	}
	// Run mode lists the servers and their status and changes nothing.
	// Without it an agent told to confirm a server before using it (the
	// crew's work-mcp skill) reports connected tools as unavailable.
	return api.registerMultiAgentMCPServerTools(registrar, func(name string) bool {
		return name != "list_mcp_servers" || disabled != nil && disabled(name)
	})
}

// Persist the same access-derived conversational mode used for tool admission.
// Old clients and restored chats may still submit the mode from before an
// access change; it must not select the CLI directory or reconnect metadata.
func normalizeWorkflowConversationMode(req *QueryRequest, readOnly bool) {
	if req == nil || req.AgentProfileID != "" || req.AgentMode != "workflow_phase" || req.PhaseID != "workflow-builder" {
		return
	}
	if req.ExecutionOptions == nil {
		req.ExecutionOptions = &ExecutionOptions{}
	} else {
		options := *req.ExecutionOptions
		req.ExecutionOptions = &options
	}
	req.ExecutionOptions.WorkshopMode = "workshop"
	if resolveWorkflowChatPolicy("", *req, nil, readOnly).Mode == "run" {
		req.ExecutionOptions.WorkshopMode = "run"
	}
}

// workflowChatNativeAgentTools reports whether this workflow chat turn runs
// with the coding CLI's native tools (agent_tools full). Owner decision
// 2026-09-29: on for every turn type — interactive Builder and Run chats,
// schedules, webhooks and triggers, Pulse, Slack and WhatsApp — unless the
// workflow's "Native agent tools" switch is off. Read-only principals stay
// off, and so do workflow step agents: child sessions of a run (a Pulse
// reviewer child is a Pulse turn, not a step).
func (api *StreamingAPI) workflowChatNativeAgentTools(ctx context.Context, req QueryRequest, sessionID string, readOnly bool) bool {
	if readOnly || strings.TrimSpace(req.AgentMode) != "workflow_phase" || strings.TrimSpace(req.SelectedFolder) == "" {
		return false
	}
	if api.isWorkflowStepTurn(req, sessionID) {
		return false
	}
	manifest, found, err := ReadWorkflowManifest(ctx, req.SelectedFolder)
	return err == nil && found && manifest != nil && manifest.Capabilities.NativeAgentToolsEnabled()
}

// errRetiredGeneralChat is the refusal for a request that names neither a
// workflow nor a product: there is no general-purpose chat, only workflows,
// Crews and Code.
var errRetiredGeneralChat = errors.New("there is no general chat; start a chat in a workflow, a Crew or a Code")

// isRetiredGeneralChat reports whether req is an interactive multi-agent chat
// with no workflow, Crew or Code behind it. Bots, schedules, triggers, child
// sessions and auto-notifications are not interactive chats and are not refused.
func isRetiredGeneralChat(req *QueryRequest, profile *resolvedAgentProfile, sessionID string) bool {
	return req != nil && profile == nil &&
		req.AgentMode == "multi-agent" &&
		!req.IsAutoNotification &&
		strings.TrimSpace(req.BotPlatform) == "" &&
		strings.TrimSpace(req.TriggeredBy) == "" &&
		strings.TrimSpace(req.ParentSessionID) == "" &&
		strings.TrimSpace(req.SessionKind) == "" &&
		strings.TrimSpace(req.AgentProfileID) == "" &&
		!isScheduledSessionIdentity(sessionID, req.TriggeredBy)
}

// External Builder intentionally has no cross-workflow context or filesystem
// grants. Its visible transcript stays in the same workflow's own builder tree.
func admitWorkflowBuilderContextPaths(ctx context.Context, req *QueryRequest) error {
	if req.ExternalBuilderOperationID == "" {
		return admitTurnContextPaths(ctx, req)
	}
	req.WorkflowContextPaths = nil
	req.authorizedWorkflowContextReadPaths = nil
	return nil
}

func externalBuilderFolderPaths(workspace string) (read, write []string) {
	root := path.Clean(strings.TrimSpace(workspace))
	if !strings.HasPrefix(root, "Workflow/") || root == "Workflow/" {
		return nil, nil
	}
	return []string{root + "/"}, []string{root + "/"}
}

func externalBuilderHostDownloads(req QueryRequest, session string) string {
	if req.ExternalBuilderOperationID != "" {
		return ""
	}
	return common.GrantSessionCDPHostDownloadsReadWrite(session, hostDownloadsBrowserMode(req))
}

// Phase tools are registered through the same source of truth as browser
// Builder, intersected with this operation's explicit tool boundary.
type externalBuilderDefinitionRegistrar struct {
	definitionRegistrar
	claims *UserClaims
}

func externalBuilderRegistrar(registrar definitionRegistrar, claims *UserClaims) definitionRegistrar {
	if claims == nil || claims.ExternalBuilderOperationID == "" {
		return registrar
	}
	return externalBuilderDefinitionRegistrar{registrar, claims}
}

func (r externalBuilderDefinitionRegistrar) RegisterCustomTool(name, description string, schema map[string]interface{}, run func(context.Context, map[string]interface{}) (string, error), category string) error {
	if externalBuilderToolDenied(r.claims, name) {
		return nil
	}
	return r.definitionRegistrar.RegisterCustomTool(name, description, schema, auditExternalBuilderPlanTool(name, run), category)
}

func (r externalBuilderDefinitionRegistrar) RegisterCustomToolWithTimeout(name, description string, schema map[string]interface{}, run func(context.Context, map[string]interface{}) (string, error), timeout time.Duration, category string) error {
	if externalBuilderToolDenied(r.claims, name) {
		return nil
	}
	return r.definitionRegistrar.RegisterCustomToolWithTimeout(name, description, schema, auditExternalBuilderPlanTool(name, run), timeout, category)
}

// isWorkflowStepTurn reports a child session of a workflow run (a step
// agent), which keeps AgentWorks-only tools. A Pulse child session is a
// Pulse turn and is not a step.
func (api *StreamingAPI) isWorkflowStepTurn(req QueryRequest, sessionID string) bool {
	parent, kind := strings.TrimSpace(req.ParentSessionID), strings.TrimSpace(req.SessionKind)
	if api != nil {
		if active, ok := api.getActiveSession(sessionID); ok && active != nil {
			parent = firstNonEmptyTrimmed(parent, active.ParentSessionID)
			kind = firstNonEmptyTrimmed(kind, active.SessionKind)
		}
	}
	if parent == "" && kind == "" {
		return false
	}
	return !strings.HasPrefix(strings.ToLower(kind), "pulse")
}
