package knowledgebase

// The public Brain tool names (PLAT-608). Every caller outside this package refers to a tool through these constants
// or ToolNames, so a name lives in one place. They are not the internal operation names (read_knowledgebase,
// update_knowledgebase, manage_knowledgebase_access, ...) that Service.Call dispatches on.
const (
	ToolBrowse = "browse_knowledgebase"
	ToolRead   = "read_knowledgebase"
	ToolUpdate = "update_knowledgebase"
	ToolBackup = "backup_knowledgebase"
	ToolSkills = "knowledgebase_skills"
	ToolAccess = "manage_knowledgebase_access"
)

// ToolNames lists the public Brain tools in surface order.
func ToolNames() []string {
	return []string{ToolBrowse, ToolRead, ToolUpdate, ToolBackup, ToolSkills, ToolAccess}
}
