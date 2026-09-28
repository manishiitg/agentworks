import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it } from 'vitest'
import { WorkspaceViewActions } from './WorkspaceViewActions'

describe('WorkspaceViewActions', () => {
  it('renders walkthrough and refresh in the row while Ask AI lives in the popup', () => {
    const html = renderToStaticMarkup(
      <WorkspaceViewActions
        workspacePath="Workflow/one"
        message="help"
        onRefresh={() => {}}
        walkthrough={<span data-testid="walkthrough" />}
      />,
    )
    expect(html).not.toContain('Ask AI')
    expect(html).toContain('data-testid="walkthrough"')
    expect(html).toContain('aria-label="Refresh view"')
  })
})
