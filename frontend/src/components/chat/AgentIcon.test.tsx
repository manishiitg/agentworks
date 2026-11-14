import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { AgentSetupMark } from './AgentIcon'
import type { TeamTabSetup } from '../../products/work/teamApi'

// Owner-requested distinction: sharing must never gate the schedule or trigger mark.
it('distinguishes schedules and triggers on private and shared agents', () => {
  const schedule = { kind: 'Schedule', name: 'Daily report', line: 'every day', on: true }
  const trigger = { kind: 'Webhook', name: 'Incoming event', line: 'when called', on: true }
  const draw = (reach: TeamTabSetup['reach'], automations: TeamTabSetup['automations']) => renderToStaticMarkup(<AgentSetupMark setup={{ reach, starts_on_own: automations.some(item => item.on), automations }} />)
  for (const reach of ['', 'team', 'agentbridge'] as const) {
    const scheduled = draw(reach, [schedule])
    expect(scheduled).toContain('lucide-clock')
    expect(scheduled).not.toContain('lucide-zap')
    expect(scheduled).toContain('Daily report')
    expect(scheduled).toContain('every day')
    const triggered = draw(reach, [trigger])
    expect(triggered).toContain('lucide-zap')
    expect(triggered).not.toContain('lucide-clock')
    expect(triggered).toContain('Incoming event')
    const mixed = draw(reach, [schedule, trigger])
    expect(mixed).toContain('lucide-clock')
    expect(mixed).toContain('lucide-zap')
  }
  expect(draw('', [{ ...schedule, on: false }])).toBe('')
})
