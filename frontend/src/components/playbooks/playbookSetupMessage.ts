import type { PlaybookCatalogItem } from './playbookCatalog'

export function playbookSetupMessage(playbook: PlaybookCatalogItem, skillName: string): string {
  const base = `Read the installed skill ${skillName} with read_skill, then follow it to configure this workflow. First inspect the existing workflow and summarize what can be reused and what is missing. Ask focused questions for unresolved customer choices before changing the workflow, record the answers as customer direction, and treat installation as guidance rather than approval.`
  if (!playbook.agentSlots?.length) return `${base} ${playbook.setupPrompt || ''}`.trim()

  const handoffs = (playbook.handoffs || [])
    .map(edge => `${edge.id}: ${edge.from} → ${edge.to}, ${edge.artifact_type}, ${edge.required ? 'required' : 'optional'}`)
    .join('; ')
  const route = handoffs ? ` Proposed handoffs: ${handoffs}.` : ''
  return `${base} Read this installed package's playbook.json and SETUP.json before plan edits. Inspect existing Crew identities and source access; propose reuse or reviewed creation.${route} For every required or selected optional handoff, set the producer Crew step's context_output to the exact JSON artifact path and use update_validation_schema on that Crew step to require the artifact and its load-bearing fields. Bind the consumer to that same artifact path, inspect the bundled business validator's actual command and inputs, and add a blocking validation step before the consumer can run. Test one valid and one rejected artifact, then run one authorized customer-like case manually; keep validation output, source IDs, and run IDs. Record each setup check only with real evidence, report blocked checks, and leave triggers, schedules, external writes, and Crew actions off until separately reviewed. ${playbook.setupPrompt || ''}`.trim()
}
