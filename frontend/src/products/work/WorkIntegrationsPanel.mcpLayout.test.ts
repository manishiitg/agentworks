import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
describe('MCP ownership UI across products', () => {
 it('uses the same private and Vault sections in Crew, Code and workflows', () => {
  for (const file of ['src/products/work/WorkIntegrationsPanel.tsx', 'src/components/workflow/WorkflowCapabilitiesPanel.tsx']) {
   const source = readFileSync(file, 'utf8')
   expect(source).toContain('<ProjectMcpPanel')
   expect(source).not.toContain('<PlaceMcpSection')
   expect(source).not.toContain('<VaultMcpSection')
   expect(source).not.toContain('Platform connected')
   expect(source).not.toContain('Shared with everyone')
  }
 })
})
