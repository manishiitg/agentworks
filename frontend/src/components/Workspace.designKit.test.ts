import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('Files design kit', () => {
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
})
