package knowledgebase

// The public Brain tool names (PLAT-608). Every caller outside this package refers to a tool through these constants
// or ToolNames, so a name lives in one place. They are not the internal operation names (read_knowledgebase,
// update_knowledgebase, manage_knowledgebase_access, ...) that Service.Call dispatches on.
const (
	ToolBrowse = "brain_browse"
	ToolRead   = "brain_read"
	ToolUpdate = "brain_update"
	ToolSkills = "brain_skills"
	ToolAccess = "brain_access"
)

// ToolNames lists the public Brain tools in surface order.
func ToolNames() []string {
	return []string{ToolBrowse, ToolRead, ToolUpdate, ToolSkills, ToolAccess}
}
