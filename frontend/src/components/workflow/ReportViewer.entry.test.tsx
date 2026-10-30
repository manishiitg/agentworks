// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'
const mocks = vi.hoisted(() => ({ catalog: vi.fn(), get: vi.fn(), post: vi.fn(), openAsk: vi.fn() }))
vi.mock('../../services/api', () => ({ default: { get: mocks.get, post: mocks.post }, agentApi: {}, workspaceApi: {}, getApiBaseUrl: () => '' }))
vi.mock('./reportDocuments', () => ({ loadReportDocumentCatalog: mocks.catalog, useSelectedReportDocument: (_workspace: string, path?: string) => path || 'db/reports/index.html' }))
vi.mock('../../hooks/useLiveRefetch', () => ({ useLiveRefetch: vi.fn() }))
vi.mock('../../stores/useWorkflowStore', () => ({ useWorkflowStore: (select: any) => select({}) }))
vi.mock('./reportWidgets/useReportChat', () => ({ useReportChat: () => ({ request: vi.fn() }) }))
vi.mock('./reportWidgets/FilePreviewModal', () => ({ FilePreviewModal: () => null }))
vi.mock('./reportWidgets/DashboardQuestionConversation', () => ({ DashboardQuestionConversation: () => null }))
vi.mock('./reportWidgets/ReportQuestionFrame', () => ({ ReportQuestionFrame: ({ title, documentPath }: any) => <div data-document={documentPath}>{title}</div> }))
vi.mock('./ShareDashboardButton', () => ({ ShareDashboardButton: () => null }))
vi.mock('../workspace/WorkspacePanelNavigation', () => ({ useWorkspacePanelChatDraft: () => undefined }))
vi.mock('../../utils/projectAskConversation', () => ({ openProjectAskConversation: mocks.openAsk }))
vi.mock('../../stores/useWorkspaceConnectionStore', async importOriginal => ({ ...await importOriginal<typeof import('../../stores/useWorkspaceConnectionStore')>(), useWorkspaceConnectionStore: (select: any) => select({ activeWorkspaceId: 'local' }) }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (select: any) => select({ user: { id: 'recipient' } }) }))
vi.mock('../ChatArea', () => ({ default: ({ tabId }: { tabId: string }) => <div data-native-tab={tabId}>Existing project history and replies</div> }))
import { ReportView } from './ReportViewer'
import { allowedReportPath } from './reportWidgets/reportMarkdown'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
afterEach(() => vi.resetAllMocks())
async function mount(path?: string) {
 const host = document.createElement('div'); document.body.append(host)
 const root = createRoot(host)
 try {
  await act(async () => root.render(<ReportView workspacePath="Crew/project" documentPath={path} hideToolbar />))
  await vi.waitFor(() => expect(host.textContent).toContain('Actual project dashboard'))
  return host.querySelector('[data-document]')?.getAttribute('data-document')
 } finally { await act(async () => root.unmount()); host.remove() }
}
it('opens the catalog default on a direct dashboard entry without mounting its picker first', async () => {
 mocks.catalog.mockResolvedValue({ documents: [{ path: 'db/reports/managed/overview/index.html' }], defaultPath: 'db/reports/managed/overview/index.html' })
 mocks.post.mockResolvedValue({ data: { resolved_path: 'db/reports/managed/overview/rev1/index.html' } })
 mocks.get.mockResolvedValue({ data: '<html><title>Actual project dashboard</title></html>' })
 expect(await mount()).toBe('Crew/project/db/reports/managed/overview/rev1/index.html')
 expect(mocks.catalog).toHaveBeenCalledWith('Crew/project')
 expect(mocks.get).toHaveBeenCalledWith('/api/workflow/report-preview/file', expect.objectContaining({ params: { workspace: 'Crew/project', path: 'db/reports/managed/overview/rev1/index.html' } }))
 expect(mocks.get.mock.calls.some(([, options]) => options.params?.path === 'db/reports/index.html')).toBe(false)
})
it('keeps an explicit authored document instead of replacing it with a default', async () => {
 mocks.get.mockResolvedValue({ data: '<html><title>Actual project dashboard</title></html>' })
 expect(await mount('db/reports/team.html')).toBe('Crew/project/db/reports/team.html')
 expect(mocks.catalog).not.toHaveBeenCalled()
})

it('names the dashboard header and offers the existing named project documents', async () => {
 mocks.catalog.mockResolvedValue({ documents: [
  { id: 'solar', title: 'Solar Plan', path: 'db/reports/solar.html', order: 0 },
  { id: 'budget', title: 'Budget', path: 'db/reports/budget.html', order: 1 },
 ], defaultPath: 'db/reports/solar.html' })
 mocks.get.mockResolvedValue({ data: '<html><title>Older page title</title></html>' })
 const host = document.createElement('div'); document.body.append(host)
 const root = createRoot(host)
 try {
  await act(async () => root.render(<ReportView workspacePath="Crew/project" />))
  await vi.waitFor(() => expect(host.querySelector('select')?.value).toBe('db/reports/solar.html'))
  expect(host.querySelectorAll('header')).toHaveLength(1)
  expect([...host.querySelectorAll('header option')].map(option => option.textContent)).toEqual(['Solar Plan', 'Budget'])
  expect(host.querySelector('header select')?.getAttribute('title')).toBe('Solar Plan')
  expect(host.querySelector('[data-document]')?.textContent).toBe('Solar Plan')
 } finally { await act(async () => root.unmount()); host.remove() }
})

it('reads project memory through the same bound report endpoint as dashboard files', async () => {
 expect(allowedReportPath('MEMORY.md')).toBe('MEMORY.md')
 expect(allowedReportPath('../MEMORY.md')).toBe('')
 mocks.get.mockResolvedValue({ data: '<html><title>Actual project dashboard</title></html>' })
 expect(await mount('MEMORY.md')).toBe('Crew/project/MEMORY.md')
 expect(mocks.get).toHaveBeenCalledWith('/api/workflow/report-preview/file', expect.objectContaining({ params: { workspace: 'Crew/project', path: 'MEMORY.md' } }))
})


it('opens the recipient chat when a shared Crew has no dashboard, without creating one', async () => {
 mocks.catalog.mockResolvedValue({ documents: [], defaultPath: 'db/reports/index.html' })
 mocks.get.mockResolvedValue({ data: null })
 mocks.openAsk.mockResolvedValue('recipient-project-chat')
 const host = document.createElement('div'); document.body.append(host)
 const root = createRoot(host)
 try {
  await act(async () => root.render(<ReportView workspacePath="Crew/project" dashboardConversation crewConversationPane />))
  await vi.waitFor(() => expect(host.querySelector('[data-native-tab]')?.getAttribute('data-native-tab')).toBe('recipient-project-chat'))
  expect(host.querySelector('header')?.textContent).toContain('Chat')
  expect(mocks.openAsk).toHaveBeenCalledWith({ workspacePath: 'Crew/project' }, { activate: false, userView: true })
  expect(host.textContent).not.toContain('Build your own interactive workspace')
  expect(mocks.post).not.toHaveBeenCalled()
 } finally { await act(async () => root.unmount()); host.remove() }
})
