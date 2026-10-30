import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it } from 'vitest'
import { Activity } from 'lucide-react'
import { WorkspaceViewHeader } from './WorkspaceViewHeader'
import { WorkspaceViewActions } from './WorkspaceViewActions'
import { TooltipProvider } from '../ui/tooltip'

it('keeps page actions, title and tabs without Help or walkthroughs', () => {
  const html = renderToStaticMarkup(<WorkspaceViewHeader icon={Activity} title="Integrations" subtitle="Connected apps"
    actions={<button>Settings</button>} below={<p>Summary</p>}
    tabs={{ value: 'apps', onChange: () => {}, options: [{ value: 'apps', label: 'Apps' }], ariaLabel: 'Integrations' }} />)
  for (const text of ['Integrations', 'Connected apps', 'Settings', 'Summary', 'Apps']) expect(html).toContain(text)
  expect(html).not.toContain('Walkthrough')
  expect(html).not.toContain('Help')
})

it('omits automatic Refresh from the standard actions', () => {
  const html = renderToStaticMarkup(<TooltipProvider>
    <WorkspaceViewHeader title="Memory" actions={<WorkspaceViewActions onRefresh={() => {}} />} />
  </TooltipProvider>)
  expect(html).not.toContain('Refresh view')
  expect(html).not.toContain('Walkthrough')
})

it('leaves hidden headers empty, but keeps their explicit actions and tabs', () => {
  expect(renderToStaticMarkup(<WorkspaceViewHeader title="Apps" hideHeader />)).toBe('')
  const html = renderToStaticMarkup(<WorkspaceViewHeader title="People" hideHeader actions={<button>Add person</button>}
    tabs={{ value: 'people', onChange: () => {}, options: [{ value: 'people', label: 'People' }], ariaLabel: 'People tabs' }} />)
  expect(html).not.toContain('<h2')
  expect(html).toContain('Add person')
  expect(html).toContain('People tabs')
})
