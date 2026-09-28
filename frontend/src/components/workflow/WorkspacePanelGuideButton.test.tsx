// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspacePanelGuideContext } from './WorkspacePanelGuideContext'
import { WorkspaceViewActions } from './WorkspaceViewActions'
import { BrowserWorkspacePanel } from './BrowserWorkspacePanel'
import { TooltipProvider } from '../ui/tooltip'
import { getWorkspacePanelGuide } from './workspacePanelGuides'
import { IntegrationHowToGuide } from './IntegrationHowToGuide'
import { INTEGRATION_HOW_TO_TOPICS } from './integrationHowToTopics'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('Panel walkthroughs', () => {
  it('adds an explanation beside Memory actions and closes with Escape', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(
        <WorkspaceViewHeader
          title="Memory"
          actions={<><button type="button" data-testid="ask" aria-label="Ask AI">Ask AI</button><button type="button" data-testid="refresh" aria-label="Refresh memory">Refresh</button></>}
        />,
      ))
      const ask = host.querySelector('[data-testid="ask"]')!
      const refresh = host.querySelector('[data-testid="refresh"]')!
      const guideButton = host.querySelector('[aria-label="Walkthrough: Memory"]') as HTMLButtonElement
      expect(guideButton).not.toBeNull()
      expect(ask.compareDocumentPosition(refresh) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
      expect(ask.compareDocumentPosition(guideButton) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
      expect(guideButton.compareDocumentPosition(refresh) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()

      await act(async () => guideButton.click())
      const dialog = host.querySelector('[role="dialog"]')!
      expect(dialog.getAttribute('aria-label')).toBe('Memory walkthrough')
      expect(dialog.textContent).toContain('durable project context')
      expect(dialog.textContent).toContain('How to use it')
      expect(guideButton.getAttribute('aria-expanded')).toBe('true')

      await act(async () => document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true })))
      expect(host.querySelector('[role="dialog"]')).toBeNull()
      expect(document.activeElement).toBe(guideButton)
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('provides specific guidance for different panels', () => {
    expect(getWorkspacePanelGuide('Automation').purpose).toContain('work starts')
    expect(getWorkspacePanelGuide('Plan').howTo).toContain('flow')
    expect(getWorkspacePanelGuide('Dashboard').purpose).toContain('results')
    expect(getWorkspacePanelGuide('Schedules for Sales').purpose).toContain('automatically')
  })

  it('separates Crew and AgentWorks guidance for the same panel', async () => {
    const crew = getWorkspacePanelGuide('Automation', 'crew')
    const workflow = getWorkspacePanelGuide('Automation', 'agentworks')
    expect(crew.purpose).toContain('Crew member')
    expect(workflow.purpose).not.toContain('Crew member')
    expect(getWorkspacePanelGuide('Memory', 'crew').group).toBe('Main toolbar')
    expect(getWorkspacePanelGuide('Files', 'crew').group).toBe('Ops')
    expect(getWorkspacePanelGuide('Identity', 'crew').group).toBe('Setup')
    expect(getWorkspacePanelGuide('Plan').group).toBe('Main toolbar')
    expect(getWorkspacePanelGuide('Knowledge').group).toBe('Ops')
    expect(getWorkspacePanelGuide('Workflow playbooks').group).toBe('Setup')

    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(
        <WorkspacePanelGuideContext.Provider value="crew">
          <WorkspaceViewHeader title="Automation" />
        </WorkspacePanelGuideContext.Provider>,
      ))
      await act(async () => (host.querySelector('[aria-label="Walkthrough: Automation"]') as HTMLButtonElement).click())
      const dialog = host.querySelector('[role="dialog"]')!
      expect(dialog.textContent).toContain('Crew · Main toolbar')
      expect(dialog.textContent).toContain('Crew member')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('explains the active tab inside a shared panel header', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(
        <WorkspacePanelGuideContext.Provider value="crew">
          <WorkspaceViewHeader title="Automation" helpTopic="Automation · Chats" />
        </WorkspacePanelGuideContext.Provider>,
      ))
      await act(async () => (host.querySelector('[aria-label="Walkthrough: Automation · Chats"]') as HTMLButtonElement).click())
      expect(host.querySelector('[role="dialog"]')?.textContent).toContain('earlier conversations')

      await act(async () => root.render(
        <WorkspacePanelGuideContext.Provider value="crew">
          <WorkspaceViewHeader title="Automation" helpTopic="Automation · Schedules" />
        </WorkspacePanelGuideContext.Provider>,
      ))
      expect(host.querySelector('[role="dialog"]')?.textContent).toContain('saved instruction')
      expect(host.querySelector('[role="dialog"]')?.textContent).toContain('Crew · Main toolbar')
      expect(getWorkspacePanelGuide('Knowledge · Database').group).toBe('Ops')
      expect(getWorkspacePanelGuide('Identity · Secrets', 'crew').group).toBe('Setup')
      expect(getWorkspacePanelGuide('Pulse · Platform health').group).toBe('Main toolbar')
      expect(getWorkspacePanelGuide('Pulse · Issue Fix').purpose).toContain('tried to change')
      expect(getWorkspacePanelGuide('Pulse · Issue Verification').purpose).toContain('checks')
      expect(getWorkspacePanelGuide('Pulse · Issue Activity').purpose).toContain('history')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('gives complex integration tabs concrete, surface-aware setup steps', async () => {
    const topics = ['MCPs', 'Skills', 'Slack', 'WhatsApp', 'Gmail', 'Connect']
    for (const surface of ['crew', 'agentworks'] as const) {
      for (const topic of topics) {
        const guide = getWorkspacePanelGuide(`Integrations · ${topic}`, surface)
        expect(guide.group).toBe('Setup')
        expect(guide.steps?.length).toBeGreaterThanOrEqual(3)
      }
    }
    expect(getWorkspacePanelGuide('Integrations · MCPs', 'crew').howTo).toContain('project')
    expect(getWorkspacePanelGuide('Integrations · MCPs', 'agentworks').howTo).toContain('workflow')
    expect(getWorkspacePanelGuide('Integrations · Slack', 'crew').steps?.join(' ')).toContain('channel’s ID')
    expect(getWorkspacePanelGuide('Integrations · WhatsApp').steps?.join(' ')).toContain('@slug')
    expect(getWorkspacePanelGuide('Integrations · Gmail').howTo).toContain('Open a question below')
    expect(getWorkspacePanelGuide('Integrations · Gmail').steps?.join(' ')).toContain('Reconnect')
    expect(getWorkspacePanelGuide('Integrations · Connect').howTo).toContain('MCPs tab')

    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkspaceViewHeader title="Integrations" helpTopic="Integrations · Skills" />))
      await act(async () => (host.querySelector('[aria-label="Walkthrough: Integrations · Skills"]') as HTMLButtonElement).click())
      const steps = host.querySelectorAll('[aria-label="Setup steps"] li')
      expect(steps).toHaveLength(3)
      expect(steps[0]?.textContent).toContain('selected skills')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('shows Gmail how-to answers only when its question-mark walkthrough opens', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(
        <WorkspacePanelGuideContext.Provider value="crew">
          <WorkspaceViewHeader title="Integrations" helpTopic="Integrations · Gmail" />
        </WorkspacePanelGuideContext.Provider>,
      ))
      const button = host.querySelector('[aria-label="Walkthrough: Integrations · Gmail"]') as HTMLButtonElement
      expect(button).not.toBeNull()
      expect(host.querySelector('[aria-label="Gmail how-to answers"]')).toBeNull()

      await act(async () => button.click())
      const dialog = host.querySelector('[role="dialog"]')!
      expect(dialog.className).toContain('w-[min(40rem,calc(100vw-1.5rem))]')
      expect(dialog.className).toContain('max-h-[min(42rem,calc(100vh-5rem))]')
      expect(dialog.querySelector('[aria-label="Gmail how-to answers"]')).not.toBeNull()
      expect(dialog.querySelectorAll('details')).toHaveLength(10)
      expect(dialog.textContent).toContain('Crew conversation')

      await act(async () => button.click())
      expect(host.querySelector('[aria-label="Gmail how-to answers"]')).toBeNull()
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it.each(INTEGRATION_HOW_TO_TOPICS)('shows %s task answers in the larger walkthrough bubble', async topic => {
    const expectedAnswer = {
      MCPs: 'Connect a new app',
      Skills: 'GitHub URL',
      Slack: 'xapp-',
      WhatsApp: 'Linked Devices',
      Gmail: 'Add & sign in',
      Connect: 'Remote MCP URL',
    }[topic]
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<WorkspaceViewHeader title="Integrations" helpTopic={`Integrations · ${topic}`} />))
      const button = host.querySelector(`[aria-label="Walkthrough: Integrations · ${topic}"]`) as HTMLButtonElement
      expect(host.querySelector(`[aria-label="${topic} how-to answers"]`)).toBeNull()

      await act(async () => button.click())
      const dialog = host.querySelector('[role="dialog"]')!
      expect(dialog.className).toContain('w-[min(40rem,calc(100vw-1.5rem))]')
      expect(dialog.querySelector(`[aria-label="${topic} how-to answers"]`)).not.toBeNull()
      expect(dialog.querySelectorAll('details').length).toBeGreaterThanOrEqual(6)
      expect(dialog.textContent).toContain(expectedAnswer)

      await act(async () => button.click())
      expect(host.querySelector(`[aria-label="${topic} how-to answers"]`)).toBeNull()
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })

  it('keeps project and workflow how-to answers specific to their controls', () => {
    const projectSkills = renderToStaticMarkup(<IntegrationHowToGuide topic="Skills" scopeNoun="project" />)
    const workflowSkills = renderToStaticMarkup(<IntegrationHowToGuide topic="Skills" scopeNoun="workflow" />)
    expect(projectSkills).toContain('Skills for this project')
    expect(workflowSkills).toContain('Builder opens in chat')
    expect(renderToStaticMarkup(<IntegrationHowToGuide topic="Slack" scopeNoun="project" />)).toContain('this project')
    expect(renderToStaticMarkup(<IntegrationHowToGuide topic="WhatsApp" scopeNoun="workflow" />)).toContain('Workflow routes')
    expect(renderToStaticMarkup(<IntegrationHowToGuide topic="Connect" scopeNoun="workflow" />)).toContain('Remote MCP URL')
  })

  it('places Browser help between Ask AI and Refresh exactly once', () => {
    const html = renderToStaticMarkup(
      <WorkspacePanelGuideContext.Provider value="crew">
        <TooltipProvider>
          <BrowserWorkspacePanel
            workspacePath={null}
            browserMode="auto"
            onBrowserModeChange={() => {}}
            cdpPort={9222}
            onCdpPortChange={() => {}}
            cdpConnected={null}
            cdpError={null}
            cdpChecking={false}
            onCheckCdpConnection={() => {}}
            assistantControl={<WorkspaceViewActions workspacePath={null} message="Browser help" onRefresh={() => {}} />}
          />
        </TooltipProvider>
      </WorkspacePanelGuideContext.Provider>,
    )
    expect(html).not.toContain('aria-label="Ask AI"')
    const walkthrough = html.indexOf('aria-label="Walkthrough: Browser"')
    const refresh = html.indexOf('aria-label="Refresh view"')
    expect(walkthrough).toBeGreaterThanOrEqual(0)
    expect(walkthrough).toBeLessThan(refresh)
    expect(html.match(/aria-label="Walkthrough: Browser"/g)).toHaveLength(1)
  })

  it('carries Ask AI inside the popup instead of the header row', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(
        <TooltipProvider>
          <WorkspaceViewHeader title="Memory" actions={<WorkspaceViewActions workspacePath="Crew/example" message="Explain memory" onRefresh={() => {}} />} />
        </TooltipProvider>,
      ))
      expect(host.querySelector('[aria-label="Ask AI"]')).toBeNull()
      const guideButton = host.querySelector('[aria-label="Walkthrough: Memory"]') as HTMLButtonElement
      expect(guideButton).not.toBeNull()

      await act(async () => guideButton.click())
      const dialog = host.querySelector('[role="dialog"]')!
      const ask = Array.from(dialog.querySelectorAll('button')).find(button => button.textContent === 'Ask AI') as HTMLButtonElement
      expect(ask).not.toBeUndefined()

      await act(async () => ask.click())
      expect(ask.textContent).toContain('Sure?')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })
})
