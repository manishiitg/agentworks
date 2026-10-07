import { WorkspaceImage } from './WorkspaceImage'
import { sharedLink, repairLocalAppLink } from '../../utils/sharedLinks'
import React, { lazy, Suspense, useEffect, useRef, useState, useCallback } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { useWorkspaceStore } from '../../stores/useWorkspaceStore'
import { useAppStore } from '../../stores/useAppStore'
import { useModeStore } from '../../stores/useModeStore'
import { useWorkflowStore } from '../../stores/useWorkflowStore'
import { useProductSurfaceStore } from '../../stores/useProductSurfaceStore'
import { useGlobalPresetStore } from '../../stores/useGlobalPresetStore'
import { workspaceApi, agentApi } from '../../services/api'

const SyntaxHighlightedCode = lazy(() => import('./SyntaxHighlightedCode'))

let mermaidModule: Promise<typeof import('mermaid').default> | null = null
const loadMermaid = () => {
  mermaidModule ??= import('mermaid').then(module => module.default)
  return mermaidModule
}

interface MarkdownRendererProps {
  /** Block remote media and workspace navigation for shared untrusted content. */
  untrustedContent?: boolean
  content: string
  className?: string
  maxHeight?: string
  showScrollbar?: boolean
  disablePathLinking?: boolean
  // Conversation messages can contain large generated reference images. Keep
  // those messages scannable while leaving the original asset one click away.
  compactImages?: boolean
  basePath?: string
  onLinkClick?: (filepath: string) => void
  workspaceLinkHref?: (filepath: string) => string
  onWorkspaceLinkResolve?: (filepath: string, displayPath: string) => { filepath: string; displayPath: string } | null | undefined
  onWorkspaceLinkClick?: (filepath: string, displayPath: string) => boolean | void
  // When provided, a ```report-widget fenced block whose body is a widget JSON
  // spec is replaced by a live, db-bound report widget. Supplied by the report
  // viewer (MarkdownWidget / FileWidget) so a generated .md document can embed
  // live widgets inline. Absent everywhere else, so the fence renders as code.
  renderEmbeddedWidget?: (spec: unknown) => React.ReactNode
}

const workspacePrefixes = ['Chats/', 'Downloads/', 'skills/', 'Workflow/', 'knowledgebase/', '_users/', 'learnings/']
const workspaceStandardPrefixes = ['Chats/', 'Downloads/', 'skills/', 'Workflow/', 'learnings/']
const linkableWorkspaceFileExtensions = [
  'md', 'markdown', 'txt', 'json', 'jsonl', 'csv', 'tsv', 'yaml', 'yml', 'xml',
  'html', 'htm', 'pdf', 'doc', 'docx', 'xls', 'xlsx', 'png', 'jpg', 'jpeg',
  'gif', 'webp', 'svg', 'bmp', 'ico', 'log', 'diff', 'patch'
]

const linkableWorkspaceFileExtensionPattern = linkableWorkspaceFileExtensions.join('|')

const safeDecodeURIComponent = (value: string): string => {
  try {
    return decodeURIComponent(value)
  } catch {
    return value
  }
}

const stripLinkFragmentAndQuery = (href: string): string => href.split(/[?#]/, 1)[0]

const hasWorkspacePrefix = (path: string): boolean => workspacePrefixes.some(prefix => path.startsWith(prefix))

const hasExplicitUrlProtocol = (href: string): boolean => /^[a-z][a-z0-9+.-]*:/i.test(href)

const resolveSafeExternalHref = (href?: string | null): string | null => {
  const trimmed = (href || '').trim()
  if (!/^(https?:|mailto:)/i.test(trimmed)) return null
  try {
    const url = new URL(trimmed)
    if (url.protocol === 'http:' || url.protocol === 'https:') {
      return typeof window === 'undefined' ? url.toString() : repairLocalAppLink(url.toString(), window.location.origin)
    }
    if (url.protocol === 'mailto:') {
      return url.toString()
    }
  } catch {
    return null
  }
  return null
}

const openExternalHref = (href?: string | null): boolean => {
  const externalHref = resolveSafeExternalHref(href)
  if (!externalHref) return false
  const electronAPI = (window as unknown as { electronAPI?: { openExternal?: (url: string) => void } }).electronAPI
  if (electronAPI?.openExternal) {
    electronAPI.openExternal(externalHref)
    return true
  }
  window.open(externalHref, '_blank', 'noopener,noreferrer')
  return true
}

const splitPipeTableCells = (line: string): string[] => {
  const trimmed = line.trim()
  if (!trimmed.startsWith('|')) return []
  return trimmed.replace(/^\|/, '').replace(/\|$/, '').split('|').map(cell => cell.trim())
}

const isPipeTableSeparator = (line: string): boolean => {
  const cells = splitPipeTableCells(line)
  return cells.length >= 2 && cells.every(cell => /^:?-{3,}:?$/.test(cell))
}

const isPipeTableRow = (line: string): boolean => {
  const cells = splitPipeTableCells(line)
  return cells.length >= 2 && cells.some(cell => cell.length > 0) && !isPipeTableSeparator(line)
}

const pipeTableSeparatorFor = (line: string): string => {
  const cells = splitPipeTableCells(line)
  return `| ${cells.map(() => '---').join(' | ')} |`
}

const pipeTableLineForCells = (cells: string[]): string => `| ${cells.join(' | ')} |`

const isBlankPipeTableCell = (cell: string): boolean => cell.trim() === ''

const isLikelyContinuationPipeRow = (cells: string[]): boolean => {
  if (cells.length < 2) return false
  const nonEmptyCells = cells.filter(cell => cell.trim() !== '')
  if (nonEmptyCells.length !== 1) return false
  const nonEmptyIndex = cells.findIndex(cell => cell.trim() !== '')
  return nonEmptyIndex > 0 && cells.slice(0, nonEmptyIndex).every(isBlankPipeTableCell)
}

const mergeContinuationPipeRow = (previous: string[], continuation: string[]): string[] => {
  const nonEmptyIndex = continuation.findIndex(cell => cell.trim() !== '')
  if (nonEmptyIndex < 0 || nonEmptyIndex >= previous.length) return previous

  const merged = [...previous]
  const prior = merged[nonEmptyIndex].trim()
  const suffix = continuation[nonEmptyIndex].trim()
  merged[nonEmptyIndex] = prior ? `${prior} ${suffix}` : suffix
  return merged
}

const normalizePipeTableBlock = (rows: string[]): string[] => {
  if (rows.length < 2) return rows

  const output: string[] = []
  let sawHeaderSeparator = false

  for (const row of rows) {
    const cells = splitPipeTableCells(row)
    if (cells.length < 2) {
      output.push(row)
      continue
    }

    if (isPipeTableSeparator(row)) {
      if (!sawHeaderSeparator && output.length > 0) {
        output.push(pipeTableSeparatorFor(output[0]))
        sawHeaderSeparator = true
      }
      continue
    }

    if (sawHeaderSeparator && isLikelyContinuationPipeRow(cells) && output.length > 2) {
      const previousCells = splitPipeTableCells(output[output.length - 1])
      if (previousCells.length === cells.length) {
        output[output.length - 1] = pipeTableLineForCells(mergeContinuationPipeRow(previousCells, cells))
        continue
      }
    }

    output.push(row)
  }

  return output
}

const isBoxTableBorderLine = (line: string): boolean => {
  const trimmed = line.trim()
  return /^[┌┬┐├┼┤└┴┘─]+$/.test(trimmed)
}

const splitBoxTableCells = (line: string): string[] => {
  const trimmed = line.trim()
  if (!trimmed.startsWith('│') || !trimmed.includes('│')) return []
  return trimmed
    .replace(/^│/, '')
    .replace(/│$/, '')
    .split('│')
    .map(cell => cell.trim())
}

const flushBoxTable = (output: string[], boxRows: string[][]) => {
  if (boxRows.length < 2 || boxRows[0].length < 1) {
    for (const row of boxRows) output.push(`│ ${row.join(' │ ')} │`)
    return
  }

  const header = boxRows[0]
  output.push(`| ${header.join(' | ')} |`)
  output.push(`| ${header.map(() => '---').join(' | ')} |`)
  for (const row of boxRows.slice(1)) {
    output.push(`| ${row.join(' | ')} |`)
  }
}

const normalizeBoxDrawingTables = (value: string): string => {
  const lines = value.split('\n')
  const output: string[] = []
  let inFence = false
  let boxRows: string[][] = []
  let sawBoxBorder = false

  const flush = () => {
    if (boxRows.length >= 2 || (boxRows.length > 0 && sawBoxBorder)) {
      flushBoxTable(output, boxRows)
    } else {
      for (const row of boxRows) output.push(`│ ${row.join(' │ ')} │`)
    }
    boxRows = []
    sawBoxBorder = false
  }

  for (const line of lines) {
    const trimmed = line.trim()
    const isFence = trimmed.startsWith('```') || trimmed.startsWith('~~~')

    if (isFence) {
      flush()
      inFence = !inFence
      output.push(line)
      continue
    }
    if (inFence) {
      output.push(line)
      continue
    }

    if (isBoxTableBorderLine(line)) {
      sawBoxBorder = true
      continue
    }

    const cells = splitBoxTableCells(line)
    if (cells.length >= 1) {
      boxRows.push(cells)
      continue
    }

    flush()
    output.push(line)
  }

  flush()
  return output.join('\n')
}

const normalizeLooseMarkdownTables = (value: string): string => {
  const lines = value.split('\n')
  const output: string[] = []
  let inFence = false
  let tableRows: string[] = []

  const flushTableRows = () => {
    if (tableRows.length > 0) {
      output.push(...normalizePipeTableBlock(tableRows))
      tableRows = []
    }
  }

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const trimmed = line.trim()
    const isFence = trimmed.startsWith('```') || trimmed.startsWith('~~~')

    if (isFence) {
      flushTableRows()
      output.push(line)
      inFence = !inFence
      continue
    }
    if (inFence) {
      output.push(line)
      continue
    }

    if (!isPipeTableRow(line) && !isPipeTableSeparator(line)) {
      flushTableRows()
      output.push(line)
      continue
    }

    tableRows.push(line)

    const nextLine = lines[i + 1] || ''
    const previousOutputLine = tableRows[tableRows.length - 2] || ''
    if (
      isPipeTableRow(nextLine) &&
      !isPipeTableSeparator(nextLine) &&
      !isPipeTableSeparator(previousOutputLine)
    ) {
      tableRows.push(pipeTableSeparatorFor(line))
    }
  }

  flushTableRows()
  return output.join('\n')
}

export const normalizeMarkdownContent = (value: string): string => {
  if (!value) return ""

  let processed = value.replace(/(^|\n)---\n([a-zA-Z0-9_-]+:[\s\S]+?)\n---(\n|$)/g, '$1\n```tool-definition\n$2\n```\n$3')
  processed = normalizeBoxDrawingTables(processed)
  processed = normalizeLooseMarkdownTables(processed)
  processed = normalizeLooseNestedLists(processed)
  processed = normalizeSectionedFlatLists(processed)
  return processed
}

const normalizeLooseNestedLists = (value: string): string => {
  const lines = value.split('\n')
  const output: string[] = []
  let changed = false
  let inFence = false
  let pendingOrderedIndent: string | null = null

  for (const line of lines) {
    const trimmed = line.trim()
    const isFence = trimmed.startsWith('```') || trimmed.startsWith('~~~')

    if (isFence) {
      output.push(line)
      inFence = !inFence
      pendingOrderedIndent = null
      continue
    }
    if (inFence) {
      output.push(line)
      continue
    }

    const orderedMatch = /^(\s*)\d+[.)]\s+\S/.exec(line)
    if (orderedMatch) {
      output.push(line)
      pendingOrderedIndent = orderedMatch[1]
      continue
    }

    if (pendingOrderedIndent !== null && trimmed === '') {
      output.push(line)
      continue
    }

    const unorderedMatch = /^(\s*)[-*+]\s+\S/.exec(line)
    if (pendingOrderedIndent !== null && unorderedMatch && unorderedMatch[1].length <= pendingOrderedIndent.length) {
      output.push(`${pendingOrderedIndent}   ${line.trimStart()}`)
      changed = true
      continue
    }

    pendingOrderedIndent = null
    output.push(line)
  }

  return changed ? output.join('\n') : value
}

const sectionedFlatListLabels = new Set([
  'files',
  'folders',
  'directories',
  'root files',
  'root folders'
])

const normalizeSectionedFlatLists = (value: string): string => {
  const lines = value.split('\n')
  const output: string[] = []
  let changed = false
  let inFence = false

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i]
    const trimmed = line.trim()
    if (trimmed.startsWith('```') || trimmed.startsWith('~~~')) {
      output.push(line)
      inFence = !inFence
      continue
    }
    if (inFence) {
      output.push(line)
      continue
    }

    const match = /^[-*]\s+(.+?)\s*$/.exec(line)
    if (!match) {
      output.push(line)
      continue
    }

    const label = match[1].replace(/:$/, '').trim()
    if (!sectionedFlatListLabels.has(label.toLowerCase())) {
      output.push(line)
      continue
    }

    let hasFollowingItem = false
    for (let j = i + 1; j < lines.length; j++) {
      if (!lines[j].trim()) break
      const nextMatch = /^[-*]\s+(.+?)\s*$/.exec(lines[j])
      if (!nextMatch) break
      const nextLabel = nextMatch[1].replace(/:$/, '').trim()
      if (!sectionedFlatListLabels.has(nextLabel.toLowerCase())) {
        hasFollowingItem = true
      }
      break
    }

    if (!hasFollowingItem) {
      output.push(line)
      continue
    }

    if (output.length > 0 && output[output.length - 1].trim() !== '') {
      output.push('')
    }
    output.push(`**${label}**`)
    output.push('')
    changed = true
  }

  return changed ? output.join('\n').replace(/\n{3,}/g, '\n\n') : value
}

const isLikelyRelativeWorkspaceFile = (href: string): boolean => {
  if (!href || href.startsWith('#') || href.startsWith('//') || hasExplicitUrlProtocol(href)) return false
  const path = stripLinkFragmentAndQuery(href).replace(/^\/+/, '')
  return new RegExp(`(?:^|/)\\.?[^/]+\\.(${linkableWorkspaceFileExtensionPattern})$`, 'i').test(path)
}

const normalizeWorkspacePathSegments = (path: string): string => {
  const output: string[] = []
  for (const segment of path.split('/')) {
    if (!segment || segment === '.') continue
    if (segment === '..') {
      output.pop()
      continue
    }
    output.push(segment)
  }
  return output.join('/')
}

const getParentPath = (path: string): string => {
  const normalized = path.replace(/\/+$/, '')
  const slashIndex = normalized.lastIndexOf('/')
  return slashIndex === -1 ? '' : normalized.slice(0, slashIndex)
}

const cleanToWorkspaceRelativePath = (path: string): string => {
  const normalizedPath = path.replace(/\\/g, '/')
  const prefixes = ['Workflow/', 'skills/', 'Chats/', 'Downloads/', 'knowledgebase/', '_users/', 'learnings/']
  // Slice from the EARLIEST-occurring prefix, not the first one in the list.
  // A list-order match would truncate "_users/bob/Chats/x.md" to "Chats/x.md"
  // (dropping the user scope) because "Chats/" is checked before "_users/".
  // Picking the smallest index keeps the outermost workspace root intact.
  let earliestIndex = -1
  for (const prefix of prefixes) {
    const index = normalizedPath.indexOf(prefix)
    if (index !== -1 && (earliestIndex === -1 || index < earliestIndex)) {
      earliestIndex = index
    }
  }
  return earliestIndex === -1 ? normalizedPath : normalizedPath.slice(earliestIndex)
}

const resolveRelativeWorkspacePath = (href: string, basePath: string): string => {
  const cleanedHref = safeDecodeURIComponent(stripLinkFragmentAndQuery(href)).replace(/^\/+/, '')
  const baseDir = getParentPath(basePath)
  const resolved = normalizeWorkspacePathSegments(baseDir ? `${baseDir}/${cleanedHref}` : cleanedHref)
  return cleanToWorkspaceRelativePath(resolved)
}

const ToolDefinition: React.FC<{ content: string }> = ({ content }) => {
  const lines = content.split('\n').filter(line => line.trim() !== '')
  const data: Record<string, string> = {}
  
  lines.forEach(line => {
    const colonIndex = line.indexOf(':')
    if (colonIndex !== -1) {
      const key = line.slice(0, colonIndex).trim()
      let value = line.slice(colonIndex + 1).trim()
      // Remove quotes if present
      if (value.startsWith('"') && value.endsWith('"')) {
        value = value.slice(1, -1)
      }
      data[key] = value
    }
  })

  return (
    <div className="bg-gray-50 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg p-4 my-4 shadow-sm">
      <div className="font-semibold text-gray-900 dark:text-gray-100 mb-2 border-b border-gray-200 dark:border-gray-700 pb-2 flex items-center gap-2">
        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="text-blue-500">
          <path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"></path>
        </svg>
        Tool Definition
      </div>
      <div className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm">
        {Object.entries(data).map(([key, value]) => (
          <React.Fragment key={key}>
            <div className="font-medium text-gray-700 dark:text-gray-300 capitalize text-right self-start whitespace-nowrap">
              {key.replace(/-/g, ' ')}:
            </div>
            <div className="text-gray-900 dark:text-gray-100 font-mono text-xs bg-white dark:bg-gray-900 px-2 py-0.5 rounded border border-gray-200 dark:border-gray-700 break-all">
              {value || <span className="text-gray-400 italic">None</span>}
            </div>
          </React.Fragment>
        ))}
      </div>
    </div>
  )
}

let mermaidCounter = 0

export const MermaidDiagram: React.FC<{ content: string }> = ({ content }) => {
  const containerRef = useRef<HTMLDivElement>(null)
  const [svg, setSvg] = useState<string>('')
  const [error, setError] = useState<string>('')
  const idRef = useRef(`mermaid-${Date.now()}-${mermaidCounter++}`)

  const renderDiagram = useCallback(async () => {
    try {
      const mermaid = await loadMermaid()
      // Re-init theme on each render to pick up dark mode changes
      const isDark = document.documentElement.classList.contains('dark') ||
                     document.documentElement.classList.contains('dark-plus')
      mermaid.initialize({
        startOnLoad: false,
        theme: isDark ? 'dark' : 'default',
        securityLevel: 'loose',
      })
      const { svg: renderedSvg } = await mermaid.render(idRef.current, content)
      setSvg(renderedSvg)
      setError('')
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to render mermaid diagram')
      setSvg('')
    }
  }, [content])

  useEffect(() => {
    renderDiagram()
  }, [renderDiagram])

  if (error) {
    return (
      <div className="my-4 p-4 border border-red-300 dark:border-red-700 rounded-lg bg-red-50 dark:bg-red-900/20">
        <div className="text-xs text-red-600 dark:text-red-400 mb-2 font-medium">Mermaid diagram error</div>
        <pre className="text-xs text-red-500 dark:text-red-400 whitespace-pre-wrap">{error}</pre>
        <details className="mt-2">
          <summary className="text-xs text-gray-500 cursor-pointer">Show source</summary>
          <pre className="mt-1 text-xs text-gray-600 dark:text-gray-400 whitespace-pre-wrap">{content}</pre>
        </details>
      </div>
    )
  }

  if (!svg) {
    return (
      <div className="my-4 p-4 border border-gray-200 dark:border-gray-700 rounded-lg bg-gray-50 dark:bg-gray-800 text-center text-sm text-gray-500">
        Rendering diagram...
      </div>
    )
  }

  return (
    <div
      ref={containerRef}
      className="my-4 p-4 border border-gray-200 dark:border-gray-700 rounded-lg bg-white dark:bg-gray-900 overflow-x-auto"
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  )
}

const MarkdownRendererImpl: React.FC<MarkdownRendererProps> = ({
  content,
  className = "",
  maxHeight = "none",
  showScrollbar = false,
  disablePathLinking = false,
  untrustedContent = false,
  compactImages = false,
  basePath,
  onLinkClick,
  workspaceLinkHref,
  onWorkspaceLinkResolve,
  onWorkspaceLinkClick,
  renderEmbeddedWidget
}) => {
  const containerClasses = `prose prose-sm max-w-none dark:prose-invert ${className}`
  const scrollClasses = showScrollbar ? 'overflow-y-auto overflow-x-auto min-w-0' : ""
  const containerStyle = showScrollbar && maxHeight !== 'none'
    ? { maxHeight }
    : undefined

  const highlightFile = useWorkspaceStore(state => state.highlightFile)
  const expandFoldersForFile = useWorkspaceStore(state => state.expandFoldersForFile)
  const setSelectedFile = useWorkspaceStore(state => state.setSelectedFile)
  const setShowFileContent = useWorkspaceStore(state => state.setShowFileContent)
  const setFileContent = useWorkspaceStore(state => state.setFileContent)
  const setLoadingFileContent = useWorkspaceStore(state => state.setLoadingFileContent)
  const setBinaryFileData = useWorkspaceStore(state => state.setBinaryFileData)
  const setWorkspaceMinimized = useAppStore(state => state.setWorkspaceMinimized)

  const getActiveWorkflowFolderPath = useCallback((): string | null => {
    const modeCategory = useModeStore.getState().selectedModeCategory
    if (modeCategory !== 'workflow') return null

    const activePreset = useGlobalPresetStore.getState().getActivePreset('workflow')
    const folderFilepath = activePreset?.selectedFolder?.filepath
    if (!folderFilepath) return null

    const parts = folderFilepath.split('/').filter(Boolean)
    const lastPart = parts[parts.length - 1]
    const isFile = lastPart?.includes('.')
    const folderPath = isFile && parts.length > 1 ? parts.slice(0, -1).join('/') : parts.join('/')
    return cleanToWorkspaceRelativePath(folderPath)
  }, [])


  const getWorkspaceDisplayPath = useCallback((filepath: string): string => {
    const workflowFolderPath = getActiveWorkflowFolderPath()
    if (!workflowFolderPath) return filepath

    const normalizedFilepath = filepath.replace(/^\/+|\/+$/g, '')
    const normalizedWorkflowFolderPath = workflowFolderPath.replace(/^\/+|\/+$/g, '')

    if (normalizedFilepath === normalizedWorkflowFolderPath) {
      const parts = normalizedWorkflowFolderPath.split('/').filter(Boolean)
      return parts[parts.length - 1] || filepath
    }

    if (normalizedFilepath.startsWith(`${normalizedWorkflowFolderPath}/`)) {
      return normalizedFilepath.slice(normalizedWorkflowFolderPath.length + 1)
    }

    return filepath
  }, [getActiveWorkflowFolderPath])

  const resolveWorkspaceHref = useCallback((href?: string | null): { filepath: string; displayPath: string } | null => {
    if (!href) return null

    const sameOriginUrl = (() => {
      if (typeof window === 'undefined') return null
      try {
        const url = new URL(href, window.location.href)
        return url.origin === window.location.origin ? url : null
      } catch {
        return null
      }
    })()

    if (href.startsWith('#workspace/')) {
      const filepath = safeDecodeURIComponent(href.replace('#workspace/', ''))
      return { filepath, displayPath: getWorkspaceDisplayPath(filepath) }
    }

    if (sameOriginUrl?.hash.startsWith('#workspace/')) {
      const filepath = safeDecodeURIComponent(sameOriginUrl.hash.replace('#workspace/', ''))
      return { filepath, displayPath: getWorkspaceDisplayPath(filepath) }
    }

    // Agents often print the absolute disk path of a workspace file
    // (/Users/me/.../workspace-docs/Workflow/x/test_mode.json). Open it as the
    // workspace file it is instead of showing dead text; access is still decided
    // by the workspace API when the file is opened.
    const absoluteDocsPath = /^\/(?:[^?#]*\/)?workspace-docs\/([^?#]+)/.exec(safeDecodeURIComponent(href.trim()))
    if (absoluteDocsPath) {
      const filepath = absoluteDocsPath[1]
      return { filepath, displayPath: getWorkspaceDisplayPath(filepath) }
    }

    const strippedHref = safeDecodeURIComponent(stripLinkFragmentAndQuery(href).trim()).replace(/^\/+/, '')
    if (!strippedHref || strippedHref.startsWith('#') || strippedHref.startsWith('//') || hasExplicitUrlProtocol(strippedHref)) {
      const sameOriginPath = sameOriginUrl
        ? safeDecodeURIComponent(sameOriginUrl.pathname.replace(/^\/+/, ''))
        : ''

      if (!basePath || !sameOriginPath || !isLikelyRelativeWorkspaceFile(sameOriginPath)) {
        return null
      }

      const filepath = resolveRelativeWorkspacePath(sameOriginPath, basePath)
      return { filepath, displayPath: getWorkspaceDisplayPath(filepath) }
    }

    if (hasWorkspacePrefix(strippedHref)) {
      return { filepath: strippedHref, displayPath: getWorkspaceDisplayPath(strippedHref) }
    }

    if (basePath && isLikelyRelativeWorkspaceFile(strippedHref)) {
      const filepath = resolveRelativeWorkspacePath(strippedHref, basePath)
      return { filepath, displayPath: getWorkspaceDisplayPath(filepath) }
    }

    return null
  }, [basePath, getWorkspaceDisplayPath])

  // Standard file opening logic — mirrors Workspace.tsx handleFileClick
  const handleWorkspaceLink = useCallback(async (filepath: string, displayPathOverride?: string) => {
    try {
      // In workflow mode, paths like "knowledgebase/..." are adjusted (prefix stripped).
      // We need to prepend the workflow folder path for the API call.
      const isStandardPath = workspaceStandardPrefixes.some(p => filepath.startsWith(p))
      let resolvedPath = filepath

      if (!isStandardPath) {
        const workflowFolderPath = getActiveWorkflowFolderPath()
        if (workflowFolderPath) {
          resolvedPath = `${workflowFolderPath}/${filepath}`
        }
      }

      // displayPath is what the workspace tree uses (adjusted path without workflow prefix)
      // resolvedPath is the full path for API calls
      const displayPath = displayPathOverride || getWorkspaceDisplayPath(resolvedPath)

      const fileName = resolvedPath.split('/').pop() || resolvedPath
      const ext = fileName.split('.').pop()?.toLowerCase() || ''

      // Check if binary viewable file
      const isViewableBinary = ['xls', 'xlsx', 'docx', 'pdf', 'webm', 'mp4', 'mov'].includes(ext)
      // 1. Ensure workspace is visible. On the main surface the viewer lives
      //    in the workspace pane's Files view, so bring that view up (this
      //    also un-minimizes the workspace). Other surfaces (Video Studio)
      //    mount the full-viewport overlay and only need the workspace open.
      setWorkspaceMinimized(false)
      if (useProductSurfaceStore.getState().productSurface === 'agentworks') {
        // The pane host is hidden behind the Workflows overview; the old
        // full-screen overlay covered the overview, so close it here or the
        // file opens where the user can't see it.
        useAppStore.getState().setShowWorkflowsOverview(false)
        useAppStore.getState().setShowSchedulesOverview(false)
        useWorkflowStore.getState().openWorkspaceView('files')
      }

      // 2. Expand folders and highlight in explorer (use display path for tree navigation)
      expandFoldersForFile(displayPath)
      highlightFile(displayPath)

      // 3. Select file and start loading content (use resolvedPath for API)
      setSelectedFile({ name: fileName, path: resolvedPath })
      setLoadingFileContent(true)

      if (isViewableBinary) {
        const response = await workspaceApi.get(
          `/api/documents/${encodeURIComponent(resolvedPath)}`,
          { params: { download: 'true' }, responseType: 'arraybuffer' }
        )
        setBinaryFileData(response.data as ArrayBuffer)
        setFileContent('')
        setShowFileContent(true)
        setLoadingFileContent(false)
        if (onLinkClick) {
          onLinkClick(resolvedPath)
        }
        return
      }

      // Text or image file — workspace API returns base64 data URL for images
      setBinaryFileData(null)
      const response = await agentApi.getPlannerFileContent(resolvedPath)
      if (response.success && response.data) {
        // Guard against undefined/null content (matches Workspace.tsx logic)
        if (response.data.content === undefined || response.data.content === null) {
          setFileContent('')
          setShowFileContent(true)
          setLoadingFileContent(false)
          if (onLinkClick) {
            onLinkClick(resolvedPath)
          }
          return
        }

        let content = typeof response.data.content === 'string'
          ? response.data.content
          : String(response.data.content)

        // Images arrive as base64 data URLs — no processing needed
        if (!response.data.is_image) {
          content = content.replace(/\\n/g, '\n').replace(/\\t/g, '\t').replace(/\\r/g, '\r')
        }

        setFileContent(content || '')
        setShowFileContent(true)
        if (onLinkClick) {
          onLinkClick(resolvedPath)
        }
      }
    } catch (error) {
      console.error('[MarkdownRenderer] Failed to open workspace link:', error)
    } finally {
      setLoadingFileContent(false)
    }
  }, [
    expandFoldersForFile,
    getActiveWorkflowFolderPath,
    getWorkspaceDisplayPath,
    highlightFile,
    setBinaryFileData,
    setFileContent,
    setLoadingFileContent,
    setSelectedFile,
    setShowFileContent,
    setWorkspaceMinimized,
    onLinkClick,
  ])

  const resolveWorkspaceTargetForView = useCallback((href?: string | null): { filepath: string; displayPath: string } | null => {
    const workspaceTarget = resolveWorkspaceHref(href)
    if (!workspaceTarget) return null
    return onWorkspaceLinkResolve?.(workspaceTarget.filepath, workspaceTarget.displayPath) || workspaceTarget
  }, [onWorkspaceLinkResolve, resolveWorkspaceHref])

  const openResolvedWorkspaceTarget = useCallback((workspaceTarget: { filepath: string; displayPath: string }) => {
    const handled = onWorkspaceLinkClick?.(workspaceTarget.filepath, workspaceTarget.displayPath)
    if (handled === true) return
    handleWorkspaceLink(workspaceTarget.filepath, workspaceTarget.displayPath)
  }, [handleWorkspaceLink, onWorkspaceLinkClick])

  const handleMarkdownClickCapture = useCallback((event: React.MouseEvent<HTMLDivElement>) => {
    const target = event.target
    if (!(target instanceof Element)) return

    const anchor = target.closest('a')
    if (!anchor) return

    const href = anchor.getAttribute('href') || anchor.href
    const workspaceTarget = resolveWorkspaceTargetForView(href)
    if (!workspaceTarget) return

    event.preventDefault()
    event.stopPropagation()
    openResolvedWorkspaceTarget(workspaceTarget)
  }, [openResolvedWorkspaceTarget, resolveWorkspaceTargetForView])

  const processedContent = React.useMemo(() => {
    if (!content) return ""
    let processed = normalizeMarkdownContent(content)

    // 2. Auto-link workspace paths (skip when disablePathLinking is true)
    if (disablePathLinking) return processed
    // Regex matches paths starting with specific prefixes: Chats/, Downloads/, Workflow/, skills/
    // Optionally matches surrounding backticks to unwrap them
    // CRITICAL FIX: We must handle paths wrapped in backticks (e.g., `Chats/foo.md`) because LLMs often format them as code.
    // The regex captures:
    // Group 1: Optional opening backtick
    // Group 2: The actual path (starting with allowed prefixes)
    // \1: Matches the closing backtick if Group 1 matched (ensuring balanced quotes)
    const pathRegex = /(`?)\b((?:_users\/[\w.-]+\/(?:Chats|memories)|Chats|Downloads|Workflow|skills|knowledgebase|learnings)\/(?:[\w\-./]+(?:[ ]+[\w\-./]+)*\.\w+|[\w\-./]+))\1/g
    
    // Replace with custom link protocol "#workspace/" to avoid sanitization issues
    // CHALLENGE 1: ReactMarkdown sanitizes unknown protocols like "workspace://", stripping the href.
    // SOLUTION: Use "#workspace/" (hash-based) which is always allowed, then intercept it in the <a> tag renderer.
    // CHALLENGE 2: `replace` callback arguments in arrow functions vs `arguments` object.
    // SOLUTION: Use explicit `...args` rest parameter to reliably capture `offset` and `string` regardless of capture groups.
    processed = processed.replace(pathRegex, (match, backtick, path, ...args) => {
      try {
        // args contains [offset, string] because we have 2 capture groups
        let str = args[args.length - 1] as string
        let offset = args[args.length - 2] as number
        
        // Fallback for weird argument passing
        if (typeof str !== 'string') {
           const foundStr = args.find(a => typeof a === 'string')
           if (foundStr) str = foundStr
        }
        if (typeof offset !== 'number') {
           const foundOffset = args.find(a => typeof a === 'number')
           if (foundOffset !== undefined) offset = foundOffset
        }

        if (typeof str !== 'string' || typeof offset !== 'number') {
           console.error('[MarkdownWorkspace] Invalid args in replace callback:', args)
           return match
        }
        
        const before = str.substring(Math.max(0, offset - 1), offset)
        const after = str.substring(offset + match.length, offset + match.length + 1)
        
        // If capture group 1 (backtick) is present, we are unwrapping a `path` span, so proceed.
        // If capture group 1 is empty, check if we are inside a code span or other formatting.
        if (!backtick) {
           // Skip if path is inside an inline code span: count backticks before this offset.
           // An odd count means the path is inside an open backtick span (e.g. `cat skills/foo.md`).
           // Fenced blocks (``` ... ```) contribute 3 backticks each — this heuristic handles both.
           const backticksBefore = (str.substring(0, offset).match(/`/g) || []).length
           if (backticksBefore % 2 === 1) {
             return match
           }
           if (before === '`' || before === '[' || before === '(' || before === '*' || before === '/' || after === ']' || after === '`' || after === '*') {
             return match
           }
        }
        
        // Handle trailing punctuation and spaces from the PATH (capture group 2)
        let cleanPath = path
        let suffix = ''
        
        // Trim trailing spaces, dots, commas, semi-colons
        const trimmed = cleanPath.replace(/[.,; ]+$/, '')
        if (trimmed !== cleanPath) {
           suffix = cleanPath.substring(trimmed.length)
           cleanPath = trimmed
        }
        
        // Use hash-based link to avoid protocol sanitization issues
        const result = `[${cleanPath}](#workspace/${encodeURIComponent(cleanPath)})${suffix}`
        return result
      } catch (err) {
        console.error('[MarkdownWorkspace] Error in regex replace:', err)
        return match
      }
    })

    if (basePath) {
      const relativeFileRegex = new RegExp(
        `(\`?)\\b((?:\\.{1,2}/)?(?:[\\w.-]+/)*[\\w.-]+\\.(${linkableWorkspaceFileExtensionPattern}))\\1`,
        'gi'
      )

      processed = processed.replace(relativeFileRegex, (match, backtick, path, _extension, ...args) => {
        try {
          let str = args[args.length - 1] as string
          let offset = args[args.length - 2] as number

          if (typeof str !== 'string') {
            const foundStr = args.find(a => typeof a === 'string')
            if (foundStr) str = foundStr
          }
          if (typeof offset !== 'number') {
            const foundOffset = args.find(a => typeof a === 'number')
            if (foundOffset !== undefined) offset = foundOffset
          }
          if (typeof str !== 'string' || typeof offset !== 'number') return match

          const before = str.substring(Math.max(0, offset - 1), offset)
          const after = str.substring(offset + match.length, offset + match.length + 1)
          const linePrefix = str.substring(str.lastIndexOf('\n', offset - 1) + 1, offset)
          const lastOpenParen = linePrefix.lastIndexOf('(')
          const lastCloseParen = linePrefix.lastIndexOf(')')
          const insideMarkdownLinkDestination =
            lastOpenParen > lastCloseParen && linePrefix.slice(0, lastOpenParen).endsWith(']')

          if (!backtick) {
            const backticksBefore = (str.substring(0, offset).match(/`/g) || []).length
            if (backticksBefore % 2 === 1) return match
            if (
              insideMarkdownLinkDestination ||
              linePrefix.includes('#workspace/') ||
              before === '[' ||
              before === '(' ||
              before === '/' ||
              before === '#' ||
              before === ':' ||
              before === '@' ||
              before === '%' ||
              after === ']' ||
              after === ')'
            ) {
              return match
            }
          }

          if (hasWorkspacePrefix(path) || hasExplicitUrlProtocol(path)) return match

          let cleanPath = path
          let suffix = ''
          const trimmed = cleanPath.replace(/[.,; ]+$/, '')
          if (trimmed !== cleanPath) {
            suffix = cleanPath.substring(trimmed.length)
            cleanPath = trimmed
          }

          return `[${cleanPath}](#workspace/${encodeURIComponent(resolveRelativeWorkspacePath(cleanPath, basePath))})${suffix}`
        } catch (err) {
          console.error('[MarkdownWorkspace] Error in relative path replace:', err)
          return match
        }
      })
    }
    
    return processed
  }, [basePath, content, disablePathLinking])

  return (
    <div
      className={`${containerClasses} ${scrollClasses} markdown-content`}
      style={containerStyle}
      onClickCapture={handleMarkdownClickCapture}
    >
      <style dangerouslySetInnerHTML={{
        __html: `
          .markdown-content ul,
          .markdown-content ol {
            list-style-position: outside;
            margin-left: 0;
            padding-left: 1.5rem;
          }
          .markdown-content ul {
            list-style-type: disc;
          }
          .markdown-content ol {
            list-style-type: decimal;
          }
          .markdown-content li {
            padding-left: 0.25rem;
          }
          .markdown-content ul ul {
            padding-left: 1.25rem;
            margin-top: 0.25rem;
            margin-bottom: 0.25rem;
          }
          .markdown-content ul ul ul {
            padding-left: 1.25rem;
            margin-top: 0.125rem;
            margin-bottom: 0.125rem;
          }
          .markdown-content ol ol {
            padding-left: 1.25rem;
            margin-top: 0.25rem;
            margin-bottom: 0.25rem;
          }
          .markdown-content ol ol ol {
            padding-left: 1.25rem;
            margin-top: 0.125rem;
            margin-bottom: 0.125rem;
          }
          .markdown-content li > p {
            margin-top: 0.25rem;
            margin-bottom: 0.25rem;
            display: block;
          }
          .markdown-content li > p:first-child {
            margin-top: 0;
          }
          .markdown-content li > p:last-child {
            margin-bottom: 0;
          }
          .markdown-content li ul,
          .markdown-content li ol {
            padding-left: 1.25rem;
            margin-top: 0.25rem;
            margin-bottom: 0.25rem;
          }
          /* Conversation answers need prose rhythm rather than the denser
             operational typography used by reports and tool output. */
          .conversation-markdown p,
          .conversation-markdown li {
            font-size: 0.9375rem;
            line-height: 1.75rem;
          }
          .conversation-markdown p {
            margin-bottom: 0.875rem;
          }
          .conversation-markdown ul,
          .conversation-markdown ol {
            margin-top: 0.625rem;
            margin-bottom: 1rem;
          }
          .conversation-markdown li + li {
            margin-top: 0.375rem;
          }
          .conversation-markdown :not(pre) > code {
            padding: 0.06rem 0.22rem;
            font-size: 0.82em;
            font-weight: 450;
            line-height: inherit;
            color: rgb(55 91 124) !important;
            background-color: rgb(15 23 42 / 0.055) !important;
            border: 0 !important;
            border-radius: 0.2rem !important;
          }
          .dark .conversation-markdown :not(pre) > code,
          .dark-plus .conversation-markdown :not(pre) > code {
            color: rgb(158 190 220) !important;
            background-color: rgb(255 255 255 / 0.07) !important;
          }
          /* Override prose table styles for dark theme */
          .markdown-content.prose table tbody tr {
            background-color: transparent !important;
          }
          .dark .markdown-content.prose table tbody tr,
          .dark-plus .markdown-content.prose table tbody tr {
            background-color: transparent !important;
          }
          .markdown-content.prose table tbody tr:hover {
            background-color: rgb(249 250 251) !important;
          }
          .dark .markdown-content.prose table tbody tr:hover,
          .dark-plus .markdown-content.prose table tbody tr:hover {
            background-color: rgb(31 41 55) !important;
          }
          .markdown-content.prose table tbody tr:focus,
          .markdown-content.prose table tbody tr:active,
          .markdown-content.prose table tbody tr.selected {
            background-color: rgb(249 250 251) !important;
          }
          .dark .markdown-content.prose table tbody tr:focus,
          .dark .markdown-content.prose table tbody tr:active,
          .dark .markdown-content.prose table tbody tr.selected,
          .dark-plus .markdown-content.prose table tbody tr:focus,
          .dark-plus .markdown-content.prose table tbody tr:active,
          .dark-plus .markdown-content.prose table tbody tr.selected {
            background-color: rgb(31 41 55) !important;
          }
          /* Ensure text color is correct in dark mode for hover/selected rows */
          .dark .markdown-content.prose table tbody tr:hover td,
          .dark .markdown-content.prose table tbody tr:focus td,
          .dark .markdown-content.prose table tbody tr:active td,
          .dark .markdown-content.prose table tbody tr.selected td,
          .dark-plus .markdown-content.prose table tbody tr:hover td,
          .dark-plus .markdown-content.prose table tbody tr:focus td,
          .dark-plus .markdown-content.prose table tbody tr:active td,
          .dark-plus .markdown-content.prose table tbody tr.selected td {
            color: rgb(243 244 246) !important;
          }
          /* Override any prose-invert styles that might be setting light backgrounds */
          .dark .markdown-content.prose-invert table tbody tr:hover,
          .dark-plus .markdown-content.prose-invert table tbody tr:hover {
            background-color: rgb(31 41 55) !important;
          }
          .dark .markdown-content.prose-invert table tbody tr:focus,
          .dark .markdown-content.prose-invert table tbody tr:active,
          .dark .markdown-content.prose-invert table tbody tr.selected,
          .dark-plus .markdown-content.prose-invert table tbody tr:focus,
          .dark-plus .markdown-content.prose-invert table tbody tr:active,
          .dark-plus .markdown-content.prose-invert table tbody tr.selected {
            background-color: rgb(31 41 55) !important;
          }
          /* Prevent browser text selection from causing light background issues */
          .dark .markdown-content.prose table tbody tr::selection,
          .dark-plus .markdown-content.prose table tbody tr::selection {
            background-color: rgb(59 130 246) !important;
            color: rgb(255 255 255) !important;
          }
          /* Ensure table cells maintain proper colors */
          .dark .markdown-content.prose table tbody td,
          .dark-plus .markdown-content.prose table tbody td {
            color: rgb(243 244 246) !important;
          }
          .dark .markdown-content.prose-invert table tbody td,
          .dark-plus .markdown-content.prose-invert table tbody td {
            color: rgb(243 244 246) !important;
          }
        `
      }} />
      <ReactMarkdown
        // Single-tilde strikethrough off: chat/model text uses ~ for
        // "approximately" (~2.37L), and pairing those into <del> spans
        // strikes whole paragraphs. ~~ still strikethroughs, like GitHub.
        remarkPlugins={[[remarkGfm, { singleTilde: false }]]}
        components={{
          p: ({ children }) => <p className="mb-2 last:mb-0 text-sm leading-6 text-gray-700 dark:text-gray-300 break-words overflow-wrap-anywhere">{children}</p>,
          h1: ({ children }) => <h1 className="text-2xl font-bold mb-2 mt-4 first:mt-0 text-gray-900 dark:text-gray-100 break-words overflow-wrap-anywhere border-b border-gray-200 dark:border-gray-700 pb-2">{children}</h1>,
          h2: ({ children }) => <h2 className="text-xl font-semibold mb-2 mt-3 first:mt-0 text-gray-900 dark:text-gray-100 break-words overflow-wrap-anywhere">{children}</h2>,
          h3: ({ children }) => <h3 className="text-lg font-semibold mb-1.5 mt-2 first:mt-0 text-gray-900 dark:text-gray-100 break-words overflow-wrap-anywhere">{children}</h3>,
          h4: ({ children }) => <h4 className="text-base font-semibold mb-1.5 mt-2 first:mt-0 text-gray-900 dark:text-gray-100 break-words overflow-wrap-anywhere">{children}</h4>,
          ul: ({ children }) => <ul className="list-disc list-outside mb-2 space-y-1 pl-6 min-w-0">{children}</ul>,
          ol: ({ children }) => <ol className="list-decimal list-outside mb-2 space-y-1 pl-6 min-w-0">{children}</ol>,
          li: ({ children }) => <li className="pl-1 text-sm break-words overflow-wrap-anywhere leading-6 text-gray-700 dark:text-gray-300">{children}</li>,
          code: ({ className, children, inline, ...props }: React.HTMLAttributes<HTMLElement> & { inline?: boolean }) => {
            const match = /language-(\w+)/.exec(className || '')
            const isInline = typeof inline === 'boolean' ? inline : false
            
            // ```report-widget — embed a live report widget from a JSON spec.
            // Only active when the report viewer passes renderEmbeddedWidget;
            // otherwise it falls through to normal code rendering.
            if (!isInline && match && match[1] === 'report-widget' && renderEmbeddedWidget) {
              const raw = String(children).replace(/\n$/, '')
              try {
                const spec = JSON.parse(raw)
                return <div className="my-3">{renderEmbeddedWidget(spec)}</div>
              } catch {
                return (
                  <pre className="my-3 rounded-lg border border-amber-300/50 bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:border-amber-500/30 dark:bg-amber-950/30 dark:text-amber-400">
                    Invalid report-widget JSON:{'\n'}{raw}
                  </pre>
                )
              }
            }

            if (!isInline && match && match[1] === 'tool-definition') {
              return <ToolDefinition content={String(children).replace(/\n$/, '')} />
            }

            if (!isInline && match && match[1] === 'mermaid') {
              return <MermaidDiagram content={String(children).replace(/\n$/, '')} />
            }

            // For inline code, use simple styling
            if (isInline || !match) {
              return (
                <code className="bg-gray-100 dark:bg-gray-800 px-1.5 py-0.5 rounded text-xs font-mono break-all overflow-wrap-anywhere text-blue-600 dark:text-blue-400" {...props}>
                  {children}
                </code>
              )
            }
            
            // For code blocks, use SyntaxHighlighter
            const language = match[1]
            const codeString = String(children).replace(/\n$/, '')

            if (['text', 'txt', 'plain', 'plaintext', 'terminal'].includes(language.toLowerCase())) {
              return (
                <pre className="my-3 max-w-full overflow-x-auto whitespace-pre-wrap break-words text-xs leading-5 text-gray-700 dark:text-gray-300">
                  {codeString}
                </pre>
              )
            }
            
            // Detect dark mode
            const isDark = document.documentElement.classList.contains('dark') || 
                          document.documentElement.classList.contains('dark-plus')
            
            return (
              <div className="my-4 min-w-0 max-w-full overflow-x-auto rounded-lg border border-gray-200 dark:border-gray-700 shadow-sm">
                <Suspense
                  fallback={(
                    <pre className="m-0 overflow-x-auto p-4 text-xs leading-6 text-gray-700 dark:text-gray-300">
                      {codeString}
                    </pre>
                  )}
                >
                  <SyntaxHighlightedCode code={codeString} language={language} isDark={isDark} />
                </Suspense>
              </div>
            )
          },
          pre: ({ children }) => {
            // Pre tag is handled by the code component above
            // This is a fallback for any pre tags that don't contain code
            return (
              <pre className="bg-gray-100 dark:bg-gray-800 p-2 rounded text-xs font-mono overflow-x-auto break-all min-w-0">
                {children}
              </pre>
            )
          },
          blockquote: ({ children }) => (
            <blockquote className="border-l-4 border-blue-500 dark:border-blue-400 pl-4 py-1.5 my-2 bg-blue-50 dark:bg-blue-900/20 rounded-r text-sm break-words overflow-wrap-anywhere text-gray-700 dark:text-gray-300 italic">
              {children}
            </blockquote>
          ),
          strong: ({ children }) => <strong className="font-semibold break-words overflow-wrap-anywhere text-gray-900 dark:text-gray-100">{children}</strong>,
          em: ({ children }) => <em className="italic break-words overflow-wrap-anywhere">{children}</em>,
          a: ({ href, children }) => {
            const workspaceTarget = untrustedContent ? null : resolveWorkspaceTargetForView(href)
            const externalHref = resolveSafeExternalHref(href)

            if (workspaceTarget) {
              if (workspaceLinkHref) return <a href={workspaceLinkHref(workspaceTarget.filepath)} className="text-blue-600 dark:text-blue-400 underline">{children}</a>
              return (
                <a
                  href={`#workspace/${encodeURIComponent(workspaceTarget.displayPath)}`}
                  onClick={(e) => {
                    e.preventDefault()
                    e.stopPropagation()
                    openResolvedWorkspaceTarget(workspaceTarget)
                  }}
                  style={{ pointerEvents: 'auto', cursor: 'pointer' }}
                  className="text-purple-600 dark:text-purple-400 hover:text-purple-800 dark:hover:text-purple-300 underline cursor-pointer break-words overflow-wrap-anywhere font-medium transition-colors"
                  title={`Open ${workspaceTarget.filepath} in workspace`}
                >
                  {children}
                </a>
              )
            }
            if (!externalHref) {
              return (
                <span
                  className="text-gray-600 dark:text-gray-300 break-words overflow-wrap-anywhere"
                  title={href ? `Unsupported link: ${href}` : undefined}
                >
                  {children}
                </span>
              )
            }
            return (
              <a
                href={externalHref}
                target="_blank"
                rel="noopener noreferrer"
                onClick={(e) => {
                  if (openExternalHref(externalHref)) {
                    e.preventDefault()
                    e.stopPropagation()
                  }
                }}
                className="text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-300 underline break-words overflow-wrap-anywhere"
              >
                {children}
              </a>
            )
          },
          img: ({ src, alt }) => {
            if (untrustedContent) return <span className="text-muted-foreground">[Image{alt ? `: ${alt}` : ''}]</span>
            const workspacePrefixes = ['Chats/', 'Downloads/', 'skills/', 'Workflow/', 'knowledgebase/', '_users/', 'learnings/']
            const workspaceFilepath = resolveWorkspaceHref(src)?.filepath || (src && workspacePrefixes.some(p => src.startsWith(p)) ? src : null)
            const resolvedSrc = workspaceFilepath
              ? sharedLink(window.location.origin, 'file', workspaceFilepath)
              : src
            console.log(`[IMAGE_RENDER] MarkdownRenderer src="${src}" workspaceFilepath="${workspaceFilepath}" resolvedSrc="${resolvedSrc}"`)
            const image = workspaceFilepath ? <WorkspaceImage path={workspaceFilepath} alt={alt} className="max-w-full h-auto max-h-[75vh] rounded-lg my-3" /> : (
              <img
                src={resolvedSrc}
                alt={alt}
                className={compactImages
                  ? "max-w-full w-auto h-auto max-h-80 rounded-lg shadow-md my-3 border border-gray-200 dark:border-gray-700 cursor-zoom-in"
                  : "max-w-full h-auto rounded-lg shadow-md my-4 border border-gray-200 dark:border-gray-700"}
              />
            )
            if (!compactImages || !resolvedSrc) return image
            return (
              <a
                href={workspaceFilepath ? sharedLink(window.location.origin, 'file', workspaceFilepath) : resolvedSrc}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-block max-w-full"
                title="Open full-size image"
                aria-label={`Open full-size image${alt ? `: ${alt}` : ''}`}
              >
                {image}
              </a>
            )
          },
          hr: () => <hr className="my-6 border-gray-300 dark:border-gray-600" />,
          table: ({ children }) => (
            <div className="overflow-x-auto my-6 min-w-0 rounded-lg border border-gray-200 dark:border-gray-700 shadow-sm">
              <table className="min-w-full border-collapse">
                {children}
              </table>
            </div>
          ),
          thead: ({ children }) => (
            <thead className="bg-gray-50 dark:bg-gray-800 border-b border-gray-200 dark:border-gray-700">
              {children}
            </thead>
          ),
          tbody: ({ children }) => (
            <tbody className="bg-white dark:bg-gray-900 divide-y divide-gray-200 dark:divide-gray-700">
              {children}  
            </tbody>
          ),
          tr: ({ children }) => (
            <tr className="transition-colors [tbody_&:hover]:bg-gray-50 dark:[tbody_&:hover]:bg-gray-800 [tbody_&:focus]:bg-gray-50 dark:[tbody_&:focus]:bg-gray-800 [tbody_&:active]:bg-gray-100 dark:[tbody_&:active]:bg-gray-700">
              {children}
            </tr>
          ),
          th: ({ children }) => (
            <th className="px-4 py-3 text-left text-xs font-semibold text-gray-700 dark:text-gray-300 uppercase tracking-wider break-words overflow-wrap-anywhere">
              {children}
            </th>
          ),
          td: ({ children }) => (
            <td className="px-4 py-3 text-sm text-gray-900 dark:text-gray-100 break-words overflow-wrap-anywhere [tr:hover_&]:text-gray-900 [tr:hover_&]:dark:text-gray-100 [tr:focus_&]:text-gray-900 [tr:focus_&]:dark:text-gray-100 [tr:active_&]:text-gray-900 [tr:active_&]:dark:text-gray-100">
              {children}
            </td>
          ),
        }}
      >
        {processedContent}
      </ReactMarkdown>
    </div>
  )
}

// Memoized because this component REMOUNTS its output rather than re-rendering
// it. The `components` map handed to ReactMarkdown is an object literal built
// inline on every render, so each render produces a new function identity for
// `p`, `h1`, … — React sees a different component *type* for every markdown
// node and therefore unmounts the old subtree and mounts a fresh one instead of
// reconciling it. In a polling chat surface that repeats several times a second
// for every message on screen: measured 120 DOM mutations in 6s on an idle
// conversation, visible as tool cards flashing and re-opening on their own.
//
// Memo fixes it at the boundary that matters — identical props now skip the
// render entirely, so the map is never rebuilt. Callers passing inline
// onLinkClick/renderEmbeddedWidget closures still re-render (their props really
// do change each time); the conversation surfaces pass only primitives.
export const MarkdownRenderer = React.memo(MarkdownRendererImpl)

// Specialized versions for different event types
export const LLMMarkdownRenderer: React.FC<{ content: string; maxHeight?: string }> = ({ content, maxHeight = "600px" }) => (
  <div className={`border border-gray-200 dark:border-gray-700 rounded-md bg-white dark:bg-gray-800 overflow-y-auto overflow-x-hidden min-w-0`} style={{ maxHeight }}>
    <div className="p-3 min-w-0">
      <MarkdownRenderer content={content} />
    </div>
  </div>
)

export const ToolMarkdownRenderer: React.FC<{ content: string; maxHeight?: string }> = ({ content, maxHeight = "400px" }) => (
  <div className={`border border-gray-200 dark:border-gray-700 rounded-md bg-white dark:bg-gray-800 overflow-y-auto overflow-x-hidden min-w-0`} style={{ maxHeight }}>
    <div className="p-3 min-w-0">
      <MarkdownRenderer content={content} />
    </div>
  </div>
)

export const SystemMarkdownRenderer: React.FC<{ content: string; maxHeight?: string }> = ({ content, maxHeight = "256px" }) => (
  <div className={`border border-gray-200 dark:border-gray-700 rounded-md bg-white dark:bg-gray-800 overflow-y-auto overflow-x-hidden min-w-0`} style={{ maxHeight }}>
    <div className="p-3 min-w-0">
      <MarkdownRenderer content={content} />
    </div>
  </div>
)

// Memoized for the same reason as MarkdownRenderer above — this is the wrapper
// every chat surface renders per message, so it is the one that multiplies.
export const ConversationMarkdownRenderer: React.FC<{ content: string; maxHeight?: string; disablePathLinking?: boolean; framed?: boolean }> = React.memo(({ content, maxHeight = "384px", disablePathLinking, framed = true }) => (
  <div
    className={`${framed ? 'border border-gray-200 dark:border-gray-700 rounded-md bg-white dark:bg-gray-800' : ''} overflow-y-auto overflow-x-hidden min-w-0`}
    style={{ maxHeight }}
  >
    <div className={`${framed ? 'p-3' : 'p-0'} min-w-0`}>
      <MarkdownRenderer content={content} className="conversation-markdown" disablePathLinking={disablePathLinking} compactImages />
    </div>
  </div>
))
