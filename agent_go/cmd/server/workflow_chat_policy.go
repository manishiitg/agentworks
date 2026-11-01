package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/agentworksproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/caplayerproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/relayproduct"
)

type workflowChatPolicy struct {
	Mode   string
	Origin string
	// RoleOrigin is the origin the chat's ROLE is judged by. It equals Origin, except for an auto-notification or agent
	// message into an existing chat: that turn keeps the role (origin and authority) of the chat's last other turn, so
	// it does not look like a different chat (PLAT-548, round 2). Origin itself stays "notification" for provenance and
	// for the checks that only a typed turn passes.
	RoleOrigin          string
	Capabilities        map[string]bool
	AuthorityKey        string
	ProductProfileID    string
	ChatInstructionsKey string
	KnowledgeKey        string
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
	workspace := req.SelectedFolder
	if workspace == "" && active != nil {
		workspace = active.WorkspacePath
	}
	roleOrigin, authority := origin, req.ExternalBuilderOperationID
	if origin == "notification" {
		if snapshot, ok := chatPolicyRoleSnapshotFor(session); ok {
			roleOrigin, authority = snapshot.Origin, snapshot.Authority
		}
	} else if strings.TrimSpace(session) != "" {
		chatPolicyRoles.Store(session, chatPolicyRoleSnapshot{Origin: origin, Authority: authority})
	}
	return workflowChatPolicy{ChatInstructionsKey: projectChatInstructionsKey(workspace), KnowledgeKey: knowledgeRuntimeConfigKey(workspace), Mode: normalized, Origin: origin, RoleOrigin: roleOrigin, Capabilities: agentworksproduct.ChatCapabilities(normalized, roleOrigin, readOnly), AuthorityKey: authority, ProductProfileID: strings.TrimSpace(req.AgentProfileID)}
}

// describe is the readable form of the parts that make up sessionKey, for the log line that says why a chat's native
// session was replaced (never contains config contents or secrets).
func (p workflowChatPolicy) describe() string {
	names := make([]string, 0, len(p.Capabilities))
	for name, on := range p.Capabilities {
		if on {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	caps := strings.Join(names, ",")
	if len(caps) > 400 {
		caps = caps[:400] + "..."
	}
	return fmt.Sprintf("mode=%s origin=%s profile=%q knowledge=%t authority=%t caps=[%s]", p.Mode, p.Origin, p.ProductProfileID, p.KnowledgeKey != "", p.AuthorityKey != "", caps)
}

// lastChatPolicyDescription remembers, per session, the describe() of the previous turn's policy (log only).
var lastChatPolicyDescription sync.Map

// chatPolicyRoleSnapshot is the role (origin and authority) of a chat's last turn that was not an auto-notification.
type chatPolicyRoleSnapshot struct{ Origin, Authority string }

var chatPolicyRoles sync.Map // session ID -> chatPolicyRoleSnapshot

func chatPolicyRoleSnapshotFor(session string) (chatPolicyRoleSnapshot, bool) {
	value, ok := chatPolicyRoles.Load(session)
	if !ok {
		return chatPolicyRoleSnapshot{}, false
	}
	snapshot, ok := value.(chatPolicyRoleSnapshot)
	return snapshot, ok
}

// seedChatPolicyRole restores the role of a chat after a restart from what its saved runtime recorded.
func seedChatPolicyRole(session, origin, authority string) {
	if strings.TrimSpace(session) == "" || strings.TrimSpace(origin) == "" {
		return
	}
	chatPolicyRoles.LoadOrStore(session, chatPolicyRoleSnapshot{Origin: origin, Authority: authority})
}

func (p workflowChatPolicy) allows(capability string) bool { return p.Capabilities[capability] }

func (p workflowChatPolicy) sessionKey() string {
	names := make([]string, 0, len(p.Capabilities))
	for name := range p.Capabilities {
		names = append(names, name)
	}
	sort.Strings(names)
	roleOrigin := p.Origin
	if p.RoleOrigin != "" {
		roleOrigin = p.RoleOrigin
	}

	// An auto-notification is a message into the same interactive chat. As its own role it threw the
	// coding CLI's session away whenever a notification and a typed message alternated, so the chat
	// kept showing "Conversation restored" (Upwork chat, 2026-10-05). The capability set below still
	// starts a fresh session if a notification turn ever gets different tools.
	if roleOrigin == "notification" {
		roleOrigin = "interactive"
	}
	identity := p.Mode + "|" + roleOrigin + "|" + strings.Join(names, ",")
	if p.ChatInstructionsKey != "" {
		identity += "|chat_instructions:" + p.ChatInstructionsKey
	}
	if p.KnowledgeKey != "" {
		identity += "|knowledge:" + p.KnowledgeKey
	}
	if p.ProductProfileID != "" {
		identity += "|profile:" + p.ProductProfileID
	}
	// Only THAT the turn runs under an external Builder operation's authority is part of the role, not which operation:
	// every MCP builder chat request mints a new operation id on the same session, and a new id each turn made every turn
	// look like a different chat (PLAT-548 round 2). The id still authorizes the turn elsewhere.
	if p.AuthorityKey != "" {
		identity += "|external-authority"
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
// session. Claude and Cursor adapters restate changed host instructions inline;
// Codex app-server updates baseInstructions on resume. Verified with native
// same-machine and relocated sessions (PLAT-856-442/447/448). They do; muse-cli keeps the project rules the session started with and also
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
			// Vault's product profile explicitly owns the direct, governed call
			// bridge. AgentWorks' workflow manifest must not remove that entry;
			// the profile gate and execution-time identity checks still apply.
			if name == "call_mcp_tool" && policy.ProductProfileID == caplayerproduct.ProfileID {
				return disabled != nil && disabled(name)
			}
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

// An MCP Builder turn admits linked workflow context exactly like the website.
func admitWorkflowBuilderContextPaths(ctx context.Context, req *QueryRequest) error {
	return admitTurnContextPaths(ctx, req)
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
	return true
}
