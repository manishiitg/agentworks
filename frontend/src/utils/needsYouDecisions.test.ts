import { describe, expect, it } from 'vitest'
import type { ReportHumanInput } from '../services/api-types'
import { needsYouDecisions } from './needsYouDecisions'

describe('needsYouDecisions', () => {
  it('is the unanswered ones plus answered ones not applied yet', () => {
    const pending = { id: 'p', status: 'pending' } as ReportHumanInput
    const unapplied = { id: 'a', status: 'answered', apply_message: 'APPLY a' } as ReportHumanInput
    const waiting = { id: 'w', status: 'answered' } as ReportHumanInput
    expect(needsYouDecisions([pending], [unapplied, waiting]).map(input => input.id)).toEqual(['p', 'a'])
  })
})
