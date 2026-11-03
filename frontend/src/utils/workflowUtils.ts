import type { VariablesManifest } from '../services/api-types';

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

/** Run context follows its recorded identity; saved input names are only labels. */
export function resolveGroupFolderPath(options: {
  currentRunningGroupId?: string | null
  selectedRunFolder?: string | null
  selectedGroupIds?: string[]
  manifest?: VariablesManifest | null
}): string | undefined {
  return options.selectedRunFolder && options.selectedRunFolder !== 'new' ? options.selectedRunFolder : undefined
}
