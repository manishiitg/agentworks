import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('Workflow MCP panel layout', () => {
  it('scrolls the content below a fixed header and has no Save footer', () => {
    const panel = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')
    const connectors = readFileSync('src/components/connectors/ConnectorsBrowser.tsx', 'utf8')

    // The header stays fixed (Pulse structure): the section itself never
    // scrolls; the tab content owns the scroll surface instead.
    const section = panel.split('\n').find(line => line.includes('<section className='))
    expect(section).toBeDefined()
    expect(section).not.toContain('overflow-y-auto')
    expect(panel).toContain("(section === 'mcp' || section === 'identity') ? 'min-h-0 flex-1 overflow-y-auto'")
    expect(panel).toContain('manageOwnScroll={false}')
    expect(connectors).toContain("manageOwnScroll ? 'min-h-0 flex-1 overflow-y-auto pt-5' : 'pt-5'")
    expect(panel).toMatch(/mcp:\s*\{[\s\S]*?savesViaManifest: false,/)
  })





  it('places Refresh in capability headers without rendering a second workspace title bar', () => {
    const panel = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')
    const host = readFileSync('src/components/workflow/canvas/WorkspaceViewHost.tsx', 'utf8')

    expect(panel).toContain('<WorkspaceViewActions')
    expect(host).not.toContain('data-ui-view-assistant')
    // Access is the one shared People panel, mounted without a header action.
    expect(host).toContain('<ShareDashboardButton workspacePath={workspacePath} variant="panel" />')
    // Knowledge is a self-served umbrella (like capabilities sections).
    expect(host).toContain("case 'knowledge':")
    expect(host).toContain('<KnowledgeView workspacePath={workspacePath} plan={plan} />')
  })

  it('embeds workflow skills inside the Integrations section instead of a standalone view', () => {
    const panel = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')
    const views = readFileSync('src/components/workflow/workspaceViews.ts', 'utf8')
    const host = readFileSync('src/components/workflow/canvas/WorkspaceViewHost.tsx', 'utf8')
    const skillsPanel = readFileSync('src/components/skills/SkillsManagerPanel.tsx', 'utf8')

    expect(panel).toMatch(/section === 'mcp'[\s\S]*?<SkillsManagerPanel/)
    expect(panel).not.toContain("section === 'skills'")
    expect(views).not.toMatch(/id: 'skills'/)
    expect(host).not.toContain("case 'skills':")
    expect(skillsPanel).toContain('manageOwnScroll')
    // One workspace inventory includes main chat, steps and native installs.
    expect(skillsPanel).not.toContain('selectedOnly')
    expect(skillsPanel).toContain('skillsApi.listSkills(workspacePath)')
    expect(panel).toContain("profileId={relayMode ? 'relays' : 'agentworks'}")
    expect(panel).toContain('customOnly')
    expect(panel).toContain('hideHeader={skillsView}')
    expect(panel).not.toContain('Install a skill')
  })



  it('keeps AgentWorks channel tabs while Flow shows MCPs, Skills and Google apps', () => {
    const panel = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')

    // The section list (Vault, Slack, WhatsApp, Google apps, Use in AI apps) is shared with Crew and Code.
    const sections = readFileSync('src/components/integrations/integrationSections.ts', 'utf8')
    expect(sections).toMatch(/'apps'[\s\S]*?'slack'[\s\S]*?'whatsapp'[\s\S]*?'gmail'/)
    expect(sections).toContain("{ value: 'apps', label: 'Integrations'")
    // One resource navigation replaces the old intermediate section directory.
    expect(panel).not.toContain('<IntegrationNavigation')
    expect(panel).not.toContain('ConnectedProjectAccounts')
    expect(panel).not.toContain('<IntegrationSectionPicker')
    expect(panel).toContain('tabs={section ===')
    expect(panel).toContain('<ProjectPluginsPanel')
    expect(panel).toContain('<ProjectIntegrationPanel')
    expect(panel).toContain('<ProductSlackPanel')
    expect(panel).toContain("shareChannel === 'slack'")
    expect(panel).toContain("allowBots={!relayMode}")
    expect(panel).not.toContain('<UseSharePanel')
    expect(panel).not.toContain('WorkflowRelaySlackPanel')
    expect(sections).toContain("label: 'Google apps'")
  })

  it('keeps route hints and Gmail inside Integrations instead of standalone views', () => {
    const panel = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')
    const views = readFileSync('src/components/workflow/workspaceViews.ts', 'utf8')
    const host = readFileSync('src/components/workflow/canvas/WorkspaceViewHost.tsx', 'utf8')
    const chips = readFileSync('src/components/workflow/bots/RouteChips.tsx', 'utf8')

    expect(chips).toContain('This route answers for another workflow')
    const gmail = readFileSync('src/components/workflow/bots/GmailNotifications.tsx', 'utf8')
    // The accounts list now lives in the Gmail section, described as "Your Google accounts".
    expect(gmail).toContain('Google accounts added to this item')
    expect(gmail).toContain('Your own Google accounts above work independently of company email defaults')
    expect(gmail).toContain('<GoogleAccountList')
    expect(gmail).not.toContain('gmailOpen')
    expect(panel).toMatch(/section === 'mcp'[\s\S]*?<WorkflowBotsPanel/)
    expect(panel).toMatch(/section === 'mcp'[\s\S]*?<WorkflowEmailPanel/)
    expect(panel).not.toContain("section === 'bots'")
    expect(panel).not.toContain("section === 'email'")
    expect(views).not.toMatch(/id: 'bots'/)
    expect(views).not.toMatch(/id: 'email'/)
    expect(host).not.toContain("case 'bots':")
    expect(host).not.toContain("case 'email':")
  })



  it('routes skill adds through builder chat instead of toggling directly', () => {
    const panel = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')
    const manager = readFileSync('src/components/skills/SkillsManagerPanel.tsx', 'utf8')
    const row = readFileSync('src/components/skills/SkillRow.tsx', 'utf8')

    expect(panel).toContain('sendWorkspacePaneMessageToChat')
    expect(panel).toContain('onAddViaChat')
    expect(manager).toContain('onAddViaChat')
    expect(row).toContain('onRequestAdd')
    expect(row).toContain('MessageCircle')
    expect(row).toContain('<Check')
  })


})
