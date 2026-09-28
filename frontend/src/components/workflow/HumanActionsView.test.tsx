import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it, vi } from 'vitest'

vi.mock('../../services/api', () => ({ agentApi: { getPulseImpact: vi.fn() }, getApiBaseUrl: () => 'http://127.0.0.1:99999' }))
vi.mock('./ReportHumanInputPanel', () => ({
  ReportHumanInputPanel: ({ workspacePath, showEmptyState }: { workspacePath: string; showEmptyState: boolean }) =>
    <div data-workspace={workspacePath} data-empty-state={showEmptyState}>Decision cards</div>,
}))

import HumanActionsView from './HumanActionsView'

it('gives workflow decisions their own workspace view', () => {
  const html = renderToStaticMarkup(<HumanActionsView workspacePath="Workflow/example" />)
  expect(html).toContain('Human actions')
  expect(html).toContain('data-workspace="Workflow/example"')
  expect(html).toContain('data-empty-state="true"')
  expect(html).toContain('Decision cards')
})
