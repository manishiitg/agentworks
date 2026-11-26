import { expect, it, vi } from 'vitest'

const sizes: Record<string, number> = { 'project/app/server.go': 600_000, 'project/logs/run.log': 600_000, 'project/logs/huge.log': 50_000_000 }
vi.mock('../components/files/readPreviewPage', () => ({
  isPagedTextFile: () => true,
  readPreviewPage: vi.fn(async (path: string, offset = 0) => ({ text: `page at ${offset}`, offset, next: offset + 10, total: sizes[path] })),
  readLineOffset: vi.fn(async () => 4_000_000),
}))
vi.mock('../services/api', () => ({
  agentApi: { getPlannerFileContent: vi.fn(async () => ({ success: true, data: { content: 'whole file' } })) },
  workspaceApi: {},
}))

import { openWorkspaceFile } from './openWorkspaceFile'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'

const state = () => useWorkspaceStore.getState()

// Real failures: a 650 KB Go file opened blank (the code editor got the empty
// paged text), and opening a huge file at a line must never read it whole.
it('loads large source whole, pages large logs, and pages a huge file from the line', async () => {
  await openWorkspaceFile('project/app/server.go')
  expect(state().fileContent).toBe('whole file')
  expect(state().selectedFile?.previewPage).toBeUndefined()

  await openWorkspaceFile('project/logs/run.log')
  expect(state().selectedFile?.previewPage?.offset).toBe(0)

  state().setFileContent('')
  await openWorkspaceFile('project/logs/huge.log', 90_000)
  expect(state().fileContent).toBe('')
  expect(state().selectedFile?.previewPage).toMatchObject({ offset: 4_000_000, startLine: 90_000 })
})
