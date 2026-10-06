// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const fixture = vi.hoisted(() => ({
  source: 'escaped = r"a\\nb\\tc\\rd"\ntext = """first\n\n\n    \nlast"""\n    \n',
  content: '',
}))
vi.mock('../services/api', () => ({ agentApi: { getPlannerFileContent: async () => ({ success: true, data: { content: fixture.source } }) }, workspaceApi: {} }))
vi.mock('../stores/useWorkspaceStore', () => ({ useWorkspaceStore: { getState: () => ({ setLoadingFileContent: vi.fn(), setSelectedFile: vi.fn(), setBinaryFileData: vi.fn(), setShowFileContent: vi.fn(), setError: vi.fn(), setFileContent: (content: string) => { fixture.content = content } }) } }))
vi.mock('../hooks/useTheme', () => ({ useTheme: () => ({ theme: 'dark' }) }))
vi.mock('@monaco-editor/react', () => ({ default: ({ value }: { value: string }) => <pre>{value}</pre> }))

import { openWorkspaceFile } from './openWorkspaceFile'
import { FileEditor } from '../components/workspace/FileEditor'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('loads and displays Python without decoding literal escapes or reformatting multiline string whitespace', async () => {
  await openWorkspaceFile('Workflow/python-relay/relay.py')
  expect(fixture.content).toBe(fixture.source)
  const host = document.createElement('div')
  const root = createRoot(host)
  try {
    await act(async () => root.render(<FileEditor filepath="Workflow/python-relay/relay.py" value={fixture.content} />))
    expect(host.querySelector('pre')?.textContent).toBe(fixture.source)
  } finally {
    await act(async () => root.unmount())
  }
})
