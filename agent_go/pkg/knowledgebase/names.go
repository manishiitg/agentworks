package knowledgebase

import "strings"

// The public Brain tool names (PLAT-608). Every caller outside this package refers to a tool through these constants
// or ToolNames, so a name lives in one place. They are not the internal operation names (read_knowledgebase,
// update_knowledgebase, manage_knowledgebase_access, ...) that Service.Call dispatches on.
const (
	ToolBrowse = "brain_browse"
	ToolRead   = "brain_read"
	ToolUpdate = "brain_update"
	ToolBackup = "brain_backup"
	ToolSkills = "brain_skills"
	ToolAccess = "brain_access"
)

// ToolNames lists the public Brain tools in surface order.
func ToolNames() []string {
	return []string{ToolBrowse, ToolRead, ToolUpdate, ToolBackup, ToolSkills, ToolAccess}
}

// legacyToolNames are the names the tools had before the Brain rename (2026-10-06). They are accepted where a call
// comes in (external MCP/REST, CallTool) and converted at once, so every check below sees one name and a retried
// request_id dedupes whichever name it used. They are never advertised.
var legacyToolNames = map[string]string{
	"browse_knowledgebase":        ToolBrowse,
	"read_knowledgebase":          ToolRead,
	"update_knowledgebase":        ToolUpdate,
	"backup_knowledgebase":        ToolBackup,
	"knowledgebase_skills":        ToolSkills,
	"manage_knowledgebase_access": ToolAccess,
}

// CanonicalToolName maps a legacy Brain tool name to its current name; any other name is returned unchanged.
func CanonicalToolName(name string) string {
	if current, ok := legacyToolNames[strings.TrimSpace(name)]; ok {
		return current
	}
	return name
}
