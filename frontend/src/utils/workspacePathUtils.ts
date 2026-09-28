import type { PlannerFile } from '../services/api-types'

/** Shared Files policy: the workspace root opens, its child folders do not. */
export const EXPAND_FIRST_LEVEL_FOLDERS_BY_DEFAULT = false

/**
 * Platform-owned project/workflow entries that are useful for diagnostics but
 * add noise to the normal Files view. This is UI-only: the entries remain on
 * disk and become visible through the shared "Show hidden files" control.
 */
export const MANAGED_WORKSPACE_ROOT_ENTRIES = new Set([
  '.agents',
  '.claude',
  '.codex',
  '.cursor',
  '.gemini',
  '.git',
  '.local',
  '.pi',
  '.sandbox-cache',
  '.tmp',
  'AGENTS.md',
  'CLAUDE.md',
  'GEMINI.md',
  'Workflow',
  '_users',
  'agents',
  'builder',
  'code-migration-backup',
  'config',
  'costs',
  'migration-backups',
  'node_modules',
  'planning',
  'product.json',
  'pulse',
  'skills',
  'soul',
  'subagents',
  'tool_output_folder',
  'variables',
  'versions',
  'workflow.json',
  'workflow.json.backup',
])

const workspaceEntryName = (file: PlannerFile): string => {
  const normalized = file.filepath.replace(/\/+$/, '')
  return normalized.split('/').pop() || normalized
}

export function hideManagedWorkspaceRootEntries(
  files: PlannerFile[],
  additionalHiddenEntries: readonly string[] = [],
): PlannerFile[] {
  const hidden = new Set([...MANAGED_WORKSPACE_ROOT_ENTRIES, ...additionalHiddenEntries])
  const visibleEntry = (file: PlannerFile): PlannerFile | null => {
    const name = workspaceEntryName(file)
    // Custom skills are user-authored project output. Keep that branch visible
    // while concealing built-in/projected skill folders alongside other
    // platform-managed files.
    if (name === 'skills' && file.type === 'folder') {
      const custom = (file.children || []).find(child => workspaceEntryName(child) === 'custom')
      if (custom) return { ...file, children: [custom] }
    }
    return !name.startsWith('.') && !hidden.has(name) ? file : null
  }
  const filterEntries = (entries: PlannerFile[]) => entries.flatMap(file => {
    const visible = visibleEntry(file)
    return visible ? [visible] : []
  })

  // Scoped project/workflow trees are represented by one visible root. Keep
  // that root and filter only its direct children so similarly named folders
  // inside user code are never hidden.
  if (files.length === 1 && files[0].type === 'folder') {
    return [{
      ...files[0],
      children: filterEntries(files[0].children || []),
    }]
  }
  return filterEntries(files)
}

export function getInitialExpandedWorkspaceFolders(rootFolder?: PlannerFile | null): Set<string> {
  return new Set(rootFolder?.type === 'folder' ? [rootFolder.filepath] : [])
}

/**
 * Utility functions for handling workspace paths in different modes (chat vs workflow)
 * Handles path normalization, adjustment, and mapping between original and adjusted paths
 */

/**
 * Strip trailing slashes only -- for equality checks between workspace paths
 * as stored (case preserved, leading slash preserved). Not the same as
 * normalizePathForComparison below.
 */
export function normalizeWorkspacePath(path?: string | null): string {
  return (path || '').replace(/\/+$/, '')
}

/**
 * Convert the authenticated user's physical workspace path to the public path
 * used by browser APIs. A foreign _users path is deliberately left intact.
 */
export function publicWorkspacePathForUser(path: string | null | undefined, userId: string | null | undefined): string {
  const normalized = normalizeWorkspacePath(path).replace(/^\/+/, '').replace(/\\/g, '/')
  if (!normalized || !userId || !/^[a-zA-Z0-9_-]{1,128}$/.test(userId)) return normalized
  const prefix = `_users/${userId}/`
  return normalized.startsWith(prefix) ? normalized.slice(prefix.length) : normalized
}

export function workspacePathsEqualForUser(
  left: string | null | undefined,
  right: string | null | undefined,
  userId: string | null | undefined,
): boolean {
  return publicWorkspacePathForUser(left, userId) === publicWorkspacePathForUser(right, userId)
}

/**
 * Normalize a path for comparison (lowercase, remove leading/trailing slashes)
 */
export function normalizePathForComparison(path: string): string {
  return path.toLowerCase().replace(/^\/+|\/+$/g, '')
}

/**
 * Check if a file path is within a folder path
 */
export function isPathWithinFolder(filepath: string, folderPath: string): boolean {
  const normalizedFile = normalizePathForComparison(filepath)
  const normalizedFolder = normalizePathForComparison(folderPath)
  
  return normalizedFile === normalizedFolder || 
         normalizedFile.startsWith(normalizedFolder + '/')
}

/**
 * Get adjusted path (workflow folder prefix removed) for display in workflow mode
 */
export function getAdjustedPath(filepath: string, workflowFolderPath: string): string {
  const filePathNormalized = normalizePathForComparison(filepath)
  const workflowPathNormalized = normalizePathForComparison(workflowFolderPath)
  
  // If this is the workflow folder itself, return just the folder name
  if (filePathNormalized === workflowPathNormalized) {
    const folderParts = workflowFolderPath.split('/').filter(Boolean)
    return folderParts.length > 0 ? folderParts[folderParts.length - 1] : filepath
  }
  
  // If this is within the workflow folder, remove the prefix
  if (filePathNormalized.startsWith(workflowPathNormalized + '/')) {
    const remaining = filepath.slice(workflowFolderPath.length)
    const adjustedPath = remaining.startsWith('/') ? remaining.slice(1) : remaining
    
    if (!adjustedPath) {
      const folderParts = workflowFolderPath.split('/').filter(Boolean)
      return folderParts.length > 0 ? folderParts[folderParts.length - 1] : filepath
    }
    
    return adjustedPath
  }
  
  // Not within workflow folder, return as-is
  return filepath
}

/**
 * Get original path from adjusted path (add workflow folder prefix back)
 */
export function getOriginalPath(adjustedPath: string, workflowFolderPath: string): string {
  const normalizedAdjusted = normalizePathForComparison(adjustedPath)
  const normalizedWorkflow = normalizePathForComparison(workflowFolderPath)
  
  // If it already starts with the workflow path, it's already original
  if (normalizedAdjusted.startsWith(normalizedWorkflow)) {
    return adjustedPath
  }
  
  // Add workflow prefix
  if (adjustedPath.startsWith('/')) {
    return `${workflowFolderPath}${adjustedPath}`
  } else {
    return `${workflowFolderPath}/${adjustedPath}`
  }
}

/** Resolve the upload dialog's displayed root to the active project or workflow. */
export function resolveWorkspaceUploadPath(folderPath: string, scope?: string | null): string {
  if (!folderPath || folderPath === '/') return scope || '/'
  if (!scope || isPathWithinFolder(folderPath, scope)) return folderPath
  return `${scope.replace(/\/+$/, '')}/${folderPath.replace(/^\/+/, '')}`
}

/**
 * Check if a file should be included in workflow filter
 */
export function shouldIncludeInWorkflowFilter(
  filepath: string, 
  workflowFolderPath: string
): boolean {
  const normalizedPath = normalizePathForComparison(filepath)
  const normalizedWorkflow = normalizePathForComparison(workflowFolderPath)
  
  // Check if within workflow folder
  if (normalizedPath === normalizedWorkflow || normalizedPath.startsWith(normalizedWorkflow + '/')) {
    return true
  }
  
  return false
}

/**
 * Collect all folder paths from a file tree (including both adjusted and original paths)
 */
export function collectFolderPaths(files: PlannerFile[], includeOriginal: boolean = true): Set<string> {
  const paths = new Set<string>()
  
  const collect = (fileList: PlannerFile[]) => {
    fileList.forEach(file => {
      if (file.type === 'folder') {
        // Add adjusted path
        paths.add(file.filepath)
        
        // Add original path if available
        if (includeOriginal && 'originalFilepath' in file && file.originalFilepath) {
          paths.add(file.originalFilepath)
        }
        
        // Recurse into children
        if (file.children) {
          collect(file.children)
        }
      }
    })
  }
  
  collect(files)
  return paths
}

/**
 * Find a file in the tree by checking both filepath and originalFilepath
 */
export function findFileByPath(files: PlannerFile[], targetPath: string): PlannerFile | null {
  for (const file of files) {
    if (file.filepath === targetPath || ('originalFilepath' in file && file.originalFilepath === targetPath)) {
      return file
    }
    
    if (file.children && file.children.length > 0) {
      const found = findFileByPath(file.children, targetPath)
      if (found) return found
    }
  }
  
  return null
}

/**
 * Restore expanded folders by matching against available folder paths
 * Handles both adjusted and original path formats
 */
export function restoreExpandedFolders(
  previouslyExpanded: Set<string>,
  availableFolderPaths: Set<string>
): Set<string> {
  const restored = new Set<string>()
  
  previouslyExpanded.forEach(folderPath => {
    // Check if this path exists in available paths
    if (availableFolderPaths.has(folderPath)) {
      restored.add(folderPath)
    }
  })
  
  return restored
}

/**
 * Extract folder paths that need to be expanded to show a file
 */
export function extractFolderPathsForFile(filepath: string): string[] {
  const parts = filepath.split('/').filter(Boolean)
  const folders: string[] = []
  
  for (let i = 0; i < parts.length - 1; i++) {
    folders.push(parts.slice(0, i + 1).join('/'))
  }
  
  return folders
}

/**
 * Normalize a path for comparison (lowercase, remove leading/trailing slashes)
 * Alias for normalizePathForComparison for backward compatibility
 */
export const normalizePath = normalizePathForComparison

/**
 * Find a folder in the file tree by path (checks both filepath and originalFilepath)
 */
export function findFolderInTree(fileList: PlannerFile[], targetPath: string): PlannerFile | null {
  for (const file of fileList) {
    if (file.type === 'folder' && (file.filepath === targetPath || ('originalFilepath' in file && file.originalFilepath === targetPath))) {
      return file
    }
    if (file.children && file.children.length > 0) {
      const found = findFolderInTree(file.children, targetPath)
      if (found) return found
    }
  }
  return null
}

/**
 * Recursively find all iteration folders in the tree
 * Matches pattern: runs/iteration-* or iteration-* (with optional group subfolders)
 */
export function findIterationFolders(fileList: PlannerFile[]): string[] {
  const iterationFolders: string[] = []
  
  for (const file of fileList) {
    if (file.type === 'folder') {
      // Check if this is an iteration folder (matches pattern: runs/iteration-* or iteration-*)
      // Also handle group subfolders: runs/iteration-*/group-* or runs/iteration-*/display-name
      // Accepts both "group-X" format and display names (any alphanumeric/dash folder name)
      if (file.filepath.match(/^runs\/iteration-\d+(?:-(?:hook|sched|slack-[a-f0-9]+))?(\/[a-zA-Z0-9_-]+)?$/) || file.filepath.match(/^iteration-\d+(?:-(?:hook|sched|slack-[a-f0-9]+))?(\/[a-zA-Z0-9_-]+)?$/)) {
        iterationFolders.push(file.filepath)
      }
      
      // Recursively search children
      if (file.children && file.children.length > 0) {
        const childIterations = findIterationFolders(file.children)
        iterationFolders.push(...childIterations)
      }
    }
  }
  
  return iterationFolders
}

/**
 * Check if a path matches an iteration folder pattern
 */
export function isIterationFolder(path: string): boolean {
  return !!(
    path.match(/^runs\/iteration-\d+(?:-(?:hook|sched|slack-[a-f0-9]+))?(\/[a-zA-Z0-9_-]+)?$/) ||
    path.match(/^iteration-\d+(?:-(?:hook|sched|slack-[a-f0-9]+))?(\/[a-zA-Z0-9_-]+)?$/)
  )
}

/**
 * Adjust file paths recursively to show workflow folder as root
 * Stores original path in originalFilepath for API calls
 */
export function adjustFilePathsRecursive(
  fileList: PlannerFile[],
  workflowFolderPath: string
): PlannerFile[] {
  return fileList.map(file => {
    const adjustedFilepath = getAdjustedPath(file.filepath, workflowFolderPath)
    const originalFilepath = file.filepath // Store original before adjustment
    
    if (file.type === 'folder') {
      return {
        ...file,
        filepath: adjustedFilepath,
        originalFilepath: originalFilepath,
        children: file.children ? adjustFilePathsRecursive(file.children, workflowFolderPath) : []
      }
    }
    
    return {
      ...file,
      filepath: adjustedFilepath,
      originalFilepath: originalFilepath
    }
  })
}
