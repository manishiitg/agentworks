import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { WorkspaceViewActions } from './WorkspaceViewActions'
import { TooltipProvider } from '../ui/tooltip'

describe('WorkspaceViewActions', () => {
  it('retains standalone Ask AI and explicit refresh without a walkthrough', () => {
    const html = renderToStaticMarkup(
      <TooltipProvider><WorkspaceViewActions
        workspacePath="Workflow/one"
        message="help"
        onRefresh={() => {}}
      /></TooltipProvider>,
    )
    expect(html).toContain('Ask AI')
    expect(html).not.toContain('walkthrough')
    expect(html).toContain('aria-label="Refresh view"')
  })
})
