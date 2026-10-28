import { useMemo } from 'react'
import { useTheme } from '../../hooks/useTheme'

export type DiffLineAnchor = { side: 'before' | 'after'; line: number }

interface DiffRendererProps {
  onSelectLine?: (anchor: DiffLineAnchor) => void
  content: string
  className?: string
  maxHeightClassName?: string
}

type DiffLineKind =
  | 'git-header'
  | 'file-header'
  | 'hunk'
  | 'addition'
  | 'deletion'
  | 'context'
  | 'meta'

interface ParsedDiffLine {
  content: string
  kind: DiffLineKind
  anchor?: DiffLineAnchor
}

interface DiffPalette {
  container: string
  toolbar: string
  stats: {
    additions: string
    deletions: string
    hunks: string
  }
  rowBorder: string
  markerBorder: string
  lineClasses: Record<DiffLineKind, string>
  markerClasses: Record<DiffLineKind, string>
}

const classifyDiffLine = (line: string): DiffLineKind => {
  if (line.startsWith('diff --git ') || line.startsWith('index ')) {
    return 'git-header'
  }
  if (line.startsWith('--- ') || line.startsWith('+++ ')) {
    return 'file-header'
  }
  if (line.startsWith('@@')) {
    return 'hunk'
  }
  if (line.startsWith('+') && !line.startsWith('+++')) {
    return 'addition'
  }
  if (line.startsWith('-') && !line.startsWith('---')) {
    return 'deletion'
  }
  if (line.startsWith(' ')) {
    return 'context'
  }
  return 'meta'
}

const lightPalette: DiffPalette = {
  container: 'border-gray-200 bg-white',
  toolbar: 'border-gray-200 bg-gray-50/80',
  stats: {
    additions: 'bg-diff-add/10 text-diff-add',
    deletions: 'bg-diff-del/10 text-diff-del',
    hunks: 'bg-sky-100 text-sky-800',
  },
  rowBorder: 'border-gray-100',
  markerBorder: 'border-gray-200',
  lineClasses: {
    'git-header': 'bg-violet-50 text-violet-700',
    'file-header': 'bg-amber-50 text-amber-700',
    'hunk': 'bg-sky-50 text-sky-700',
    'addition': 'bg-diff-add/10 text-diff-add',
    'deletion': 'bg-diff-del/10 text-diff-del',
    'context': 'bg-white text-gray-700',
    'meta': 'bg-gray-50/60 text-gray-600',
  },
  markerClasses: {
    'git-header': 'text-violet-500',
    'file-header': 'text-amber-500',
    'hunk': 'text-sky-500',
    'addition': 'text-diff-add',
    'deletion': 'text-diff-del',
    'context': 'text-gray-400',
    'meta': 'text-gray-400',
  },
}

const darkPalette: DiffPalette = {
  container: 'border-gray-700 bg-gray-900',
  toolbar: 'border-gray-700 bg-gray-800/80',
  stats: {
    additions: 'bg-diff-add/10 text-diff-add',
    deletions: 'bg-diff-del/10 text-diff-del',
    hunks: 'bg-sky-950/50 text-sky-200',
  },
  rowBorder: 'border-gray-800',
  markerBorder: 'border-gray-800',
  lineClasses: {
    'git-header': 'bg-violet-950/30 text-violet-300',
    'file-header': 'bg-amber-950/30 text-amber-300',
    'hunk': 'bg-sky-950/30 text-sky-300',
    'addition': 'bg-diff-add/10 text-diff-add',
    'deletion': 'bg-diff-del/10 text-diff-del',
    'context': 'bg-gray-900 text-gray-300',
    'meta': 'bg-gray-900/80 text-gray-400',
  },
  markerClasses: {
    'git-header': 'text-violet-300',
    'file-header': 'text-amber-300',
    'hunk': 'text-sky-300',
    'addition': 'text-diff-add',
    'deletion': 'text-diff-del',
    'context': 'text-gray-500',
    'meta': 'text-gray-500',
  },
}

export function DiffRenderer({
  content,
  onSelectLine,
  className = '',
  maxHeightClassName = 'max-h-[70vh]',
}: DiffRendererProps) {
  const { theme } = useTheme()
  const lines = useMemo<ParsedDiffLine[]>(() => {
    let before = 0, after = 0
    return content.split('\n').map(content => {
      const kind = classifyDiffLine(content)
      const hunk = content.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/)
      if (hunk) { before = Number(hunk[1]); after = Number(hunk[2]) }
      let anchor: DiffLineAnchor | undefined
      if (kind === 'deletion' && before > 0) anchor = { side: 'before', line: before++ }
      if (kind === 'addition' && after > 0) anchor = { side: 'after', line: after++ }
      if (kind === 'context' && after > 0) { anchor = { side: 'after', line: after++ }; before++ }
      return { content, kind, anchor }
    })
  }, [content])
  const palette = theme === 'dark' ? darkPalette : lightPalette

  const stats = useMemo(() => {
    return lines.reduce((acc, line) => {
      if (line.kind === 'addition') acc.additions += 1
      if (line.kind === 'deletion') acc.deletions += 1
      if (line.kind === 'hunk') acc.hunks += 1
      return acc
    }, { additions: 0, deletions: 0, hunks: 0 })
  }, [lines])

  return (
    <div className={`flex flex-col rounded-lg border ${palette.container} ${className}`}>
      <div className={`flex items-center gap-2 border-b px-3 py-2 text-xs ${palette.toolbar}`}>
        <span className={`rounded px-2 py-0.5 font-medium ${palette.stats.additions}`}>
          +{stats.additions}
        </span>
        <span className={`rounded px-2 py-0.5 font-medium ${palette.stats.deletions}`}>
          -{stats.deletions}
        </span>
        <span className={`rounded px-2 py-0.5 font-medium ${palette.stats.hunks}`}>
          {stats.hunks} sections
        </span>
      </div>

      <div className={`min-h-0 flex-1 overflow-auto ${maxHeightClassName}`}>
        <div className="min-w-full font-mono text-xs">
          {lines.map((line, index) => {
            const marker = line.content.length > 0 ? line.content[0] : ' '

            return (
              <div
                key={`${index}-${line.content}`}
                className={`grid grid-cols-[3rem_1fr] items-start border-b ${palette.rowBorder} ${palette.lineClasses[line.kind]}`}
              >
                <div className={`select-none border-r px-2 py-1 text-right ${palette.markerBorder} ${palette.markerClasses[line.kind]}`}>
                  {onSelectLine && line.anchor ? <button type="button" aria-label={`Comment on ${line.anchor.side} line ${line.anchor.line}`} className="underline" onClick={() => onSelectLine(line.anchor!)}>{line.anchor.line}</button> : marker}
                </div>
                <pre className="overflow-x-auto whitespace-pre-wrap break-words px-3 py-1 leading-5">
                  {line.content}
                </pre>
              </div>
            )
          })}
        </div>
      </div>
    </div>
  )
}

export default DiffRenderer
