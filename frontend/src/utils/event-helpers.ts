/**
 * Utility functions for event display formatting
 */

/**
 * Formats a JSON string with proper indentation
 * @param jsonString - The JSON string to format
 * @returns Formatted JSON string or the original string if not valid JSON
 */
export const formatJSON = (jsonString: string): string => {
  try {
    const parsed = JSON.parse(jsonString)
    return JSON.stringify(parsed, null, 2)
  } catch {
    // If it's not valid JSON, return the original string
    return jsonString
  }
}

/**
 * Checks if a string is valid JSON
 * @param str - The string to check
 * @returns True if the string is valid JSON
 */
export const isValidJSON = (str: string): boolean => {
  try {
    JSON.parse(str)
    return true
  } catch {
    return false
  }
}

/**
 * Extracts the logical tool name from an MCP-prefixed tool name.
 * MCP tools are named as mcp__<server-name>__<tool-name>.
 * Regular tools are returned as-is.
 * Example: mcp__api-bridge__get_api_spec → get_api_spec
 */
export const getLogicalToolName = (toolName: string): string => {
  const parts = toolName.split('__')
  if (parts.length >= 3 && parts[0] === 'mcp') {
    return parts.slice(2).join('__')
  }
  return toolName
}

/**
 * Extracts the MCP server name from an MCP-prefixed tool name.
 * Returns null for non-MCP tools.
 * Example: mcp__api-bridge__get_api_spec → api-bridge
 */
export const getMCPServerName = (toolName: string): string | null => {
  const parts = toolName.split('__')
  if (parts.length >= 3 && parts[0] === 'mcp') {
    return parts[1]
  }
  return null
}
