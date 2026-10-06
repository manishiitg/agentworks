package server

import (
	"log"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// sanitizeUserIDForPath returns a userID safe to use as a filesystem path segment.
// Falls back to "default" when the input is empty, too long, or contains characters
// outside the allowed set. Used to route per-user folders under _users/<id>/.
func sanitizeUserIDForPath(userID string) string {
	return workspaceref.SanitizeUserID(userID)
}

// perUserChatsFolderFor returns the workspace-relative Chats folder path for a given user,
// e.g. "_users/alice/Chats". This is the per-user chat scratch / output folder — replacing
// the older global "Chats/" folder. Every multi-agent chat session defaults to this path,
// and it's propagated to sub-agents via ChatsFolderKey context so shell commands resolve correctly.
func perUserChatsFolderFor(userID string) string {
	return workspaceref.PhysicalPath(userID, "Chats")
}

// ConditionalWriteGrant is a declarative spec for extending a multi-agent chat
// session's folder guard based on runtime conditions (usually which skills the
// user has selected).
//
// Each grant can contribute:
//   - extra write paths appended to the chat's folder guard write list
//   - extra read-only paths appended to the read list
//   - a system prompt section appended to the agent's system prompt
//
// Grants are resolved once per request via resolveConditionalGrants(req),
// and the result is reused across every site that needs it:
//   - the main chat agent's workspace tool folder guard
//   - the main chat agent's browser tool folder guard
//   - the sub-agent executor's folder guard and browser tool folder guard
//   - the main chat agent's system prompt assembly
//   - the sub-agent's system prompt assembly
//
// Adding a new conditional grant is a single entry in conditionalGrants — no
// need to hunt through server.go for every folder-guard or prompt site.
type ConditionalWriteGrant struct {
	// Name is used for logging only (and as a stable key in AppliedNames).
	Name string

	// Trigger returns true when this grant should be applied for the current request.
	Trigger func(req QueryRequest) bool

	// WriteFolders are workspace-relative paths appended to the folder guard's
	// write list when Trigger is true. Keep the trailing slash (e.g. "subagents/custom/")
	// — the folder guard compares prefixes.
	WriteFolders []string

	// ReadOnlyExtra are workspace-relative paths appended to the read-only list.
	// Most grants don't need this (WriteFolders are implicitly readable); include
	// here only paths the agent needs to read but not write.
	ReadOnlyExtra []string

	// PromptSection is an optional system-prompt block to append when the grant
	// is active. Return "" from the function to skip.
	PromptSection func() string
}

// conditionalGrants is the authoritative registry of all conditional folder-guard
// extensions for multi-agent chat. Add a new grant here — one entry feeds every
// folder guard site and every system prompt assembly site automatically.
//
// NOTE: this only covers "folder-guard extension" style grants. Privileged-tool
// style grants (like create_workflow, which bypasses the folder guard via direct
// filesystem I/O) are registered separately via registerWorkflowCreatorTool.
var conditionalGrants = []ConditionalWriteGrant{
	{
		Name: "work-custom-skills",
		Trigger: func(req QueryRequest) bool {
			return req.AgentProfileID == "work"
		},
		PromptSection: func() string {
			return `## Custom skill authoring
When the user explicitly asks to preserve or improve a reusable procedure, the Work agent may create or update a focused custom skill under skills/<skill-name>/ in the current workspace. Use the workspace's existing write permissions. Keep the normal Work identity. Inspect existing skills first, update rather than duplicate when appropriate, and never store secrets in a skill.`
		},
	},
	{
		Name: "skill-creator",
		Trigger: func(req QueryRequest) bool {
			for _, s := range req.SelectedSkills {
				if s == "skill-creator" {
					return true
				}
			}
			return false
		},
		PromptSection: GetSkillBuilderInstructions,
	},
	{
		Name: "subagent-creator",
		Trigger: func(req QueryRequest) bool {
			for _, s := range req.SelectedSkills {
				if s == "subagent-creator" || s == "custom/subagent-creator" {
					return true
				}
			}
			return false
		},
		WriteFolders:  []string{"subagents/custom/"},
		PromptSection: GetSubAgentBuilderInstructions,
	},
}

// ResolvedGrants holds the merged results of applying every conditional grant
// whose Trigger fired for a single request. Safe to pass around and reuse across
// folder guard sites and system prompt assembly.
type ResolvedGrants struct {
	// WriteFolders are all extra write paths contributed by applied grants,
	// in registry order, with no deduplication. Append this to the base
	// extraFolders slice before calling the folder guard wrapper.
	WriteFolders []string

	// ReadOnlyExtra are extra read-only paths from applied grants.
	ReadOnlyExtra []string

	// PromptSections are system-prompt blocks from applied grants, in registry
	// order. Append each one to the agent's system prompt.
	PromptSections []string

	// AppliedNames lists the Name of each grant that fired, for logging and
	// quick "was this grant applied?" checks via HasGrant.
	AppliedNames []string
}

// HasGrant reports whether a grant with the given name was applied. Useful when
// a caller needs to branch on a specific grant (e.g. install-on-demand logic
// for skill-creator that fetches the skill from GitHub if missing).
func (r ResolvedGrants) HasGrant(name string) bool {
	for _, n := range r.AppliedNames {
		if n == name {
			return true
		}
	}
	return false
}

// resolveConditionalGrants walks the conditionalGrants registry and returns the
// merged write/read/prompt additions for the given request. Call once at request
// setup time and reuse the result everywhere that needs it.
func resolveConditionalGrants(req QueryRequest) ResolvedGrants {
	var r ResolvedGrants
	for _, g := range conditionalGrants {
		if g.Trigger == nil || !g.Trigger(req) {
			continue
		}
		r.AppliedNames = append(r.AppliedNames, g.Name)
		r.WriteFolders = append(r.WriteFolders, g.WriteFolders...)
		r.ReadOnlyExtra = append(r.ReadOnlyExtra, g.ReadOnlyExtra...)
		if g.PromptSection != nil {
			if section := g.PromptSection(); section != "" {
				r.PromptSections = append(r.PromptSections, section)
			}
		}
		log.Printf("[GRANT] Applied %q: write=%v read=%v", g.Name, g.WriteFolders, g.ReadOnlyExtra)
	}
	return r
}
