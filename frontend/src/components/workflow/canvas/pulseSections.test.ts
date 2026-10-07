import { describe, expect, it } from 'vitest'
import { PULSE_FIXED_COMMANDS, PULSE_MODULE_COMMANDS } from './pulseSections'

describe('Pulse workspace registry', () => {
  it('exposes the complete database-native review module set once', () => {
    const moduleIds = PULSE_MODULE_COMMANDS.map(module => module.id)
    expect(moduleIds).toEqual([
      'technical_review',
      'architecture_review',
      'strategic_review',
      'plan_drift_review',
    ])
    expect(new Set(moduleIds).size).toBe(moduleIds.length)
  })

  it('exposes the complete finalization command set once', () => {
    const commandIds = PULSE_FIXED_COMMANDS.map(command => command.id)
    expect(commandIds).toEqual(['dashboard'])
    expect(new Set(commandIds).size).toBe(commandIds.length)
  })

  it('exposes one combined strategic lifecycle', () => {
    const moduleIds = PULSE_MODULE_COMMANDS.map(module => module.id)
    expect(moduleIds.filter(module => module === 'strategic_review')).toHaveLength(1)
    expect(moduleIds).not.toContain('strategy_auditor')
    expect(moduleIds).not.toContain('goal_advisor')
  })
})
