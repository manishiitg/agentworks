import { describe, expect, it } from 'vitest'
import type { PlannerFile } from '../services/api-types'
import { scopeFilesToWorkspace } from './scopedWorkspaceTree'
import { flattenVisiblePlannerFiles } from './plannerFileTree'
import { isProtectedWorkspaceEntry } from './workspaceSelection'

const path = 'Chats/Code/projects/unknown2-0-8079bd2f'
const rootName = 'unknown2-0-8079bd2f'
const code: PlannerFile = { filepath: `${path}/code`, type: 'folder', children: [{ filepath: `${path}/code/app.ts`, type: 'file' }] }
const db: PlannerFile = { filepath: `${path}/db`, type: 'folder' }
const listing: PlannerFile[] = [code, db, { filepath: path, type: 'folder', children: [code] }, { filepath: `${path}/notes.md`, type: 'file' }]

describe('scoped workspace hierarchy', () => {
  it.each([path, `/${path}///`, `_users/alice/${path}`])('puts the root above its children for scope %s', scope => {
    const files = scopeFilesToWorkspace(listing, scope, [], 'alice')
    expect(files).toHaveLength(1)
    expect(files[0].filepath).toBe(rootName)
    expect(flattenVisiblePlannerFiles(files, new Set([rootName, 'code']), false).map(row => [row.file.filepath, row.depth])).toEqual([
      [rootName, 0], ['code', 1], ['code/app.ts', 2], ['db', 1], ['notes.md', 1],
    ])
    expect(files[0].children?.find(file => file.filepath === 'code')?.children?.[0].originalFilepath).toBe(`${path}/code/app.ts`)
    expect(isProtectedWorkspaceEntry(files[0], path)).toBe(true)
    expect(listing[2].children).toEqual([code])
  })

  it('groups a contents-only listing under its scoped root', () => {
    const [root] = scopeFilesToWorkspace([code, db], path)
    expect(root.filepath).toBe(rootName)
    expect(root.children?.map(file => file.filepath)).toEqual(['code', 'db'])
  })

  it('converts only own physical listing paths to public API paths', () => {
    const files = scopeFilesToWorkspace([
      { filepath: `_users/alice/${path}`, type: 'folder' },
      { filepath: `_users/alice/${path}/code`, type: 'folder' },
      { filepath: `_users/alice/${path}/product.json`, type: 'file' },
      { filepath: `_users/bob/${path}/secret.txt`, type: 'file' },
    ], path, [], 'alice')
    expect(files[0].originalFilepath).toBe(path)
    expect(files[0].children?.map(file => file.originalFilepath)).toEqual([`${path}/code`, `${path}/product.json`])
    expect(isProtectedWorkspaceEntry(files[0].children![1], path)).toBe(true)
  })

  it('keeps empty roots and returns no tree for an empty response', () => {
    expect(scopeFilesToWorkspace([{ filepath: path, type: 'folder' }], path)[0].children).toEqual([])
    expect(scopeFilesToWorkspace([], path)).toEqual([])
  })

  it('hides root entries and excludes neighboring and foreign user paths', () => {
    const [root] = scopeFilesToWorkspace([
      ...listing,
      { filepath: `${path}/builder`, type: 'folder', children: [{ filepath: `${path}/builder/private.txt`, type: 'file' }] },
      { filepath: `${path}/code/builder`, type: 'folder' },
      { filepath: `${path}-other/code`, type: 'folder' },
      { filepath: `_users/bob/${path}/private.txt`, type: 'file' },
    ], path, ['builder'], 'alice')
    expect(root.children?.map(file => file.filepath)).toEqual(['code', 'db', 'notes.md'])
    expect(root.children?.[0].children?.map(file => file.filepath)).toEqual(['code/app.ts', 'code/builder'])
  })

  it('preserves case and keeps API paths when passed an already adjusted tree', () => {
    expect(scopeFilesToWorkspace(listing, path.toLowerCase())).toEqual([])
    const first = scopeFilesToWorkspace(listing, path)
    expect(scopeFilesToWorkspace(first, path)).toEqual(first)
  })
})
