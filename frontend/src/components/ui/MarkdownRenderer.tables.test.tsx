// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { expect, it, vi } from 'vitest'
import * as clipboard from '../../utils/textUtils'
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

it('lets the message width constrain markdown cells and keeps the original values when copying', async () => {
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
  const host = document.createElement('div')
  const root = createRoot(host)
  const copy = vi.spyOn(clipboard, 'copyToClipboard').mockResolvedValue(true)
  const issue = 'A long issue description that should wrap inside the conversation pane'
  const url = 'https://example.com/' + 'x'.repeat(120)
  const reply = ['| Issue | Outcome |', '| --- | --- |', `| ${issue} | <${url}> |`].join('\n')

  try {
    await act(async () => root.render(<ConversationMarkdownRenderer content={reply} />))
    const table = host.querySelector('table')!
    // The DOM harness does not lay out pixels. Check the width and wrapping
    // contract, including the local scroller for tables that cannot fit.
    expect(table.parentElement?.classList.contains('w-full')).toBe(true)
    expect(table.parentElement?.classList.contains('w-max')).toBe(false)
    expect(table.parentElement?.parentElement?.classList.contains('overflow-x-auto')).toBe(true)
    expect(table.classList.contains('whitespace-normal')).toBe(true)
    expect(table.classList.contains('[&_:is(th,td,a)]:[overflow-wrap:anywhere]')).toBe(true)
    const button = host.querySelector<HTMLButtonElement>('button[aria-label="Copy table"]')!
    expect(button.parentElement).toBe(table.parentElement)
    await act(async () => button.click())
    expect(copy).toHaveBeenCalledWith(`Issue\tOutcome\n${issue}\t${url}`)
  } finally {
    await act(async () => root.unmount())
    copy.mockRestore()
    vi.unstubAllGlobals()
  }
})
