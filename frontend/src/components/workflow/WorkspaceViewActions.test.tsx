import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { WorkspaceViewActions } from './WorkspaceViewActions'
import { TooltipProvider } from '../ui/tooltip'

describe('WorkspaceViewActions', () => {
  it('shows an explicit refresh without a walkthrough', () => {
    const html = renderToStaticMarkup(
      <TooltipProvider><WorkspaceViewActions onRefresh={() => {}} /></TooltipProvider>,
    )
    expect(html).not.toContain('walkthrough')
    expect(html).toContain('aria-label="Refresh view"')
  })
})
