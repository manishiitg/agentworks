import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const read = (path: string) => readFileSync(path, 'utf8')

describe('Crew shared AgentWorks platform contract', () => {
  it('opens Crew history beside the permanent chat instead of replacing it', () => {
    const surface = read('src/products/work/WorkSurface.tsx')
    const pane = read('src/products/work/WorkWorkspacePane.tsx')
    const resume = read('src/hooks/useResumePreviousChat.ts')

    expect(surface).toContain('<WorkChatTabs')
    expect(surface).toContain("tab.metadata?.isViewOnly === true")
    expect(pane).toContain('allowOpen')
    expect(pane).toContain('activeSessionId={canonicalSessionId || activeSessionId}')
    expect(pane).toContain('onSelectSession={openHistoryChat}')
    expect(resume).toContain("targetTab.metadata.agentProfileId === 'work'")
    expect(resume).toContain('canonical?.sessionId === session.session_id')
    expect(resume).toContain('isViewOnly: true')
    expect(resume).not.toContain("if (targetTab.metadata.agentProfileId === 'work') {\n      const profileId")
  })

  it('offers permanent Crew deletion with the shared confirmation dialog', () => {
    const surface = read('src/products/work/WorkSurface.tsx')

    expect(surface).toContain('title={`Delete ${product.noun}`}')
    expect(surface).toContain('confirmText={`Delete ${product.noun}`}')
    expect(surface).toContain('onDelete={setDeleteCandidate}')
    expect(surface).toContain('await remove(deleteCandidate.id, deleteCandidate.product)')
  })
})
