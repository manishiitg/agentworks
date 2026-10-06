// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { MarkdownRenderer } from './MarkdownRenderer'

const workspaceState = {
  highlightFile: vi.fn(),
  expandFoldersForFile: vi.fn(),
  setSelectedFile: vi.fn(),
  setShowFileContent: vi.fn(),
  setFileContent: vi.fn(),
  setLoadingFileContent: vi.fn(),
  setBinaryFileData: vi.fn(),
}

vi.mock('../../stores/useWorkspaceStore', () => ({
  useWorkspaceStore: (selector: (state: typeof workspaceState) => unknown) => selector(workspaceState),
}))
vi.mock('../../stores/useAppStore', () => ({
  useAppStore: (selector: (state: { setWorkspaceMinimized: () => void }) => unknown) => selector({ setWorkspaceMinimized: vi.fn() }),
}))
vi.mock('../../stores/useModeStore', () => ({
  useModeStore: { getState: () => ({ selectedModeCategory: null }) },
}))
vi.mock('../../stores/useWorkflowStore', () => ({
  useWorkflowStore: { getState: () => ({ openWorkspaceView: vi.fn() }) },
}))
vi.mock('../../stores/useProductSurfaceStore', () => ({
  useProductSurfaceStore: { getState: () => ({ productSurface: 'agentworks' }) },
}))
vi.mock('../../stores/useGlobalPresetStore', () => ({
  useGlobalPresetStore: { getState: () => ({ getActivePreset: vi.fn() }) },
}))
vi.mock('../../services/api', () => ({
  workspaceApi: { get: vi.fn() },
  agentApi: { getPlannerFileContent: vi.fn() },
  getApiBaseUrl: () => '',
}))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

it('uses the view resolver and click handler for relative workspace links', async () => {
  const resolve = vi.fn(() => ({
    filepath: 'Workflow/demo/learnings/references/guide.md',
    displayPath: 'references/guide.md',
  }))
  const open = vi.fn(() => true)
  const container = document.createElement('div')
  const root = createRoot(container)

  try {
    await act(async () => root.render(
      <MarkdownRenderer
        content="[Guide](../references/guide.md)"
        basePath="Workflow/demo/learnings/_global/SKILL.md"
        onWorkspaceLinkResolve={resolve}
        onWorkspaceLinkClick={open}
      />,
    ))

    const anchor = container.querySelector<HTMLAnchorElement>('a')
    expect(anchor?.getAttribute('href')).toBe('#workspace/references%2Fguide.md')
    await act(async () => anchor?.click())

    expect(resolve).toHaveBeenCalledWith(
      'Workflow/demo/learnings/references/guide.md',
      'Workflow/demo/learnings/references/guide.md',
    )
    expect(open).toHaveBeenCalledWith(
      'Workflow/demo/learnings/references/guide.md',
      'references/guide.md',
    )
  } finally {
    await act(async () => root.unmount())
  }
})

it('leaves external links outside the workspace-link callbacks', async () => {
  const resolve = vi.fn()
  const open = vi.fn()
  const container = document.createElement('div')
  const root = createRoot(container)

  try {
    await act(async () => root.render(
      <MarkdownRenderer
        content="[Docs](https://example.com/docs)"
        basePath="Workflow/demo/learnings/_global/SKILL.md"
        onWorkspaceLinkResolve={resolve}
        onWorkspaceLinkClick={open}
      />,
    ))

    const anchor = container.querySelector<HTMLAnchorElement>('a')
    expect(anchor?.href).toBe('https://example.com/docs')
    expect(anchor?.target).toBe('_blank')
    expect(resolve).not.toHaveBeenCalled()
    expect(open).not.toHaveBeenCalled()
  } finally {
    await act(async () => root.unmount())
  }
})

it('opens an absolute disk path under workspace-docs as a workspace file', async () => {
  const resolve = vi.fn((filepath: string, displayPath: string) => ({ filepath, displayPath }))
  const open = vi.fn(() => true)
  const container = document.createElement('div')
  const root = createRoot(container)

  try {
    await act(async () => root.render(
      <MarkdownRenderer
        content="[View test receipt](/Users/me/ai-work/mcp-agent-builder-go/workspace-docs/Workflow/upwork/runs/test-1/test_mode.json)"
        onWorkspaceLinkResolve={resolve}
        onWorkspaceLinkClick={open}
      />,
    ))

    const anchor = container.querySelector<HTMLAnchorElement>('a')
    expect(anchor).not.toBeNull()
    await act(async () => anchor?.click())
    expect(open).toHaveBeenCalledWith(
      'Workflow/upwork/runs/test-1/test_mode.json',
      expect.any(String),
    )
  } finally {
    await act(async () => root.unmount())
  }
})
