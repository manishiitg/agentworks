import type { ReactNode } from 'react'
import type { PlannerFile } from '../../services/api-types'

export interface FileViewerSource {
  selectedFile: { name: string; path: string } | null
  fileContent: string
  loadingFileContent: boolean
  showFileContent: boolean
  setShowFileContent: (show: boolean) => void
  openTabs: Array<{ name: string; path: string }>
  openFile: (path: string) => void | Promise<void>
  closeFile: (path: string) => void
  revealFolder: (path: string) => void
  contentHeader?: ReactNode
}

/** A read-only product data source. Paths never go through workspace APIs. */
export interface ReadOnlyFileWorkspaceSource extends FileViewerSource {
  files: PlannerFile[]
  loading: boolean
  error: string | null
  expandedFolders: Set<string>
  toggleFolder: (path: string) => void
  refresh: () => void
}
