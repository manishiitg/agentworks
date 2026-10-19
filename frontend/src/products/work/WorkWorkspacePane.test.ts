import { readFileSync } from 'node:fs'
import { expect, it } from 'vitest'

it('keeps saved history and the existing skill manager reachable in Ops', () => {
  const pane = readFileSync('src/products/work/WorkWorkspacePane.tsx', 'utf8')
  expect(pane).toContain("view === 'history' && <PreviousChatHistoryPanel")
  expect(pane).toContain('skillsManager={<WorkSkillsPanel')
  expect(pane).not.toContain('chatContent=')
  const skills = readFileSync('src/products/work/WorkSkillsPanel.tsx', 'utf8')
  expect(skills).toContain('onBeforeUninstall={() => pendingSkillSaves.current}')
  expect(skills).toContain('agentProfileRuntimeDirty: true')
})
