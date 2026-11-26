import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { AgentSetupMark } from './AgentIcon'
import type { TeamTabSetup } from '../../products/work/teamApi'

// Sharing must never gate the automation mark; every automation kind shows the one clock (owner, 18 Nov).
it('shows one clock for any automation on private and shared agents', () => {
  const schedule = { kind: 'Schedule', name: 'Daily report', line: 'every day', on: true }
  const trigger = { kind: 'Webhook', name: 'Incoming event', line: 'when called', on: true }
  const draw = (reach: TeamTabSetup['reach'], automations: TeamTabSetup['automations']) => renderToStaticMarkup(<AgentSetupMark setup={{ reach, starts_on_own: automations.some(item => item.on), automations }} />)
  for (const reach of ['', 'team', 'agentbridge'] as const) {
    for (const automations of [[schedule], [trigger], [schedule, trigger]]) {
      const mark = draw(reach, automations)
      expect(mark.match(/lucide-clock/g)).toHaveLength(1)
      for (const item of automations) expect(mark).toContain(item.name)
    }
  }
  expect(draw('', [{ ...schedule, on: false }])).toBe('')
})
