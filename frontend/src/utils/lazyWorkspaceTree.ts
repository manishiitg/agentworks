import type { PlannerFile } from '../services/api-types'
import { getAdjustedPath } from './workspacePathUtils'
const parentPath = (path: string) => path.includes('/') ? path.slice(0, path.lastIndexOf('/')) : ''

// Listings include a root and sometimes duplicate flat siblings. Normalize the
// fetched folder only; an absent child list on its descendants stays unknown.
export function folderListing(files: PlannerFile[], folder?: string): PlannerFile[] {
  if (!folder) return files
  const nodes = new Map<string, PlannerFile>()
  const collect = (items: PlannerFile[]) => items.forEach(item => {
    if (!nodes.has(item.filepath)) nodes.set(item.filepath, item)
    if (item.children) collect(item.children)
  })
  collect(files)
  const root = nodes.get(folder) ?? { filepath: folder, type: 'folder' as const }
  const children = [...nodes.values()].filter(item => item.filepath.slice(0, item.filepath.lastIndexOf('/')) === folder)
  return [{ ...root, children }]
}

// Retain already opened descendants when a parent is refreshed, but discard
// removed children and folders the server now confirms empty.
export function mergeFolderListing(tree: PlannerFile[], incoming: PlannerFile[]): PlannerFile[] {
  const old = new Map<string, PlannerFile>()
  const index = (items: PlannerFile[]) => items.forEach(item => { old.set(item.filepath, item); if (item.children) index(item.children) })
  index(tree)
  const retain = (item: PlannerFile): PlannerFile => ({
    ...item,
    children: item.has_children === false ? [] : item.children?.map(retain) ?? old.get(item.filepath)?.children,
  })
  return incoming.map(retain)
}

export function expandedListingPaths(files: PlannerFile[], scope: string | undefined, expanded: Set<string>): string[] {
  const paths: string[] = []
  const collect = (items: PlannerFile[]) => items.forEach(item => {
    const shown = scope ? getAdjustedPath(item.filepath, scope) : item.filepath
    if (item.type === 'folder' && expanded.has(shown)) {
      paths.push(item.filepath)
      if (item.children) collect(item.children)
    }
  })
  collect(files)
  return paths.sort((a, b) => a.split('/').length - b.split('/').length)
}

export function fileSearchTree(results: { filepath: string; type?: string }[], scope?: string, displayName?: string): PlannerFile[] {
  const nodes = new Map<string, PlannerFile>()
  for (const result of results) {
    nodes.set(result.filepath, { filepath: result.filepath, type: result.type === 'folder' ? 'folder' : 'file' })
    let parent = parentPath(result.filepath)
    while (parent && (!scope || parent === scope || parent.startsWith(scope + '/'))) {
      if (!nodes.has(parent)) nodes.set(parent, { filepath: parent, type: 'folder' })
      if (!parent.includes('/')) break
      parent = parentPath(parent)
    }
  }
  const roots: PlannerFile[] = []
  for (const node of nodes.values()) {
    if (scope && node.filepath === scope) node.display_name = displayName
    const parent = nodes.get(parentPath(node.filepath))
    if (parent) (parent.children ??= []).push(node)
    else roots.push(node)
  }
  return roots
}
