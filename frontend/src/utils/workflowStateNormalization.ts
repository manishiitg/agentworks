/**
 * Workflow State Normalization Utilities
 *
 * Single source of truth for converting between different representations:
 * - UI Display: folder names, group names
 * - Canonical State: group names, step numbers
 * - API Format: group names, step numbers, folder paths
 *
 * All normalization happens here to ensure consistency.
 */

import type { VariablesManifest } from '../services/api-types'

/**
 * Sanitizes a name for folder matching
 */
function sanitizeForMatch(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9-]/g, '-').replace(/-+/g, '-').trim()
}

/**
 * Normalizes group names from any format to canonical names
 *
 * Input can be:
 * - exact group names (e.g., "Production") -- already canonical
 * - folder names (e.g., "production") -- needs lookup by sanitized match
 *
 * @param input - Array of group names or folder names
 * @param manifest - Variables manifest to resolve folder names to group names
 * @returns Array of valid group names from manifest (filtered and normalized)
 */
export function normalizeGroupNames(
  input: string[],
  manifest: VariablesManifest | null
): string[] {
  if (!input || input.length === 0) {
    return []
  }

  if (!manifest?.groups || manifest.groups.length === 0) {
    console.warn('[normalizeGroupNames] No manifest available, cannot normalize group names')
    return []
  }

  const normalized: string[] = []
  const seen = new Set<string>()

  for (const item of input) {
    if (seen.has(item)) continue

    let matchedName: string | null = null

    // Strategy 1: Check if it's already an exact group name
    const exactMatch = manifest.groups.find(g => g.name === item)
    if (exactMatch) {
      matchedName = exactMatch.name
    } else {
      // Strategy 2: Try to match by sanitized name (might be a folder name)
      const sanitizedMatch = manifest.groups.find(g => {
        const sanitizedName = sanitizeForMatch(g.name)
        const itemSanitized = sanitizeForMatch(item)
        return sanitizedName === itemSanitized
      })
      if (sanitizedMatch) {
        matchedName = sanitizedMatch.name
      }
    }

    if (matchedName) {
      normalized.push(matchedName)
      seen.add(matchedName)
    } else {
      console.warn('[normalizeGroupNames] Could not normalize group name:', item)
    }
  }

  return normalized
}

/**
 * Normalizes run folder selection
 * 
 * Ensures the folder path is valid and extracts iteration/group info
 * 
 * @param folderPath - Folder path (e.g., "iteration-4", "iteration-4/manishiithuf", "new")
 * @param manifest - Variables manifest for validation
 * @returns Normalized folder path or "new"
 */
export function normalizeRunFolder(
  folderPath: string | null | undefined,
  manifest: VariablesManifest | null
): string | null {
  if (!folderPath) {
    return null
  }

  // Validate format: should be "iteration-X" or "iteration-X/group-name"
  if (!folderPath.startsWith('iteration-')) {
    console.warn('[normalizeRunFolder] Invalid folder path format:', folderPath)
    return null
  }

  // If it's a group folder, validate the group exists in manifest
  if (folderPath.includes('/')) {
    const parts = folderPath.split('/')
    if (parts.length === 2) {
      const [, groupFolderName] = parts
      
      // Check if group folder name matches any group in manifest
      if (manifest?.groups) {
        const groupExists = manifest.groups.some(g => {
          // Match by exact name
          if (g.name === groupFolderName) return true

          // Match by sanitized name
          const sanitizedName = sanitizeForMatch(g.name)
          const folderNameSanitized = sanitizeForMatch(groupFolderName)
          return sanitizedName === folderNameSanitized
        })
        
        if (!groupExists) {
          console.warn('[normalizeRunFolder] Group folder not found in manifest:', groupFolderName)
          // Return just the iteration folder
          return parts[0]
        }
      }
    }
  }

  return folderPath
}
