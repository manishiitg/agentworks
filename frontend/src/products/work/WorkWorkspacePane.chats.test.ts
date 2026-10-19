import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('Crew chats management', () => {
  it('wires the Chats panel with rename, delete, and cleanup enabled', () => {
    const source = readFileSync('src/products/work/WorkWorkspacePane.tsx', 'utf8')
    const block = source.match(/view === 'history' && <PreviousChatHistoryPanel[\s\S]*?\/>\}/)
    expect(block).not.toBeNull()
    // readOnly would hide the row actions menu items and both Delete old
    // dropdowns. The active Crew session is filtered from the list, and the
    // backend still guards protected sessions via can_delete/can_resume.
    expect(block![0]).not.toMatch(/(^|\s)readOnly[\s/}]/)
    expect(block![0]).toContain('allowOpen')
    expect(block![0]).toContain('openOnRowClick')
  })
})
