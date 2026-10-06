import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import { WORK_PANELS } from '../productPanels'

describe('Work Dashboard', () => {
  it('reuses the shared dashboard and database views with project-scoped data', () => {
    const source = readFileSync('src/products/work/WorkWorkspacePane.tsx', 'utf8') + readFileSync('src/products/productPanels.ts', 'utf8')
    const identity = readFileSync('src/products/work/WorkIdentityPanel.tsx', 'utf8')

    expect(source).toContain("{ id: 'dashboard', label: 'Dashboard'")
    expect(source).toContain("{ id: 'memory', label: 'Memory'")
    expect(source).toContain("{ id: 'database', label: 'Database'")
    expect(source).toContain('<ReportDocumentSwitcher workspacePath={workspacePath}')
    expect(source).not.toContain('documentPath="db/reports/index.html"')
    expect(source).toContain("view === 'database'")
    expect(source).toContain('sendChatMessage={sendDashboardMessage}')
    expect(source).toContain("From this project's dashboard:")
    expect(source).toContain('emptyIdentity={{ icon: projectIdentity?.icon, name: projectIdentity?.name || projectTitle, projectName: projectTitle }}')
    expect(source).toContain('selectedGlobalSecrets={selectedGlobalSecrets}')
    // Secret selection moved out of Identity into Integrations > Connections > Vault/Secrets.
    expect(identity).not.toContain('persistExplicitGlobalSelection')
    expect(readFileSync('src/products/work/WorkIntegrationsPanel.tsx', 'utf8')).toContain('onSelectedGlobalSecretsChange')
  })

  it('shows durable project memory and links it to Crew skills', () => {
    const pane = readFileSync('src/products/work/WorkWorkspacePane.tsx', 'utf8')
    const memory = readFileSync('src/products/work/WorkMemoryPanel.tsx', 'utf8')

    expect(pane).toContain("view === 'memory' && <WorkMemoryPanel")
    expect(memory).toContain('`${workspacePath}/MEMORY.md`')
    expect(memory).toContain('title="Memory"')
    expect(memory).toContain('Custom skills')
    expect(memory).toContain('`${workspacePath}/skills`')
    expect(memory).toContain('loadCrewSkills(workspacePath, files)')
    expect(memory).toContain('Crew has not created any custom skills yet.')
    expect(memory).not.toContain('skillsApi.listSkills')
    expect(memory).not.toContain('View and manage skills')
    expect(memory).not.toContain('onOpenSkills')
    expect(memory).toContain('const deduplicated = new Map<string, CrewSkill>()')
    expect(memory).toContain('if (!deduplicated.has(identity))')
    expect(memory).toContain('onClick={() => onOpenFile(skill.filePath)}')
    expect(pane).toContain("onViewChange('files')")
    expect(pane).toContain('agentApi.getPlannerFileContent(filePath)')
  })
})

describe('Work Setup consolidation', () => {
  it('groups Setup into Identity then Integrations with shared headers and tabs', () => {
    const source = readFileSync('src/products/work/WorkWorkspacePane.tsx', 'utf8')
    const identity = readFileSync('src/products/work/WorkIdentityPanel.tsx', 'utf8')
    const integrations = readFileSync('src/products/work/WorkIntegrationsPanel.tsx', 'utf8')
    const gating = readFileSync('src/products/work/workViewGating.ts', 'utf8')

    // Setup is Identity then Integrations; no bots, models or email panels.
    expect(WORK_PANELS.setup.map(panel => panel.id)).toEqual(['identity', 'mcp'])
    expect(source).toContain("view === 'identity' && <WorkIdentityPanel")
    expect(source).toContain("view === 'mcp' && <WorkIntegrationsPanel")
    expect(source).toContain('isWorkWorkspaceViewEnabled(view, enabledPanels, localCodeSession)')
    expect(gating).toContain("if (view === 'identity' || view === 'plan' || view === 'suggestions') return true")
    expect(gating).toContain("enabledPanels.has('mcp') || enabledPanels.has('skills')")
    expect(identity).toContain("tabs={modelsOnly ? undefined : { value: activeTab")
    expect(identity).toContain("ariaLabel: 'Identity'")
    expect(identity).toContain("{ value: 'general', label: 'General' }")
    expect(identity).not.toContain("{ value: 'secrets'")
    expect(identity).not.toContain("label: 'Connected work'")
    expect(identity).toContain("{ value: 'models', label: 'Models' }")
    expect(identity).toContain('onUpdateIdentity={onUpdateIdentity}')
    expect(identity).toContain('onDeleteRequest={onDeleteRequest}')
    expect(identity).toContain('hideAdd')
    expect(identity).not.toContain('Attach another folder')
    expect(identity).not.toContain('Absolute folder path')
    expect(identity).toContain('<IconUploadField')
    // Integrations opens on a section picker; a section shows Integrations > section breadcrumbs.
    expect(integrations).toContain('<IntegrationSectionPicker')
    expect(integrations).toContain('<WorkspaceViewBreadcrumbs parent="Integrations"')
    expect(integrations).toContain('<WorkspaceViewHeader')
    // The Integrations section definitions are shared with workflows (integrationSections.ts); the first is "Tools & secrets".
    const sections = readFileSync('src/components/integrations/integrationSections.ts', 'utf8')
    expect(sections).toContain("{ value: 'apps', label: 'Tools & secrets'")
    expect(integrations).toContain("ariaLabel: 'Integrations'")
    const plugins = readFileSync('src/components/integrations/ProjectPluginsPanel.tsx', 'utf8')
    for (const tab of ["{ value: 'connected', label: 'Connected MCPs' }", "{ value: 'available', label: 'Available MCPs' }", "{ value: 'secrets', label: 'Secrets' }", "{ value: 'skills', label: 'Skills' }", "{ value: 'vault', label: 'Vault' }"]) expect(plugins).toContain(tab)
    for (const section of ["{ value: 'slack', label: 'Slack'", "{ value: 'whatsapp', label: 'WhatsApp'", "{ value: 'gmail', label: 'Google apps'", "{ value: 'cli', label: 'Use in AI apps'"]) expect(sections).toContain(section)
    expect(integrations).toContain('fixedChannel="slack"')
    expect(integrations).toContain('fixedChannel="whatsapp"')
    expect(integrations).toContain('<WorkflowEmailPanel')
    expect(integrations).toContain('scopeNoun="project"')
    expect(integrations).toContain('className="min-h-0 flex-1 overflow-y-auto p-4"')
    expect(integrations.match(/manageOwnScroll=\{false\}/g)?.length).toBe(1)
  })
})
