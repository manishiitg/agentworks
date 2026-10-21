import type { PlannerFile } from '../services/api-types'

/**
 * Processes hierarchical files from API response
 * @param files - Array of files from API
 * @returns Processed files with ensured type and children properties
 */
export const processHierarchicalFiles = (files: PlannerFile[]): PlannerFile[] => {
  // API returns hierarchical structure directly, just ensure type is set correctly
  return files.map(file => ({
    ...file,
    type: file.type || 'file', // Ensure type is set
    children: file.children || [] // Ensure children array exists
  }))
}

/**
 * Recursively searches for a file in the file tree
 * @param fileList - Array of files to search through
 * @param targetPath - Path of the file to find (can be full path or relative filename)
 * @returns true if file exists, false otherwise
 */
export const findFileInTree = (fileList: PlannerFile[], targetPath: string): boolean => {
  // Normalize target path for comparison
  const normalizedTarget = targetPath.trim()
  const isRelativeFilename = !normalizedTarget.includes('/')
  
  for (const file of fileList) {
    // Check exact match first (both filepath and originalFilepath)
    if (file.filepath === normalizedTarget || 
        ('originalFilepath' in file && file.originalFilepath === normalizedTarget)) {
      return true
    }
    
    // If target is a relative filename (no slashes), check if any file ends with it
    // This handles cases like "step_9_clear_sheets_status.json" matching
    // "Workflow/ICICI BANK PARSING/step_9_clear_sheets_status.json"
    if (isRelativeFilename) {
      const filepathParts = file.filepath.split('/')
      const filename = filepathParts[filepathParts.length - 1]
      
      if (filename === normalizedTarget) {
        return true
      }
      
      // Also check originalFilepath if available
      if ('originalFilepath' in file && file.originalFilepath) {
        const originalParts = file.originalFilepath.split('/')
        const originalFilename = originalParts[originalParts.length - 1]
        if (originalFilename === normalizedTarget) {
          return true
        }
      }
    } else {
      // For full paths, also check if targetPath is a suffix of the file path
      // This handles cases where targetPath might be a relative path from workflow root
      if (file.filepath.endsWith('/' + normalizedTarget) || 
          file.filepath.endsWith(normalizedTarget)) {
        return true
      }
      
      if ('originalFilepath' in file && file.originalFilepath) {
        if (file.originalFilepath.endsWith('/' + normalizedTarget) || 
            file.originalFilepath.endsWith(normalizedTarget)) {
          return true
        }
      }
    }
    
    // Recurse into children
    if (file.children && file.children.length > 0) {
      if (findFileInTree(file.children, targetPath)) {
        return true
      }
    }
  }
  return false
}

/**
 * Extracts folder paths from a file path
 * @param filepath - Full file path (e.g., "Tasks/hello/task.md" or "Workflow/project/todo.md")
 * @returns Array of folder paths to expand
 */
export const extractFolderPaths = (filepath: string): string[] => {
  const pathParts = filepath.split('/')
  const foldersToExpand: string[] = []
  
  // Build folder paths progressively (exclude the file itself)
  for (let i = 0; i < pathParts.length - 1; i++) {
    const folderPath = pathParts.slice(0, i + 1).join('/')
    foldersToExpand.push(folderPath)
  }
  
  return foldersToExpand
}
