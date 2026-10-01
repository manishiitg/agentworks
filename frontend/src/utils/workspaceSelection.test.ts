import { describe, expect, it } from 'vitest'
import type { PlannerFile } from '../services/api-types'
import { isProtectedWorkspaceEntry, workspaceSelectionItems } from './workspaceSelection'

const root = 'Chats/Code/projects/test-1'
const file = (name: string): PlannerFile => ({ filepath: `test-1/${name}`, originalFilepath: `${root}/${name}`, type: 'file' })
const contents = [file('app.ts'), file('product.json'), file('workflow.json'), { ...file('code'), type: 'folder' as const, children: [file('code/index.ts')] }]
const tree: PlannerFile[] = [{ filepath: 'test-1', originalFilepath: root, type: 'folder', children: contents }]

describe('scoped workspace selection', () => {
  it('selects project contents instead of recursively deleting the entire project', () => {
    expect(workspaceSelectionItems(tree, root).map(item => item.originalFilepath))
      .toEqual([`${root}/app.ts`, `${root}/code`])
  })
  it('works with a flat listing and shortened display paths', () => {
    expect(workspaceSelectionItems(contents, root)).toEqual([contents[0], contents[3]])
    expect(isProtectedWorkspaceEntry(tree[0], `/${root}/`)).toBe(true)
    expect(isProtectedWorkspaceEntry(contents[1], root)).toBe(true)
  })
  it('keeps ordinary folders selectable outside protected scoped views', () => {
    expect(workspaceSelectionItems(tree)).toEqual(tree)
    expect(isProtectedWorkspaceEntry(tree[0])).toBe(false)
  })
  it('allows a nested file with the same name as project metadata', () => {
    expect(isProtectedWorkspaceEntry(file('code/product.json'), root)).toBe(false)
  })
  it('does not select an empty project root', () => {
    expect(workspaceSelectionItems([{ ...tree[0], children: [] }], root)).toEqual([])
  })
})
