import { readFileSync, readdirSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const read = (path: string) => readFileSync(path, 'utf8')

describe('Crew shared AgentWorks platform contract', () => {
  it('composes the shared conversation and workspace primitives', () => {
    const surface = read('src/products/work/WorkSurface.tsx')
    const pane = read('src/products/work/WorkWorkspacePane.tsx')
    const setup = read('src/products/work/WorkIdentityPanel.tsx')
      + read('src/products/work/WorkIntegrationsPanel.tsx')

    for (const sharedConversationPrimitive of [
      "../../components/ChatArea",
      "../../components/workspace/WorkspaceSplitDivider",
      "../../platform/ui-control/useWorkspaceUIControl",
    ]) {
      expect(surface).toContain(sharedConversationPrimitive)
    }

    for (const sharedWorkspacePrimitive of [
      "../../components/FileWorkspacePane",
      "../../components/automation/AutomationHubPanel",
      "../../components/workflow/BrowserWorkspacePanel",
      "../../components/workflow/DatabaseView",
      "../../components/workflow/ReportViewer",
      "../../components/workflow/CostsPopup",
    ]) {
      expect(pane).toContain(sharedWorkspacePrimitive)
    }

    // Setup is consolidated into Identity + Integrations tabs, which keep
    // composing the shared primitives instead of forking them.
    expect(pane).toContain('./WorkIdentityPanel')
    expect(pane).toContain('./WorkIntegrationsPanel')
    for (const sharedSetupPrimitive of [
      "../../components/workflow/WorkflowBotsPanel",
      "../../components/skills/SkillsManagerPanel",
      // Secret selection is composed through the shared Integrations panel (it renders SecretSelectionSection).
      "../../components/integrations/ProjectSecretsPanel",
    ]) {
      expect(setup).toContain(sharedSetupPrimitive)
    }
  })

  it('keeps browser and automation behavior in components shared with AgentWorks', () => {
    const workflowLayout = read('src/components/workflow/WorkflowLayout.tsx')
    const workflowCapabilities = read('src/components/workflow/WorkflowCapabilitiesPanel.tsx')
    const workPane = read('src/products/work/WorkWorkspacePane.tsx')

    expect(workflowLayout).toContain("../automation/AutomationHubPanel")
    expect(workPane).toContain("../../components/automation/AutomationHubPanel")
    expect(workflowCapabilities).toContain("./BrowserWorkspacePanel")
    expect(workPane).toContain("../../components/workflow/BrowserWorkspacePanel")
  })

  it('uses the complete shared split rail in AgentWorks and Crew', () => {
    const workflowLayout = read('src/components/workflow/WorkflowLayout.tsx')
    const workSurface = read('src/products/work/WorkSurface.tsx')

    for (const consumer of [workflowLayout, workSurface]) {
      expect(consumer).toContain('<WorkspaceSplitRail')
      expect(consumer).toContain('onPreviewDeviceChange=')
      expect(consumer).toContain('onCollapseChat=')
      expect(consumer).toContain('onCollapseWorkspace=')
      expect(consumer).not.toContain('<WorkspaceSplitCollapseControls')
    }
  })

  it('opens Crew history beside the permanent chat instead of replacing it', () => {
    const surface = read('src/products/work/WorkSurface.tsx')
    const pane = read('src/products/work/WorkWorkspacePane.tsx')
    const resume = read('src/hooks/useResumePreviousChat.ts')

    expect(surface).toContain('<WorkChatTabs')
    expect(surface).toContain("tab.metadata?.isViewOnly === true")
    expect(pane).toContain('allowOpen')
    expect(pane).toContain('activeSessionId={canonicalSessionId || activeSessionId}')
    expect(pane).toContain('onSelectSession={openHistoryChat}')
    expect(resume).toContain("targetTab.metadata.agentProfileId === 'work'")
    expect(resume).toContain('canonical?.sessionId === session.session_id')
    expect(resume).toContain('isViewOnly: true')
    expect(resume).not.toContain("if (targetTab.metadata.agentProfileId === 'work') {\n      const profileId")
  })

  it('identifies Crew chats by their Crew name in the shared switcher', () => {
    const switcher = read('src/components/QuickSwitcher.tsx')

    expect(switcher).toContain('label: project,')
    expect(switcher).toContain('subtitle: `Crew · ${role}')
    expect(switcher).not.toContain('label: tab.metadata?.agentProfileBuilder ? project : tab.name')
  })

  it('offers permanent Crew deletion with the shared confirmation dialog', () => {
    const surface = read('src/products/work/WorkSurface.tsx')

    expect(surface).toContain('title={`Delete ${product.noun}`}')
    expect(surface).toContain('confirmText={`Delete ${product.noun}`}')
    expect(surface).toContain('onDelete={setDeleteCandidate}')
    expect(surface).toContain('await remove(deleteCandidate.id)')
  })

  it('does not add product-local replacements for shared platform surfaces', () => {
    const files = readdirSync('src/products/work')
    const forbiddenProductForks = [
      'WorkChatArea.tsx',
      'WorkChatInput.tsx',
      'WorkTranscript.tsx',
      'WorkFileWorkspacePane.tsx',
      'WorkTerminal.tsx',
      'WorkBrowserWorkspacePanel.tsx',
      'WorkDatabaseView.tsx',
      'WorkDashboard.tsx',
      'WorkAutomationHubPanel.tsx',
      'WorkCostsPanel.tsx',
      'WorkBotsPanel.tsx',
      'WorkSkillsManager.tsx',
      'WorkSecretsPanel.tsx',
      'WorkMCPBrowser.tsx',
    ]

    expect(files.filter(file => forbiddenProductForks.includes(file))).toEqual([])
  })
})
