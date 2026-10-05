// Custom tool names by category.
// These should match the LLM-visible backend categories. Internal-only
// workspace_basic executors are intentionally omitted here.

// workspace_advanced: advanced tools (shell, PDF, text generation, diff patch)
// Maps to backend "workspace_advanced" category
export const WORKSPACE_ADVANCED_TOOLS = [
  'execute_shell_command',
  'read_image',
  'read_pdf',
  'generate_text_llm',
  'diff_patch_workspace_file',
] as const;

// workspace_browser: 1 browser automation tool
// Maps to backend "workspace_browser" category
export const WORKSPACE_BROWSER_TOOLS = [
  'agent_browser',
] as const;

// All LLM-visible workspace tools (combined) - for backward compatibility with "workspace_tools" category
export const WORKSPACE_TOOLS = [
  ...WORKSPACE_ADVANCED_TOOLS,
  ...WORKSPACE_BROWSER_TOOLS,
] as const;

export const HUMAN_TOOLS = [
  'human_feedback',
] as const;

export type WorkspaceAdvancedToolName = typeof WORKSPACE_ADVANCED_TOOLS[number];
export type WorkspaceBrowserToolName = typeof WORKSPACE_BROWSER_TOOLS[number];
export type WorkspaceToolName = typeof WORKSPACE_TOOLS[number];
export type HumanToolName = typeof HUMAN_TOOLS[number];
export type CustomToolName = WorkspaceToolName | HumanToolName;

// Helper to get all tools for a category
// Supports: workspace_tools (all visible workspace tools), workspace_advanced, workspace_browser, human_tools
export function getToolsByCategory(category: string, capabilities?: unknown): string[] {
  void capabilities

  switch (category) {
    case 'workspace_tools':
      // Backward compatible - returns all LLM-visible workspace tools
      return [...WORKSPACE_TOOLS];
    case 'workspace_advanced':
      // Advanced tools (shell + PDF + text generation + web search + diff patch)
      return [...WORKSPACE_ADVANCED_TOOLS];
    case 'workspace_browser':
      return [...WORKSPACE_BROWSER_TOOLS];
    case 'human_tools':
      return [...HUMAN_TOOLS];
    default:
      return [];
  }
}

// Helper to get all available custom tools
export function getAllCustomTools(capabilities?: unknown): string[] {
  void capabilities;

  return [
    ...WORKSPACE_ADVANCED_TOOLS,
    ...WORKSPACE_BROWSER_TOOLS,
    ...HUMAN_TOOLS
  ];
}

// Normalize MCP tool names: strip "mcp__<server>__" prefix so routing reuses existing components
export const normalizeMCPToolName = (toolName: string): string => {
  if (toolName.startsWith('mcp__')) {
    const parts = toolName.slice('mcp__'.length).split('__')
    return parts.slice(1).join('__') || toolName
  }
  return toolName
}

// Helper to get the category for a specific tool name
export function getCategoryForTool(toolName: string): string | null {
  if (WORKSPACE_ADVANCED_TOOLS.includes(toolName as WorkspaceAdvancedToolName)) {
    return 'workspace_advanced';
  }
  if (WORKSPACE_BROWSER_TOOLS.includes(toolName as WorkspaceBrowserToolName)) {
    return 'workspace_browser';
  }
  if (HUMAN_TOOLS.includes(toolName as HumanToolName)) {
    return 'human_tools';
  }
  return null;
}
