import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('identity section layout', () => {
  it('separates general, secrets, folders, llm, and upgrades into header tabs with general first', () => {
    const panel = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')

    expect(panel).toContain("relayMode ? 'relays.tab.workflow-identity' : 'agentworks.tab.workflow-identity', 'general'")
    expect(panel).toMatch(/IDENTITY_TABS[^=]*=[\s\S]*?'general'[\s\S]*?'secrets'[\s\S]*?'folders'[\s\S]*?'llm'[\s\S]*?'upgrades'/)
    expect(panel).toContain("ariaLabel: 'Identity'")
    expect(panel).toContain('getIdentityTabAskAIMessage(activeIdentityTab)')
    expect(panel).toContain("const RELAY_IDENTITY_TABS = IDENTITY_TABS.filter(option => option.value === 'general' || option.value === 'llm')")
    expect(panel).toContain('builderOnly={relayMode}')
  })

  it('embeds identity, secrets, folders, llm, and upgrades panels inside the Identity tabs instead of standalone views', () => {
    const panel = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')
    const views = readFileSync('src/components/workflow/workspaceViews.ts', 'utf8')
    const host = readFileSync('src/components/workflow/canvas/WorkspaceViewHost.tsx', 'utf8')

    expect(panel).toMatch(/section === 'identity'[\s\S]*?<WorkflowIdentityPanel/)
    expect(panel).toMatch(/section === 'identity'[\s\S]*?<SecretSelectionSection/)
    expect(panel).toMatch(/section === 'identity'[\s\S]*?<WorkflowFolderAccessView/)
    expect(panel).toMatch(/section === 'identity'[\s\S]*?<WorkflowLLMConfigurationPanel/)
    expect(panel).toMatch(/section === 'identity'[\s\S]*?<WorkflowUpdatesView/)
    expect(panel).not.toContain("section === 'secrets'")
    expect(panel).not.toContain("section === 'llm'")
    expect(views).not.toMatch(/id: 'secrets'/)
    expect(views).not.toMatch(/id: 'folders'/)
    expect(views).not.toMatch(/id: 'llm'/)
    expect(views).not.toMatch(/id: 'updates'/)
    expect(views).toMatch(/id: 'identity'/)
    expect(host).not.toContain("case 'secrets':")
    expect(host).not.toContain("case 'folders':")
    expect(host).not.toContain("case 'llm':")
    expect(host).not.toContain("case 'updates':")
    expect(host).toContain("case 'identity':")
  })

  it('embeds folders without its own header or scroll', () => {
    const panel = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')

    expect(panel).toContain('<WorkflowFolderAccessView workspacePath={workspacePath} hideHeader manageOwnScroll={false} />')
  })

  it('lets users expand each upgrade to see what changed', () => {
    const updates = readFileSync('src/components/workflow/WorkflowUpdatesView.tsx', 'utf8')

    expect(updates).toContain('aria-expanded={expanded}')
    expect(updates).toContain('What changed')
    expect(updates).toContain("item.details || 'No migration details are available.'")
  })

  it('attaches only workflow and global secrets, never personal ones', () => {
    const panel = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')
    const section = readFileSync('src/components/secrets/SecretSelectionSection.tsx', 'utf8')

    expect(panel).toContain('<SecretSelectionSection')
    expect(section).not.toContain('showSharedSecrets')
    expect(section).not.toContain('sharedSecrets')
    expect(section).not.toContain('>Shared<')
  })
})
