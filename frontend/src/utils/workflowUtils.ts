import type { VariableGroup, VariablesManifest } from '../services/api-types'

/**
 * Utility functions for workflow-related operations
 * Consolidates logic for handling group folders, iteration paths, and display name sanitization
 */

/**
 * Sanitizes a display name for use in folder paths.
 * Converts to lowercase, replaces special characters with dashes, normalizes multiple dashes.
 *
 * This ensures consistent folder naming across frontend and backend.
 *
 * @param displayName - The display name to sanitize
 * @returns Sanitized display name, or empty string if invalid
 *
 * @example
 * sanitizeDisplayNameForFolder("Training #1") // Returns: "real-training-1"
 * sanitizeDisplayNameForFolder("Production--Env") // Returns: "production-env"
 */
export function sanitizeDisplayNameForFolder(displayName: string | undefined): string {
  if (!displayName) return ''

  return displayName
    .toLowerCase()
    .replace(/[^a-z0-9-]/g, '-')
    .replace(/-+/g, '-')
    .trim()
    .replace(/^-+|-+$/g, '')
}

/**
 * Extracts the iteration folder from a run folder path
 * Handles both formats: "iteration-X" and "iteration-X/group-Y"
 * 
 * @param runFolder - The run folder path (e.g., "iteration-1" or "iteration-1/group-5")
 * @returns The iteration folder name (e.g., "iteration-1"), or null if not found
 */
export function extractIterationFolder(runFolder: string | null | undefined): string | null {
  if (!runFolder || runFolder === 'new') return null
  
  if (runFolder.includes('/')) {
    return runFolder.split('/')[0] // e.g., "iteration-1" from "iteration-1/group-5"
  }
  
  if (runFolder.startsWith('iteration-')) {
    return runFolder
  }
  
  return null
}

/**
 * Extracts the group name from a run folder path
 * Matches the folder name against sanitized group names from the manifest.
 *
 * @param runFolder - The run folder path (e.g., "iteration-1/production")
 * @param manifest - Optional variables manifest to resolve folder names to group names
 * @returns The group name, or null if not found
 */
export function extractGroupNameFromFolder(
  runFolder: string | null | undefined,
  manifest?: VariablesManifest | null
): string | null {
  if (!runFolder || runFolder === 'new' || !runFolder.includes('/')) {
    return null
  }

  const parts = runFolder.split('/')
  if (parts.length !== 2) return null

  const groupFolderName = parts[1] // e.g., "production"

  // Try to resolve it to a group name using manifest
  if (manifest?.groups) {
    const sanitizeForMatch = (name: string) =>
      name.toLowerCase().replace(/[^a-z0-9-]/g, '-').replace(/-+/g, '-').trim()

    const group = manifest.groups.find((g: VariableGroup) => {
      const sanitizedName = sanitizeForMatch(g.name)
      const folderNameSanitized = sanitizeForMatch(groupFolderName)

      return sanitizedName === folderNameSanitized || g.name === groupFolderName
    })

    return group?.name || null
  }

  // No manifest provided, return the folder name as-is
  return groupFolderName
}

/**
 * Builds a group folder path from a group ID
 * Uses the sanitized group name as the folder name
 * 
 * @param groupName - The group name (e.g., "Production")
 * @param iterationFolder - The iteration folder (e.g., "iteration-1")
 * @param manifest - Variables manifest to get group details
 * @returns The full group folder path (e.g., "iteration-1/production"), or null if group not found
 */
export function buildGroupFolderPath(
  groupName: string,
  iterationFolder: string | null | undefined,
  manifest?: VariablesManifest | null
): string | null {
  if (!groupName || !iterationFolder) return null

  // Find the group in manifest
  const group = manifest?.groups?.find(g => g.name === groupName)
  if (!group) {
    // Group not found in manifest, use sanitized groupName as folder name
    return `${iterationFolder}/${sanitizeDisplayNameForFolder(groupName) || groupName}`
  }

  // Folder name is the sanitized group name
  const folderName = sanitizeDisplayNameForFolder(group.name) || group.name

  return `${iterationFolder}/${folderName}`
}

/**
 * Determines the specific group folder path to use for phases that need context.
 *
 * This function resolves which group folder path should be used based on a simplified priority system:
 * - Priority 1: Currently running group (during batch execution) - provides execution feedback
 * - Priority 2: First selected group from checkboxes - user's explicit selection
 * - Fallback: Iteration folder only (no specific group)
 *
 * @param options - Configuration options
 * @param options.currentRunningGroupId - Currently running group name (during batch execution)
 * @param options.selectedRunFolder - Currently selected run folder (e.g., "iteration-1")
 * @param options.selectedGroupIds - Array of selected group names from checkboxes (store field still called selectedGroupIds)
 * @param options.manifest - Variables manifest containing group definitions
 * @returns The resolved group folder path (e.g., "iteration-1/production"), iteration folder, or undefined
 *
 * @example
 * // During batch execution
 * resolveGroupFolderPath({ currentRunningGroupId: 'Production', selectedRunFolder: 'iteration-1', ... })
 * // Returns: "iteration-1/production"
 *
 * @example
 * // User selected groups via checkboxes
 * resolveGroupFolderPath({ selectedGroupIds: ['Production', 'Staging'], selectedRunFolder: 'iteration-5', ... })
 * // Returns: "iteration-5/production" (uses first selected)
 *
 * @example
 * // No groups selected - execute all enabled
 * resolveGroupFolderPath({ selectedGroupIds: [], selectedRunFolder: 'iteration-1', ... })
 * // Returns: "iteration-1"
 */
export function resolveGroupFolderPath(options: {
  currentRunningGroupId?: string | null
  selectedRunFolder?: string | null
  selectedGroupIds?: string[]
  manifest?: VariablesManifest | null
}): string | undefined {
  const {
    currentRunningGroupId,
    selectedRunFolder,
    selectedGroupIds = [],
    manifest
  } = options

  // If no manifest or groups, return selectedRunFolder as-is (if valid)
  // This is normal - don't log errors for this case
  if (!manifest?.groups || manifest.groups.length === 0) {
    // Only return if selectedRunFolder is a valid string (not null/undefined/empty)
    return (selectedRunFolder && selectedRunFolder !== 'new') ? selectedRunFolder : undefined
  }

  // Try to extract iteration folder from selectedRunFolder
  const iterationFolder = extractIterationFolder(selectedRunFolder)
  
  // If we couldn't extract iteration from selectedRunFolder, try to find it from selectedGroupIds
  // This handles the case where page refreshes and selectedRunFolder is invalid but we have selectedGroupIds
  if (!iterationFolder && selectedGroupIds.length > 0 && manifest.groups) {
    // Try to find a group that matches one of the selectedGroupIds and extract its iteration
    // We can't directly get iteration from groupId, but we can check if any run folders exist
    // For now, if we have selectedGroupIds but no valid selectedRunFolder, return undefined
    // The caller should handle this case
    // This is normal during page refresh - don't log errors
    return undefined
  }
  
  // If still no iteration folder, return selectedRunFolder as-is (if valid) or undefined
  // This is normal when data isn't ready yet (e.g., during initial load or page refresh)
  // Only log if we have all the data but still can't extract (which would be unusual)
  if (!iterationFolder) {
    // Only log if we have a selectedRunFolder but it's in an unexpected format
    // This helps debug actual issues without spamming logs during normal operation
    if (selectedRunFolder && selectedRunFolder !== 'new' && !selectedRunFolder.startsWith('iteration-')) {
      // This is unusual - selectedRunFolder exists but doesn't match expected format
      // But don't log repeatedly - this might be called many times during renders
      // The caller can handle undefined gracefully
    }
    return (selectedRunFolder && selectedRunFolder !== 'new') ? selectedRunFolder : undefined
  }

  // Priority 1: Use currently running group (during batch execution)
  // This provides real-time feedback on which group is executing
  if (currentRunningGroupId) {
    const groupPath = buildGroupFolderPath(currentRunningGroupId, iterationFolder, manifest)
    return groupPath || iterationFolder
  }

  // Priority 2: Use first selected group from checkboxes
  // This represents the user's explicit selection for context
  if (selectedGroupIds.length > 0) {
    const groupPath = buildGroupFolderPath(selectedGroupIds[0], iterationFolder, manifest)
    if (selectedGroupIds.length > 1) {
      console.warn('[workflowUtils] Multiple groups selected, using first group for context:', selectedGroupIds[0])
    }
    return groupPath || iterationFolder
  }

  // No specific group selected - return iteration folder only
  // Backend will execute all enabled groups
  return iterationFolder
}
