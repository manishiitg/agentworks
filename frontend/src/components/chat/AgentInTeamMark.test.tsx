// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import { AgentInTeamMark, agentTeamShareFromRow, describeAgentShare } from './AgentInTeamMark'
import { AgentHeaderLine, type CrewAgentRow } from './AgentCard'

vi.mock('../../products/work/ShareCrew', () => ({
  HeaderAgentSharing: ({ agentKey, defaultOpen }: { agentKey: string; defaultOpen?: boolean }) => (
    <div data-testid="header-sharing" data-agent-key={agentKey} data-default-open={defaultOpen ? 'true' : 'false'} />
  ),
}))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('AgentInTeamMark and describeAgentShare', () => {
  it('returns null for private agents and renders nothing', async () => {
    expect(describeAgentShare(undefined)).toBeNull()
    expect(describeAgentShare({ inTeam: false, audience: 'whole_team', chatShare: 'live' })).toBeNull()
    expect(agentTeamShareFromRow({ mine: true, in_team: false, audience: 'whole_team', chat_share: 'live' })).toBeUndefined()

    const host = document.createElement('div')
    const root = createRoot(host)
    try {
      await act(async () => root.render(<AgentInTeamMark on={false} />))
      expect(host.innerHTML).toBe('')
    } finally {
      await act(async () => root.unmount())
    }
  })

  it('distinguishes everyone vs chosen people and chat visible vs hidden', () => {
    expect(describeAgentShare({ inTeam: true, audience: 'whole_team', chatShare: 'live' })).toEqual({
      headerLabel: 'Team · Chat visible',
      shortLabel: 'Team',
      tooltip: 'Shared with everyone on the Team · Chat visible',
      tone: 'team-visible',
      chosen: false,
      hidden: false,
    })
    expect(describeAgentShare({ inTeam: true, audience: 'whole_team', chatShare: 'agent' })).toEqual({
      headerLabel: 'Team · Chat hidden',
      shortLabel: 'Chat hidden',
      tooltip: 'Shared with everyone on the Team · Chat hidden (teammates can ask, not read chat)',
      tone: 'team-hidden',
      chosen: false,
      hidden: true,
    })
    expect(describeAgentShare({ inTeam: true, audience: 'chosen', chosenPeople: ['Ana', 'Sam'], chatShare: 'agent' })).toEqual({
      headerLabel: '2 people · Chat hidden',
      shortLabel: '2 ppl · hidden',
      tooltip: 'Shared with Ana, Sam · Chat hidden',
      tone: 'chosen',
      chosen: true,
      hidden: true,
    })
    expect(describeAgentShare({ inTeam: true, audience: 'chosen', chosenPeople: ['Ana'], chatShare: 'live' })).toEqual({
      headerLabel: 'Ana · Chat visible',
      shortLabel: 'Ana',
      tooltip: 'Shared with Ana · Chat visible',
      tone: 'chosen',
      chosen: true,
      hidden: false,
    })
  })
})

describe('AgentHeaderLine sharing pill', () => {
  it('hides the pill for private agents and opens expanded sharing controls for shared agents', async () => {
    const host = document.createElement('div')
    const root = createRoot(host)
    const privateRow: CrewAgentRow = {
      tabId: 't1',
      name: 'Scratchpad',
      main: false,
      info: { name: 'Scratchpad', sharing: { projectId: 'crew', agentKey: 'scratch' } },
    }
    const sharedRow: CrewAgentRow = {
      tabId: 't2',
      name: 'Design',
      main: false,
      inTeam: true,
      teamShare: { inTeam: true, audience: 'whole_team', chatShare: 'agent' },
      info: {
        name: 'Design',
        sharing: { projectId: 'crew', agentKey: 'design' },
        teamShare: { inTeam: true, audience: 'whole_team', chatShare: 'agent' },
      },
    }
    try {
      await act(async () => root.render(<AgentHeaderLine crewName="ai-work" agent={privateRow} />))
      expect(host.textContent).not.toContain('Only you')
      expect(host.textContent).not.toContain('Team')

      await act(async () => root.render(<AgentHeaderLine crewName="ai-work" agent={sharedRow} />))
      const pill = host.querySelector<HTMLButtonElement>('button[aria-label*="Chat hidden"]')
      expect(pill).not.toBeNull()
      expect(pill?.textContent).toContain('Team · Chat hidden')

      await act(async () => pill!.click())
      const dialog = document.body.querySelector('[role="dialog"][aria-label="Sharing for Design"]')
      expect(dialog).not.toBeNull()
      const sharing = dialog?.querySelector('[data-testid="header-sharing"]')
      expect(sharing?.getAttribute('data-agent-key')).toBe('design')
      expect(sharing?.getAttribute('data-default-open')).toBe('true')
    } finally {
      await act(async () => root.unmount())
    }
  })
})
