import { describe, expect, it } from 'vitest'
import { isSafeReportWorkspacePath } from './reportWorkspacePath'

describe('isSafeReportWorkspacePath', () => {
  it('accepts a workflow, a Crew in its owner tree, a Crew at the shared root and a Code', () => {
    expect(isSafeReportWorkspacePath('Workflow/trading')).toBe(true)
    expect(isSafeReportWorkspacePath('Chats/Work/projects/sde-1a2b3c4d')).toBe(true)
    expect(isSafeReportWorkspacePath('Crew/sde-1a2b3c4d')).toBe(true)
    expect(isSafeReportWorkspacePath('Chats/Code/projects/app-1')).toBe(true)
  })

  it('refuses the bare shared root, hidden entries, traversal and other roots', () => {
    expect(isSafeReportWorkspacePath('Crew')).toBe(false)
    expect(isSafeReportWorkspacePath('Crew/')).toBe(false)
    expect(isSafeReportWorkspacePath('Crew/.migrating/x')).toBe(false)
    expect(isSafeReportWorkspacePath('Crew/../Workflow/x')).toBe(false)
    expect(isSafeReportWorkspacePath('/Crew/x')).toBe(false)
    expect(isSafeReportWorkspacePath('Downloads/x')).toBe(false)
  })
})
