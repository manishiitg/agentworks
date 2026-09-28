import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { Activity } from 'lucide-react'
import { AskAIButton } from './AskAIButton'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspaceViewActions } from './WorkspaceViewActions'
import { WorkspaceViewIconButton } from './WorkspaceViewIconButton'
import { TooltipProvider } from '../ui/tooltip'

describe('WorkspaceViewHeader', () => {
  it('lays out icon, title, context, subtitle, actions, and below in order', () => {
    const html = renderToStaticMarkup(
      <WorkspaceViewHeader
        icon={<span data-testid="icon" />}
        title="Costs"
        context={<span data-testid="context" />}
        subtitle="Where the money goes"
        actions={<><button type="button" data-testid="ask-ai">Ask AI</button><button type="button" data-testid="refresh" aria-label="Refresh costs">Refresh</button></>}
        below={<div data-testid="below" />}
      />,
    )
    const order = ['data-testid="icon"', '>Costs<', 'data-testid="context"', 'Where the money goes', 'data-testid="ask-ai"', 'Walkthrough: Costs', 'data-testid="refresh"', 'data-testid="below"']
      .map(token => html.indexOf(token))
    for (const index of order) expect(index).toBeGreaterThanOrEqual(0)
    expect([...order].sort((a, b) => a - b)).toEqual(order)
    expect(html).toContain('<header')
    expect(html).toContain('<h2')
  })

  it('renders the active tab actions left of the base actions', () => {
    const render = (value: 'apps' | 'skills') => renderToStaticMarkup(
      <WorkspaceViewHeader
        title="Integrations"
        actions={<button type="button" data-testid="ask-ai">Ask AI</button>}
        tabs={{ value, onChange: () => {}, options: [{ value: 'apps', label: 'Apps' }, { value: 'skills', label: 'Skills' }], ariaLabel: 'Integrations' }}
        tabActions={{ apps: <button type="button" data-testid="tab-action">Apps action</button> }}
      />,
    )
    const html = render('apps')
    const tabActionIndex = html.indexOf('data-testid="tab-action"')
    const askIndex = html.indexOf('data-testid="ask-ai"')
    expect(tabActionIndex).toBeGreaterThanOrEqual(0)
    expect(tabActionIndex).toBeLessThan(askIndex)
    expect(render('skills')).not.toContain('data-testid="tab-action"')
  })

  it('moves the shared Ask AI action into the walkthrough popup, keeping Refresh rightmost', () => {
    const html = renderToStaticMarkup(
      <TooltipProvider>
        <WorkspaceViewHeader title="Memory" actions={<WorkspaceViewActions workspacePath="Crew/example" message="Explain memory" onRefresh={() => {}} />} />
      </TooltipProvider>,
    )
    expect(html).not.toContain('aria-label="Ask AI"')
    const walkthrough = html.indexOf('aria-label="Walkthrough: Memory"')
    const refresh = html.indexOf('aria-label="Refresh view"')
    expect(walkthrough).toBeGreaterThanOrEqual(0)
    expect(walkthrough).toBeLessThan(refresh)
  })

  it('moves an icon-only Ask AI fragment child into the popup but keeps labeled ones inline', () => {
    const html = renderToStaticMarkup(
      <TooltipProvider>
        <WorkspaceViewHeader
          title="Notifications"
          actions={<>
            <AskAIButton workspacePath="Workflow/one" message="/notify" label="Change" />
            <AskAIButton workspacePath="Workflow/one" message="Explain notifications" iconOnly />
            <WorkspaceViewIconButton label="Refresh notifications" onClick={() => {}} />
          </>}
        />
      </TooltipProvider>,
    )
    expect(html).toContain('>Change<')
    expect(html).not.toContain('aria-label="Ask AI"')
    const walkthrough = html.indexOf('aria-label="Walkthrough: Notifications"')
    const refresh = html.indexOf('aria-label="Refresh notifications"')
    expect(walkthrough).toBeGreaterThanOrEqual(0)
    expect(walkthrough).toBeLessThan(refresh)
  })

  it('renders an icon reference in the standard h-9 tile', () => {
    const html = renderToStaticMarkup(<WorkspaceViewHeader icon={Activity} title="Pulse" />)
    expect(html).toContain('h-9 w-9')
    expect(html).toContain('border-primary/25')
  })

  it('renders sticky and bare variants', () => {
    const sticky = renderToStaticMarkup(<WorkspaceViewHeader title="Webhooks" sticky />)
    expect(sticky).toContain('sticky top-0')
    const bare = renderToStaticMarkup(<WorkspaceViewHeader title="Costs" bare />)
    expect(bare).not.toContain('<header')
    expect(bare).toContain('>Costs<')
  })

  it('lets a nested toolbar own the walkthrough without duplicating it', () => {
    const html = renderToStaticMarkup(<WorkspaceViewHeader title="Browser" showWalkthrough={false} actions={<button type="button">Toolbar actions</button>} />)
    expect(html).not.toContain('Walkthrough: Browser')
  })
})
