import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
vi.mock('../../services/api', () => ({ agentApi: {}, getApiBaseUrl: () => 'http://127.0.0.1:99999' }))
vi.mock('../../api/playbooks', () => ({ playbooksApi: { list: vi.fn(), listInstalled: vi.fn() } }))
import { manualPulseReviewMessage, PulseWorkspace, pulseTabReviewMessage } from './PulseWorkspace'
import { setProductCommands } from '../../commands/registry'
import { toAgentworksCommandDefinitions } from '../../commands/agentworksProductCommands'
import { AUTONOMY_LEVELS, autonomyLevelIndex } from './pulseAutonomy'

describe('PulseWorkspace information hierarchy', () => {
  it('leads with Goal Work for the user and keeps platform upkeep one tap away', () => {
    const html = renderToStaticMarkup(<PulseWorkspace workspacePath="Workflow/example"
      moduleStates={[{ workspace_path: 'Workflow/example', module: 'plan_drift_review', last_pulse_run_id: 'pulse-1', last_gate_decision: 'due', last_reason: 'Changed steps need checking.' }]}
      planDriftDue planDriftDueItems={[{ step_id: 'prepare-booking', step_type: 'regular', reason: 'The plan changed or a prior drift check remains unresolved.' }]}
      finalCommandStates={[]} reviewFocuses={[]} reviewFocusSelections={[]} statusError={null}
      goalWork={[
        { id: 'GW-1', kind: 'goal_work', title: 'Nobody replies to commenters', status: 'done', action_taken: 'Drafted replies for 12 commenters', detail: 'Own data: commenters we replied to followed back 22% vs 6% (n=40, 60 days).', links: ['pulse/work/2026-09-23/replies.md'], metric: 'followers', expected_direction: 'increase', check_at: '2026-09-30', created_at: '2026-09-23', updated_at: '2026-09-23' },
        { id: 'GW-2', kind: 'goal_work', title: 'Try a weekly carousel', status: 'idea', links: [], created_at: '2026-09-23', updated_at: '2026-09-23' },
        { id: 'GW-3', kind: 'constraint_challenge', title: 'Longer posts may grow followers', status: 'needs_user', decision_id: 'goal-work-length', constraint_text: 'Posts remain 150-350 words', constraint_class: 'choice', links: [], created_at: '2026-09-23', updated_at: '2026-09-23' },
      ]}
      autonomy={{ run: 'auto', outward: 'ask', change: 'auto' }} focusAreas={['Find more audience strategies like SaaS Builder']} onSaveFocusAreas={async () => true} />)
    expect(html).toContain('For you')
    expect(html).toContain('Platform health')
    expect(html).not.toContain('Needs your decision')
    expect(html.indexOf('Progress toward goals')).toBeLessThan(html.indexOf('Did for you'))
    expect(html.indexOf('Did for you')).toBeLessThan(html.indexOf('Challenging your rules'))
    expect(html.indexOf('Challenging your rules')).toBeLessThan(html.indexOf('Next up'))
    expect(html.indexOf('Next up')).toBeLessThan(html.indexOf('Pulse autonomy'))
    expect(html).toContain('Drafted replies for 12 commenters')
    expect(html).toContain('Why this should move the goal')
    expect(html).toContain('followed back 22% vs 6%')
    expect(html).toContain('replies.md')
    expect(html).toContain('Waiting to see the effect')
    expect(html).toContain('Try a weekly carousel')
    expect(html).toContain('Posts remain 150-350 words')
    expect(html).toContain('Choice: open to a test')
    expect(html).toContain('Focus areas')
    expect(html).toContain('Find more audience strategies like SaaS Builder')
    expect(html.indexOf('Focus areas')).toBeLessThan(html.indexOf('Did for you'))
    expect(html).toContain('Run Goal Work now')
    expect(html).toContain('Pulse autonomy')
    expect(html).toContain('type="range"')
    // run + change auto, outward ask = the "Edit workflow" stop.
    expect(html).toContain('Also edits steps and schedules. Asks before new posts or messages.')
    expect(html).not.toContain('Always asks you')
    expect(html).toContain('Workflow Review due')
    // Platform upkeep and the retired improvement ledger are not on the user's view.
    expect(html).not.toContain('Maintenance issues')
    expect(html).not.toContain('Strategic proposals')
    expect(html).not.toContain('Platform improvements')
    expect(html).not.toContain('Run automatically')
  })

  it('routes every manual review through its intended guided command', () => {
    expect(manualPulseReviewMessage('technical_review')).toContain('kind="engineering-review"')
    expect(manualPulseReviewMessage('architecture_review', 'Workflow/example')).toContain('workspace_path="Workflow/example"')
    expect(manualPulseReviewMessage('architecture_review', 'Workflow/example')).toContain('references/architecture-review.md')
    expect(manualPulseReviewMessage('strategic_review')).toContain('kind="strategy-auditor"')
    expect(manualPulseReviewMessage('plan_drift_review')).toContain('kind="review-artifact-drift"')
    expect(() => manualPulseReviewMessage('unknown')).toThrow('Unsupported Pulse review module')
  })

  it('maps stored Pulse permissions to the highest slider stop they fully allow', () => {
    expect(autonomyLevelIndex({ run: 'ask', outward: 'ask', change: 'ask' })).toBe(0)
    expect(autonomyLevelIndex({ run: 'auto', outward: 'ask', change: 'ask' })).toBe(1)
    expect(autonomyLevelIndex({ run: 'auto', outward: 'ask', change: 'auto' })).toBe(2)
    expect(autonomyLevelIndex({ run: 'auto', outward: 'auto', change: 'auto' })).toBe(3)
    // A mix no stop describes falls back to the highest stop it covers.
    expect(autonomyLevelIndex({ run: 'auto', outward: 'auto', change: 'ask' })).toBe(1)
    expect(autonomyLevelIndex({ run: 'ask', outward: 'auto', change: 'auto' })).toBe(0)
    expect(AUTONOMY_LEVELS.map(level => level.label)).toEqual(['Ask first', 'Run steps', 'Edit workflow', 'Full'])
  })

  it('runs the same product command from a Pulse tab button, with review and fix together', () => {
    // Before product commands load, the fallback still reviews and fixes.
    expect(pulseTabReviewMessage('technical_review')).toContain('kind="pulse-fixer"')
    setProductCommands(toAgentworksCommandDefinitions([{
      name: 'run-technical-review', description: 'Technical', icon: 'check-circle', aliases: ['pulse-review'], menuHidden: true,
      prompt: 'Run /run-technical-review as a BACKGROUND task. Review, then fix. Focus: {{context}}',
    }]))
    try {
      const message = pulseTabReviewMessage('technical_review')
      expect(message).toContain('Run /run-technical-review as a BACKGROUND task')
      expect(message).toContain('Requested from the Pulse tab.')
    } finally {
      setProductCommands([])
    }
  })
})
