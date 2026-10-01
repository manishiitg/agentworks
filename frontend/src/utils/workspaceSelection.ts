import type { PlannerFile } from '../services/api-types'

function originalPath(file: PlannerFile): string {
  return (file.originalFilepath || file.filepath).replace(/^\/+|\/+$/g, '')
}

// A scoped Files pane owns the contents, while project deletion owns its root
// and identity manifests. Display paths may have been shortened for the tree.
export function isProtectedWorkspaceEntry(file: PlannerFile, protectedRootPath?: string): boolean {
  if (!protectedRootPath) return false
  const root = protectedRootPath.replace(/^\/+|\/+$/g, '')
  const path = originalPath(file)
  return path === root || path === `${root}/product.json` || path === `${root}/workflow.json`
}

export function workspaceSelectionItems(files: PlannerFile[], protectedRootPath?: string): PlannerFile[] {
  const root = protectedRootPath?.replace(/^\/+|\/+$/g, '')
  return files.flatMap(file => root && originalPath(file) === root ? file.children || [] : [file])
    .filter(file => !isProtectedWorkspaceEntry(file, protectedRootPath))
}
