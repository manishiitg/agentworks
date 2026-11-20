import { beforeEach, expect, it, vi } from 'vitest'

vi.mock('../components/files/readPreviewPage', () => ({
  isPagedTextFile: () => true,
  readPreviewPage: vi.fn(async () => ({ text: 'first page', offset: 0, next: 10, total: 600_000 })),
}))
vi.mock('../services/api', () => ({
  agentApi: { getPlannerFileContent: vi.fn(async () => ({ success: true, data: { content: 'whole file' } })) },
  workspaceApi: {},
}))

import { openWorkspaceFile } from './openWorkspaceFile'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'

beforeEach(() => { useWorkspaceStore.getState().setFileContent('') })

// Real failure: a 650 KB Go file opened blank, because the code editor got the empty paged-preview text.
it('loads a large source file whole and pages a large log', async () => {
  await openWorkspaceFile('project/app/server.go')
  expect(useWorkspaceStore.getState().fileContent).toBe('whole file')
  expect(useWorkspaceStore.getState().selectedFile?.previewPage).toBeUndefined()
  await openWorkspaceFile('project/logs/run.log')
  expect(useWorkspaceStore.getState().selectedFile?.previewPage?.total).toBe(600_000)
})
