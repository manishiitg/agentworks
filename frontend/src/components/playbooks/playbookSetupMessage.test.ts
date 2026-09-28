import { describe, expect, it } from 'vitest'
import { PLAYBOOK_CATALOG } from './playbookCatalog'
import { playbookSetupMessage } from './playbookSetupMessage'

describe('Playbook Builder setup prompt', () => {
  it('names every authored handoff and requires a blocking validator before its consumer', () => {
    const proposals = PLAYBOOK_CATALOG.filter(item => item.agentSlots?.length)
    // The catalog grows; guard against authored playbooks disappearing.
    expect(proposals.length).toBeGreaterThanOrEqual(40)
    for (const playbook of proposals) {
      const message = playbookSetupMessage(playbook, `agentworks-playbook-${playbook.id}`)
      expect(message).toContain('update_validation_schema on that Crew step')
      expect(message).toContain('context_output to the exact JSON artifact path')
      expect(message).toContain('blocking validation step before the consumer')
      expect(message).toContain('one valid and one rejected artifact')
      expect(message).toContain('one authorized customer-like case manually')
      expect(message).toContain('SETUP.json')
      for (const handoff of playbook.handoffs || []) {
        expect(message).toContain(`${handoff.id}: ${handoff.from} → ${handoff.to}, ${handoff.artifact_type}`)
      }
    }
  })

  it('keeps a Workflow guide focused on adaptation without invented Crew handoffs', () => {
    const guide = PLAYBOOK_CATALOG.find(item => item.id === 'basic-browser-setup')!
    const message = playbookSetupMessage(guide, 'agentworks-playbook-basic-browser-setup')
    expect(message).toContain('summarize what can be reused and what is missing')
    expect(message).not.toContain('Proposed handoffs')
  })
})
