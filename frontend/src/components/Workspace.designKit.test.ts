import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const FILES_SURFACE = [
  'src/components/Workspace.tsx',
  'src/components/FileWorkspacePane.tsx',
  'src/components/FileContentViewer.tsx',
  'src/components/workspace/PlannerFileList.tsx',
  'src/components/workspace/MoveFileDialog.tsx',
  'src/components/workspace/RenameFileDialog.tsx',
  'src/components/workspace/CreateFolderDialog.tsx',
  'src/components/workspace/PushToGistDialog.tsx',
  'src/components/ui/ConfirmationDialog.tsx',
  'src/components/FileContextDisplay.tsx',
  'src/stores/useWorkspaceStore.ts',
]

// Raw Tailwind palette steps that the design kit replaces with semantic
// tokens (foreground/muted/primary/destructive/border/...). Emerald and
// amber stay: success and warning semantics respectively.
const BANNED_PALETTE = [
  'text-gray-', 'bg-gray-', 'border-gray-',
  'text-blue-', 'bg-blue-', 'border-blue-',
  'text-red-', 'bg-red-', 'border-red-',
  'text-green-', 'bg-green-',
  'text-purple-', 'bg-purple-',
  'text-orange-', 'bg-orange-',
  'text-yellow-', 'bg-yellow-',
  'text-lg', 'bg-black bg-opacity',
]

describe('Files design kit', () => {
  it('keeps the Explorer header VS Code-style: title, Open Editors, then the folder toolbar', () => {
    const workspace = readFileSync('src/components/Workspace.tsx', 'utf8')
    const header = readFileSync('src/components/workspace/ExplorerHeader.tsx', 'utf8')

    expect(workspace).toContain('<ExplorerHeader')
    for (const label of ['Upload file', 'New folder', 'Refresh files', 'Collapse all folders', 'Select files']) {
      expect(workspace).toContain(`aria-label="${label}"`)
    }
    // The agent action sits with the title, the toolbar icons on the folder row.
    expect(header.indexOf('Explorer')).toBeLessThan(header.indexOf('<OpenEditors />'))
    expect(header.indexOf('<OpenEditors />')).toBeLessThan(header.indexOf('{toolbar}'))
    expect(header).toContain('Open Editors')
  })

  it('keeps the filter in the content below the header, not inside it', () => {
    const workspace = readFileSync('src/components/Workspace.tsx', 'utf8')

    expect(workspace).not.toContain('below={')
    expect(workspace).toContain('Search sits in the content, below the header line')
    expect(workspace).toContain('placeholder="Filter files"')
  })

  it('routes feedback through kit toasts and dialogs, never hand-rolled fixed popups', () => {
    for (const file of ['src/components/Workspace.tsx', 'src/components/FileContentViewer.tsx']) {
      const source = readFileSync(file, 'utf8')
      expect(source).not.toContain('fixed bottom-4 right-4')
      expect(source).not.toContain('window.confirm')
    }
    // The viewer still toasts; the tree lost its last toast call site with the
    // ZIP backup removal and must not grow a hand-rolled popup instead.
    expect(readFileSync('src/components/FileContentViewer.tsx', 'utf8')).toContain('addToast')
    // The viewer is read-only now; the tree's destructive actions still confirm.
    expect(readFileSync('src/components/Workspace.tsx', 'utf8')).toContain('<ConfirmationDialog')
  })

  it('keeps the Files surface on kit tokens (emerald/amber carry success/warning)', () => {
    for (const file of FILES_SURFACE) {
      // Rendered markdown keeps its prose color overrides; everything else
      // must use semantic tokens.
      const source = readFileSync(file, 'utf8')
        .split('\n')
        .filter(line => !line.includes('prose-'))
        .join('\n')
      for (const token of BANNED_PALETTE) {
        expect(source, `${file} contains banned ${token}`).not.toContain(token)
      }
    }
  })

  it('makes Delete All Contents type the folder name before it can run', () => {
    const workspace = readFileSync('src/components/Workspace.tsx', 'utf8')

    expect(workspace).toContain('title="Delete All Contents"')
    expect(workspace).toContain("requireText={deleteAllFilesDialog.folder?.filepath.split('/').pop() || 'DELETE'}")
  })

  it('lazy-loads the Files pane at every mount point', () => {
    const mounts = [
      'src/components/workflow/canvas/WorkspaceViewHost.tsx',
      'src/products/work/WorkWorkspacePane.tsx',
      'src/products/video-studio/VideoStudioSurface.tsx',
    ]
    for (const file of mounts) {
      const source = readFileSync(file, 'utf8')
      expect(source, `${file} statically bundles Files`).not.toContain(
        "from '../../components/FileWorkspacePane'",
      )
      expect(source, `${file} statically bundles Files`).not.toContain(
        "from '../../FileWorkspacePane'",
      )
      expect(source, `${file} must lazy-load Files`).toContain(
        'const FileWorkspacePane = lazy(() => import(',
      )
      expect(source, `${file} lazy Files must map the named export`).toContain(
        '({ default: module.FileWorkspacePane })',
      )
      expect(source, `${file} lazy Files needs a Suspense fallback`).toContain('<Suspense')
    }
  })

  it('keeps the viewer fixes in place', () => {
    const viewer = readFileSync('src/components/FileContentViewer.tsx', 'utf8')

    expect(viewer).not.toContain('exportProgress')
    // Viewer only: no edit mode (people ask the agent to change files).
    expect(viewer).not.toContain('isEditMode')
    expect(viewer).not.toContain('handleSave')
    expect(viewer).not.toContain('navigator.clipboard.writeText')
    expect(viewer).toContain("Couldn't load this image")
    expect(viewer).toContain('parsedJsonContent')
    expect(viewer.split('JSON.parse(fileContent)').length - 1).toBe(1)
    expect(viewer).toContain('useMediaObjectUrl')
    expect(viewer).toContain("from '../utils/fileTypes'")
    expect(viewer).not.toContain('autoPlay')
  })

  it('keeps the phase-1 dead code deleted', () => {
    const workspace = readFileSync('src/components/Workspace.tsx', 'utf8')
    const pane = readFileSync('src/components/FileWorkspacePane.tsx', 'utf8')
    const viewer = readFileSync('src/components/FileContentViewer.tsx', 'utf8')
    const list = readFileSync('src/components/workspace/PlannerFileList.tsx', 'utf8')
    const store = readFileSync('src/stores/useWorkspaceStore.ts', 'utf8')

    for (const source of [workspace, pane, viewer, list, store]) {
      expect(source).not.toContain('minimiz')
      expect(source).not.toContain('moveDialog')
      expect(source).not.toContain('dirtyFolders')
      expect(source).not.toContain('loadingChildren')
    }
    expect(viewer).not.toContain('FileContentViewerOverlay')
    expect(viewer).not.toContain('variant=')
    expect(store).not.toContain('markDirtyFolder')
    expect(store).not.toContain('refreshDirtyFolders')
    expect(store).toContain('WORKSPACE_REFRESH_DEBOUNCE_MS')
  })
})
