import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'
import { normalizeWorkspaceViewId, WORKSPACE_VIEWS } from './workspaceViews'
import { UI_CONTROL_CONTRACT } from '../../platform/ui-control/contract.generated'

// The agent's UI tool (agent_go/cmd/server/ui_control_contract.json) offers only the Dashboard (owner, 3 Nov);
// every view it offers must exist in this registry.
describe('the agent UI contract uses registered workspace views', () => {
  it('offers only registered view ids', () => {
    const contractFile = resolve(__dirname, '../../../../agent_go/cmd/server/ui_control_contract.json')
    const contract = JSON.parse(readFileSync(contractFile, 'utf8'))
    expect(contract).toEqual(UI_CONTROL_CONTRACT)
    const registered = WORKSPACE_VIEWS.map(v => v.id) as string[]
    for (const view of UI_CONTROL_CONTRACT.views) expect(registered).toContain(view.id)
  })
})

describe('persisted workspace view ids', () => {
  it('maps schedule and trigger destinations into Automation while retired Setup views fall back', () => {
    expect(WORKSPACE_VIEWS.map(view => view.id)).not.toContain('api-triggers')
    expect(normalizeWorkspaceViewId('api-triggers')).toBe('workshop')
    expect(normalizeWorkspaceViewId('schedules')).toBe('workshop')
    expect(normalizeWorkspaceViewId('webhooks')).toBe('workshop')
    // Bots and Gmail live under the Integrations tabs now: a persisted
    // standalone id remaps to nothing so restore falls back to the default.
    expect(normalizeWorkspaceViewId('bots')).toBeNull()
    expect(normalizeWorkspaceViewId('email')).toBeNull()
    // Secrets and folders live under the Identity tabs now.
    expect(normalizeWorkspaceViewId('secrets')).toBeNull()
    expect(normalizeWorkspaceViewId('folders')).toBeNull()
    expect(normalizeWorkspaceViewId('llm')).toBeNull()
    expect(normalizeWorkspaceViewId('identity')).toBe('identity')
    // Learnings, Knowledgebase, and Database live under the Knowledge tabs.
    expect(normalizeWorkspaceViewId('learnings')).toBe('knowledge')
    expect(normalizeWorkspaceViewId('knowledgebase')).toBe('knowledge')
    expect(normalizeWorkspaceViewId('database')).toBe('knowledge')
  })
})
