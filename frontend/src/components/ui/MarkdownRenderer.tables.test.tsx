// @vitest-environment happy-dom
import React from 'react'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it, vi } from 'vitest'
import { ConversationMarkdownRenderer } from './MarkdownRenderer'

vi.mock('../../services/api', () => ({
  getApiBaseUrl: () => 'http://example.com', getAuthToken: () => 'app-session',
  workspaceApi: {}, agentApi: {},
}))

// A table inside a numbered list showed as raw pipes: the table tidy-up
// rewrote its separator line without the list item's indent.
it('renders a table nested in a numbered list item', () => {
  const reply = '1. **Steps**:\n\n   | Tool | Line |\n   | --- | --- |\n   | Read | Read a file |\n\n2. Next'
  const html = renderToStaticMarkup(<ConversationMarkdownRenderer content={reply} />)
  expect(html).toContain('<table')
  expect(html).not.toContain('| --- |')
})
