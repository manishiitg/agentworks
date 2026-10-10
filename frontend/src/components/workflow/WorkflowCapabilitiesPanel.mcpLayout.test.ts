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





  it('places Refresh and Ask AI in capability headers without rendering a second workspace title bar', () => {
    const panel = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')
    const host = readFileSync('src/components/workflow/canvas/WorkspaceViewHost.tsx', 'utf8')

    expect(panel).toContain('<WorkspaceViewActions')
    expect(panel).toContain('getIntegrationTabAskAIMessage(activeMcpTab')
    expect(panel).toContain('getWorkspaceAskAIMessage(section)')
    expect(panel).toContain("message={getWorkspaceAskAIMessage('browser')}")
    expect(panel).toContain('refreshLabel="Refresh Browser"')
    expect(host).not.toContain('data-ui-view-assistant')
    for (const view of ['costs', 'execution-logs', 'schedules', 'pulse', 'backup', 'publish', 'notify']) {
      expect(host).toContain(`headerAction={askAI('${view}')}`)
    }
    // Access owns its Ask AI + refresh pair inside the view; the host mounts
    // it without a header action.
    const access = readFileSync('src/components/workflow/WorkflowAccessView.tsx', 'utf8')
    expect(access).toContain('getAccessTabAskAIMessage(activeTab)')
    expect(access).toContain('onRefresh')
    expect(host).toContain('<WorkflowAccessView workspacePath={workspacePath} />')
    // Folders moved under the Identity tabs; it no longer has a host header.
    expect(host).not.toContain("headerAction={refreshAndAskAI('folders')}")
    // Knowledge is a self-served umbrella (like capabilities sections): the
    // host mounts it without header actions; its Ask AI follows the active tab.
    expect(host).toContain("case 'knowledge':")
    expect(host).toContain('<KnowledgeView workspacePath={workspacePath} plan={plan} />')
    expect(host).toContain("getWorkspaceAskAIMessage('report')")
    expect(host).toContain("getWorkspaceAskAIMessage('flow')")
    // The Files view has no Ask AI (it is a plain Explorer).
    expect(host).not.toContain("getWorkspaceAskAIMessage('files')")
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
    expect(panel).toContain('Install a skill')
  })



  it('keeps AgentWorks channel tabs while Relay shows MCPs, Skills and Google apps', () => {
    const panel = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')

    // The section list (Tools & secrets, Slack, WhatsApp, Google apps, Use in AI apps) is shared with Crew and Code.
    const sections = readFileSync('src/components/integrations/integrationSections.ts', 'utf8')
    expect(panel).toContain('const MCP_TABS = PROJECT_INTEGRATION_SECTIONS')
    expect(sections).toMatch(/'apps'[\s\S]*?'slack'[\s\S]*?'whatsapp'[\s\S]*?'gmail'/)
    expect(sections).toContain("{ value: 'apps', label: 'Tools & secrets'")
    // Relay shows Tools & secrets (MCPs, secrets, skills, Vault now live in that one section) and Google apps.
    expect(panel).toContain("const RELAY_MCP_TABS = MCP_TABS.filter(option => option.value === 'apps' || option.value === 'gmail')")
    expect(panel).toContain('const mcpTabs = relayMode ? RELAY_MCP_TABS : MCP_TABS')
    expect(panel).toContain('tabs={section ===')
    expect(panel).toContain('options: projectPluginTabs()')
    expect(panel).toContain("ariaLabel: 'Integrations'")
    expect(panel).toContain('fixedChannel="slack"')
    expect(panel).toContain('fixedChannel="whatsapp"')
    expect(panel).not.toContain('WorkflowRelaySlackPanel')
    expect(sections).toContain("label: 'Google apps'")
  })

  it('embeds bots and gmail inside the Integrations tabs instead of standalone views', () => {
    const panel = readFileSync('src/components/workflow/WorkflowCapabilitiesPanel.tsx', 'utf8')
    const views = readFileSync('src/components/workflow/workspaceViews.ts', 'utf8')
    const host = readFileSync('src/components/workflow/canvas/WorkspaceViewHost.tsx', 'utf8')
    const chips = readFileSync('src/components/workflow/bots/RouteChips.tsx', 'utf8')
    const slack = readFileSync('src/components/workflow/bots/SlackSetup.tsx', 'utf8')

    expect(chips).toContain('This route answers for another workflow')
    // One question per workflow; platform settings live with the admin panel.
    expect(slack).toContain('Which bot answers')
    expect(slack).not.toContain('Save platform settings')
    expect(readFileSync('src/components/admin/SlackAdminPanel.tsx', 'utf8')).toContain('Shared bot enabled')
    const gmail = readFileSync('src/components/workflow/bots/GmailNotifications.tsx', 'utf8')
    // The accounts list now lives in the Gmail section, described as "Your Google accounts".
    expect(gmail).toContain('Your Google accounts for this project')
    expect(gmail).toContain('<GoogleAccountList')
    expect(gmail).toContain('Ask Builder to set up Gmail')
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
