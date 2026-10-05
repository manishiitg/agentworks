import { useEffect, useRef, useState, type KeyboardEvent, type PointerEvent, type ReactNode } from 'react'
import type { ReadOnlyFileWorkspaceSource } from './workspace/fileWorkspaceSource'
import { ReadOnlyFileTree } from './workspace/ReadOnlyFileTree'
import { FileContentViewerBody } from './FileContentViewer'
import Workspace from './Workspace'
import { useWorkspaceStore } from '../stores/useWorkspaceStore'
import { FileGitContext, useFileGit, useFileGitStore } from './workspace/FileGitContext'
import { ActivityRail, GitBar, GitChangesList, GitFilePanel } from './workspace/GitPanels'
import { EXPAND_FIRST_LEVEL_FOLDERS_BY_DEFAULT } from '../utils/workspacePathUtils'

// Side by side (tree left, file right) once the pane is wide enough; a narrow
// pane keeps the single-pane swap.
const SPLIT_MIN_WIDTH = 720
const TREE_WIDTH_KEY = 'agentworks.files.treeWidth'
const TREE_MIN = 180
const TREE_MAX = 520

function readTreeWidth(): number {
  try {
    const stored = Number(window.localStorage.getItem(TREE_WIDTH_KEY))
    if (Number.isFinite(stored) && stored >= TREE_MIN && stored <= TREE_MAX) return stored
  } catch { /* optional */ }
  return 260
}

type FileWorkspacePaneProps = {
  source?: ReadOnlyFileWorkspaceSource
  workspacePath?: string
  title?: string
  hiddenRootFolders?: string[]
  hideAddToChat?: boolean
  hideRootActions?: boolean
  expandFirstLevelFolders?: boolean
  hideManagedEntriesByDefault?: boolean
  testId?: string
  headerAction?: ReactNode
  /** Sends a request to the project's agent (commit, pull and push go through it). */
  onAsk?: (message: string) => void | Promise<unknown>
}

/**
 * Shared Files surface for any product that exposes a workspace. It keeps the
 * tree mounted while a file is open, preserving navigation state, and makes
 * the file viewer occupy that product's right-side Files pane rather than
 * opening a competing full-screen overlay.
 */
function FileWorkspacePaneBody({
  source,
  workspacePath,
  title,
  hiddenRootFolders,
  hideAddToChat = false,
  hideRootActions = false,
  expandFirstLevelFolders = EXPAND_FIRST_LEVEL_FOLDERS_BY_DEFAULT,
  hideManagedEntriesByDefault = false,
  testId,
  headerAction,
  onAsk,
}: FileWorkspacePaneProps) {
  const git = useFileGit()
  const gitEnabled = !source || !!source.git
  const gitPath = workspacePath ?? (source?.git ? "" : undefined)
  const workspaceShowFileContent = useWorkspaceStore(state => state.showFileContent)
  const workspaceFiles = useWorkspaceStore(state => state.files)
  const workspaceSelectedPath = useWorkspaceStore(state => state.selectedFile?.path ?? null)
  const showFileContent = source?.showFileContent ?? workspaceShowFileContent
  const files = source?.files ?? workspaceFiles
  const selectedPath = source ? source.selectedFile?.path ?? null : workspaceSelectedPath
  const gitPanel = useFileGitStore(state => state.panel)
  const hasRepos = useFileGitStore(state => state.repos.length > 0)
  const [changesOpen, setChangesOpen] = useState(false)

  // Git status for the folder this pane shows: on mount, whenever the tree
  // reloads (debounced), and when the window regains focus.
  useEffect(() => {
    if (!gitEnabled || gitPath === undefined) return
    const state = git.store.getState()
    if (state.workspacePath !== gitPath) state.clear()
    void state.refresh(gitPath)
    const onFocus = () => { void git.store.getState().refresh(gitPath!) }
    window.addEventListener('focus', onFocus)
    return () => window.removeEventListener('focus', onFocus)
  }, [gitPath, gitEnabled, git.store])
  useEffect(() => {
    if (!gitEnabled || gitPath === undefined) return
    const timer = window.setTimeout(() => { void git.store.getState().refresh(gitPath!) }, 1200)
    return () => window.clearTimeout(timer)
  }, [files, gitPath, gitEnabled, git.store])
  // Tabs belong to one workspace: switching projects must not carry the last one's files along.
  useEffect(() => { if (!source && workspacePath) useWorkspaceStore.getState().pruneOpenTabs(workspacePath) }, [gitPath, gitEnabled, git.store])
  // Follow the open file in the tree (VS Code's auto-reveal): expand its folders and scroll to it.
  useEffect(() => {
    if (!source && showFileContent && selectedPath) void useWorkspaceStore.getState().scrollToFile(selectedPath)
  }, [showFileContent, selectedPath, !!source])
  // Opening a file from the tree replaces any diff or history panel.
  useEffect(() => { if (gitEnabled) git.store.getState().openPanel(null) }, [selectedPath, !!source])
  useEffect(() => { if (!hasRepos) setChangesOpen(false) }, [hasRepos])

  const rightOpen = showFileContent || (gitEnabled && !!gitPanel && gitPath !== undefined)
  const containerRef = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)
  const [treeWidth, setTreeWidth] = useState(readTreeWidth)

  useEffect(() => {
    const el = containerRef.current
    if (!el || typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(entries => setWidth(entries[0]?.contentRect.width ?? 0))
    observer.observe(el)
    return () => observer.disconnect()
  }, [])

  const split = rightOpen && width >= SPLIT_MIN_WIDTH
  const saveTreeWidth = (next: number) => {
    const clamped = Math.min(TREE_MAX, Math.max(TREE_MIN, Math.round(next)))
    setTreeWidth(clamped)
    try { window.localStorage.setItem(TREE_WIDTH_KEY, String(clamped)) } catch { /* optional */ }
  }
  const startResize = (event: PointerEvent<HTMLDivElement>) => {
    event.preventDefault()
    const left = containerRef.current?.getBoundingClientRect().left ?? 0
    const move = (e: globalThis.PointerEvent) => saveTreeWidth(e.clientX - left)
    const up = () => {
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
      document.body.style.cursor = ''
    }
    document.body.style.cursor = 'col-resize'
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }
  const stepResize = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
    event.preventDefault()
    saveTreeWidth(treeWidth + (event.key === 'ArrowLeft' ? -16 : 16))
  }

  return (
    <div ref={containerRef} className="relative flex h-full min-h-0 flex-row bg-background" data-testid={testId} data-layout={split ? 'split' : 'single'}>
      {/* One tree element in both layouts, so opening a file never remounts it. */}
      <div
        className={split ? 'min-h-0 shrink-0 overflow-hidden border-r border-border' : 'min-h-0 min-w-0 flex-1'}
        style={split ? { width: treeWidth } : undefined}
        hidden={rightOpen && !split}
      >
        <div className="flex h-full min-h-0 flex-row">
          {gitEnabled && gitPath !== undefined && hasRepos && <ActivityRail view={changesOpen ? 'scm' : 'files'} onChange={next => setChangesOpen(next === 'scm')} />}
          <div className="flex min-h-0 min-w-0 flex-1 flex-col">
            {gitEnabled && gitPath !== undefined && !changesOpen && <GitBar />}
            {gitEnabled && gitPath !== undefined && changesOpen && hasRepos && (
              <div className="min-h-0 flex-1"><GitChangesList workspacePath={gitPath!} onAsk={onAsk} /></div>
            )}
            <div className="min-h-0 flex-1" hidden={gitEnabled && changesOpen && hasRepos}>
              {source ? <ReadOnlyFileTree source={source} title={title || 'Files'} headerAction={split ? undefined : headerAction} /> : <Workspace
                scopedWorkspacePath={workspacePath}
                hiddenRootFolders={hiddenRootFolders}
                hideAddToChat={hideAddToChat}
                hideRootActions={hideRootActions}
                expandFirstLevelFolders={expandFirstLevelFolders}
                hideManagedEntriesByDefault={hideManagedEntriesByDefault}
                title={title}
                headerAction={split ? undefined : headerAction}
              />}
            </div>
          </div>
        </div>
      </div>
      {split && (
        <div
          role="separator"
          aria-orientation="vertical"
          aria-label="Resize file tree"
          aria-valuemin={TREE_MIN}
          aria-valuemax={TREE_MAX}
          aria-valuenow={treeWidth}
          tabIndex={0}
          onPointerDown={startResize}
          onKeyDown={stepResize}
          className="relative z-10 -ml-1 w-2 shrink-0 cursor-col-resize outline-none after:absolute after:inset-y-0 after:left-1/2 after:w-px after:-translate-x-1/2 after:bg-transparent hover:after:bg-primary focus-visible:after:bg-primary"
        />
      )}
      {gitEnabled && gitPanel && gitPath !== undefined ? (
        <div className="min-h-0 min-w-0 flex-1">
          <GitFilePanel workspacePath={gitPath!} panel={gitPanel} onClose={() => git.store.getState().openPanel(null)} onAsk={onAsk} />
        </div>
      ) : showFileContent && (
        <div className="min-h-0 min-w-0 flex-1">
          <FileContentViewerBody headerAction={headerAction} source={source} />
        </div>
      )}
    </div>
  )
}

export function FileWorkspacePane(props: FileWorkspacePaneProps) {
  const existing = useFileGit()
  return <FileGitContext.Provider value={props.source?.git ?? existing}><FileWorkspacePaneBody {...props} /></FileGitContext.Provider>
}
