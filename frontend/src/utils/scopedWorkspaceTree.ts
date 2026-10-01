import type { PlannerFile } from '../services/api-types'
import { adjustFilePathsRecursive, publicWorkspacePathForUser } from './workspacePathUtils'

// The document listing can include the root, nested children and duplicate flat
// siblings. Build one tree using API paths before shortening paths for display.
export function scopeFilesToWorkspace(
  files: PlannerFile[],
  workspacePath: string,
  hiddenRootFolders: readonly string[] = [],
  userId?: string,
): PlannerFile[] {
  const canonicalPath = (path: string) => publicWorkspacePathForUser(path, userId)
  const target = canonicalPath(workspacePath)
  if (!target) return []
  const prefix = `${target}/`
  const hidden = new Set(hiddenRootFolders)
  const nodes = new Map<string, PlannerFile>()
  const collect = (items: PlannerFile[]) => {
    for (const item of items) {
      const path = canonicalPath(item.originalFilepath || item.filepath)
      const relative = path.startsWith(prefix) ? path.slice(prefix.length) : ''
      if ((path === target || path.startsWith(prefix)) && !hidden.has(relative.split('/')[0])) {
        if (!nodes.has(path)) nodes.set(path, { ...item, filepath: path, children: item.type === 'folder' ? [] : undefined })
        if (item.children) collect(item.children)
      }
    }
  }
  collect(files)
  if (!nodes.size) return []

  const root = nodes.get(target) || { filepath: target, type: 'folder', children: [] }
  nodes.set(target, root)
  for (const [path, node] of nodes) {
    if (path === target) continue
    const parent = nodes.get(path.slice(0, path.lastIndexOf('/')))
    if (parent?.type === 'folder') parent.children!.push(node)
  }
  return adjustFilePathsRecursive([root], target)
}
