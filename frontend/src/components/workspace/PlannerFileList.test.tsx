// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import type { PlannerFile } from '../../services/api-types'
import { TooltipProvider } from '../ui/tooltip'
import PlannerFileList from './PlannerFileList'
import { useWorkspaceGitStore } from '../../stores/useWorkspaceGitStore'

vi.mock('../../stores/useWorkspaceStore', () => ({
  useWorkspaceStore: Object.assign(
    (selector: (state: { scrollToFile: () => void }) => unknown) => selector({ scrollToFile: () => undefined }),
    { getState: () => ({ scrollToFile: () => undefined }) },
  ),
}))
vi.mock('../../services/workspaceGit', () => ({ workspaceGitApi: {} }))
vi.mock('../../stores/useAuthStore', () => ({
  useAuthStore: { getState: () => ({ user: { id: 'test-user' } }) },
}))
import {
  flattenVisiblePlannerFiles,
  WORKSPACE_SCROLL_TO_FILE_EVENT,
} from '../../utils/plannerFileTree'

const file = (filepath: string, type: 'file' | 'folder' = 'file', children?: PlannerFile[]): PlannerFile => ({
  filepath,
  type,
  children,
})

describe('flattenVisiblePlannerFiles', () => {
  it('uses a stable event name for virtual row navigation', () => {
    expect(WORKSPACE_SCROLL_TO_FILE_EVENT).toBe('workspace-scroll-to-file')
  })

  it('sorts folders before files without mutating backend arrays', () => {
    const children = [file('root/z.txt'), file('root/a', 'folder')]
    const files = [file('z.txt'), file('root', 'folder', children), file('a.txt')]

    const rows = flattenVisiblePlannerFiles(files, new Set(['root']), false)

    expect(rows.map(row => [row.file.filepath, row.depth])).toEqual([
      ['root', 0],
      ['root/a', 1],
      ['root/z.txt', 1],
      ['a.txt', 0],
      ['z.txt', 0],
    ])
    expect(files.map(item => item.filepath)).toEqual(['z.txt', 'root', 'a.txt'])
    expect(children.map(item => item.filepath)).toEqual(['root/z.txt', 'root/a'])
  })

  it('only includes descendants of expanded folders', () => {
    const files = [file('root', 'folder', [file('root/child.txt')])]

    expect(flattenVisiblePlannerFiles(files, new Set(), false)).toHaveLength(1)
    expect(flattenVisiblePlannerFiles(files, new Set(), true)).toHaveLength(2)
  })
})

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('PlannerFileList Work controls', () => {
  it('keeps the project root and identity metadata out of bulk selection', async () => {
    const toggle = vi.fn()
    const files = [file('project', 'folder', [file('project/app.ts'), file('project/product.json')])]
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<TooltipProvider><PlannerFileList
        files={files} loading={false} error={null}
        onFolderClick={() => undefined} onFileClick={() => undefined}
        onFileDelete={() => undefined} onFolderDelete={() => undefined}
        onRetry={() => undefined} expandedFolders={new Set(['project'])}
        chatFileContext={[]} addFileToContext={() => undefined}
        hideRootActions protectedRootPath="project" isSelectionMode onToggleFileSelection={toggle}
      /></TooltipProvider>))
      expect((host.querySelector('[aria-label="Select project"]') as HTMLInputElement).disabled).toBe(true)
      expect((host.querySelector('[aria-label="Select product.json"]') as HTMLInputElement).disabled).toBe(true)
      await act(async () => (host.querySelector('[data-filepath="project"]') as HTMLElement).click())
      expect(toggle).not.toHaveBeenCalled()
      await act(async () => (host.querySelector('[aria-label="Select app.ts"]') as HTMLInputElement).click())
      expect(toggle).toHaveBeenCalledWith(files[0].children![0])
    } finally {
      await act(async () => root.unmount()); host.remove()
    }
  })
  it('can hide send-to-chat everywhere and actions on the project root only', async () => {
    const files = [file('my-project', 'folder', [file('my-project/frontend', 'folder')])]
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(
        <TooltipProvider>
          <PlannerFileList
            files={files}
            loading={false}
            error={null}
            onFolderClick={() => undefined}
            onFileClick={() => undefined}
            onFileDelete={() => undefined}
            onFolderDelete={() => undefined}
            onRetry={() => undefined}
            expandedFolders={new Set(['my-project'])}
            chatFileContext={[]}
            addFileToContext={() => undefined}
            onCreateFolder={() => undefined}
            hideAddToChat
            hideRootActions
          />
        </TooltipProvider>,
      ))

      const actionButtons = Array.from(host.querySelectorAll('button[aria-label^="More actions for"]'))
      expect(actionButtons).toHaveLength(1)
      expect(actionButtons[0].getAttribute('aria-label')).toBe('More actions for frontend')
      expect(host.querySelector('[aria-label="More actions for my-project"]')).toBeNull()
      expect(host.querySelector('[aria-label^="Send "]')).toBeNull()
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })
})

describe('PlannerFileList git marks', () => {
  it('shows a status letter on changed files and a dot on folders that contain changes', async () => {
    useWorkspaceGitStore.setState({
      fileStatus: new Map([['app/a.ts', { status: 'modified' }], ['app/new.ts', { status: 'untracked' }]]),
      changedDirs: new Map([['app', 'modified' as const]]),
    })
    const files = [file('app', 'folder', [file('app/a.ts'), file('app/clean.ts'), file('app/new.ts')])]
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(
        <TooltipProvider>
          <PlannerFileList
            files={files}
            loading={false}
            error={null}
            onFolderClick={() => undefined}
            onFileClick={() => undefined}
            onFileDelete={() => undefined}
            onFolderDelete={() => undefined}
            onRetry={() => undefined}
            expandedFolders={new Set(['app'])}
            chatFileContext={[]}
            addFileToContext={() => undefined}
            hideAddToChat
          />
        </TooltipProvider>,
      ))
      const rowFor = (path: string) => host.querySelector(`[data-filepath="${path}"]`) as HTMLElement
      expect(rowFor('app/a.ts').querySelector('[title="Modified"]')?.textContent).toBe('M')
      expect(rowFor('app/new.ts').querySelector('[title="Untracked"]')?.textContent).toBe('U')
      expect(rowFor('app/clean.ts').querySelector('[title="Modified"], [title="Untracked"]')).toBeNull()
      expect(rowFor('app').querySelector('[aria-label="Contains changes"]')).not.toBeNull()
    } finally {
      await act(async () => root.unmount())
      host.remove()
      useWorkspaceGitStore.getState().clear()
    }
  })
})


describe('PlannerFileList highlights', () => {
  it('highlights only an explicit matching path, including trees without original paths', async () => {
    const files = [file('Scratch', 'folder', [file('Scratch/hello.md')]), file('Testlab', 'folder')]
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host)
    const render = async (highlightedFile?: string) => {
      await act(async () => root.render(<TooltipProvider><PlannerFileList files={files} loading={false} error={null}
        onFolderClick={() => {}} onFileClick={() => {}} onFileDelete={() => {}} onFolderDelete={() => {}} onRetry={() => {}}
        expandedFolders={new Set(['Scratch'])} chatFileContext={[]} addFileToContext={() => {}} hideAddToChat highlightedFile={highlightedFile} /> </TooltipProvider>))
    }
    try {
      await render()
      expect(host.querySelectorAll('[data-highlighted="true"]')).toHaveLength(0)
      await render('Scratch/hello.md')
      expect(host.querySelectorAll('[data-highlighted="true"]')).toHaveLength(1)
      expect(host.querySelector('[data-highlighted="true"]')?.getAttribute('data-filepath')).toBe('Scratch/hello.md')
    } finally { await act(async () => root.unmount()); host.remove() }
  })
})
