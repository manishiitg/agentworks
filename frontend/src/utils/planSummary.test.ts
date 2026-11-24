import { describe, expect, it } from 'vitest'
import { planFromQuestion, summarizePlan } from './planSummary'

describe('summarizePlan', () => {
  it('takes the title, step count and first line from a markdown plan', () => {
    const plan = planFromQuestion('Approve this plan?\n# Add rate limiting\n\nUploads have no limit.\n\n1. Add a limiter.\n2. Wire it in.\n3. Tests.')
    expect(summarizePlan(plan)).toEqual({ title: 'Add rate limiting', steps: 3, peek: 'Uploads have no limit.', body: plan })
  })

  it('counts bullets when there are no numbered steps, and drops a "Plan:" prefix', () => {
    const summary = summarizePlan('Plan: tidy the docs\n- one\n- two')
    expect(summary.title).toBe('tidy the docs')
    expect(summary.steps).toBe(2)
    expect(summary.peek).toBe('one')
  })

  it('handles an empty plan', () => {
    expect(summarizePlan('')).toEqual({ title: 'Plan', steps: 0, peek: '', body: '' })
  })
})
