import { useMemo, useState } from 'react'
import { useTheme } from '../../hooks/useTheme'
import { useMobileViewport } from '../../hooks/useMobileViewport'

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
  before?: number
  after?: number
  anchor?: DiffLineAnchor
}

type SplitRow = { full: ParsedDiffLine } | { left?: ParsedDiffLine; right?: ParsedDiffLine }

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

const parseDiff = (content: string): ParsedDiffLine[] => {
  let before = 0, after = 0
  return content.split('\n').map(content => {
    const kind = classifyDiffLine(content)
    const hunk = content.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/)
    if (hunk) { before = Number(hunk[1]); after = Number(hunk[2]) }
    if (kind === 'deletion' && before > 0) { const line = before++; return { content, kind, before: line, anchor: { side: 'before', line } } }
    if (kind === 'addition' && after > 0) { const line = after++; return { content, kind, after: line, anchor: { side: 'after', line } } }
    if (kind === 'context' && after > 0) { const line = after++; return { content, kind, before: before++, after: line, anchor: { side: 'after', line } } }
    return { content, kind }
  })
}

const withoutWhitespace = (line: ParsedDiffLine) => line.content.slice(1).replace(/\s+/g, '')

/** Like `git diff -w`: a block of removed lines replaced by the same lines with
 * only whitespace changed is shown as unchanged (new side text and numbers). */
const hideWhitespaceChanges = (lines: ParsedDiffLine[]): ParsedDiffLine[] => {
  const result: ParsedDiffLine[] = []
  for (let i = 0; i < lines.length;) {
    if (lines[i].kind !== 'deletion') { result.push(lines[i++]); continue }
    let mid = i
    while (mid < lines.length && lines[mid].kind === 'deletion') mid++
    let end = mid
    while (end < lines.length && lines[end].kind === 'addition') end++
    const removed = lines.slice(i, mid), added = lines.slice(mid, end)
    if (removed.length === added.length && removed.every((line, n) => withoutWhitespace(line) === withoutWhitespace(added[n]))) {
      added.forEach((line, n) => result.push({ ...line, content: ` ${line.content.slice(1)}`, kind: 'context', before: removed[n].before }))
    } else result.push(...removed, ...added)
    i = end
  }
  return result
}

/** Side by side: removed lines pair with the added lines that follow them. */
const toSplitRows = (lines: ParsedDiffLine[]): SplitRow[] => {
  const rows: SplitRow[] = []
  for (let i = 0; i < lines.length;) {
    const line = lines[i]
    if (line.kind === 'context') { rows.push({ left: line, right: line }); i++; continue }
    if (line.kind !== 'deletion' && line.kind !== 'addition') { rows.push({ full: line }); i++; continue }
    const removed: ParsedDiffLine[] = [], added: ParsedDiffLine[] = []
    while (i < lines.length && lines[i].kind === 'deletion') removed.push(lines[i++])
    while (i < lines.length && lines[i].kind === 'addition') added.push(lines[i++])
    for (let n = 0; n < Math.max(removed.length, added.length); n++) rows.push({ left: removed[n], right: added[n] })
  }
  return rows
}

const readChoice = (key: string) => { try { return localStorage.getItem(key) === '1' } catch { return false } }
const saveChoice = (key: string, value: boolean) => { try { localStorage.setItem(key, value ? '1' : '0') } catch { /* per-browser convenience only */ } }
const SPLIT_KEY = 'diff-view:split'
const WHITESPACE_KEY = 'diff-view:hide-whitespace'

export function DiffRenderer({
  content,
  onSelectLine,
  className = '',
  maxHeightClassName = 'max-h-[70vh]',
}: DiffRendererProps) {
  const { theme } = useTheme()
  const phone = useMobileViewport()
  const [splitChoice, setSplitChoice] = useState(() => readChoice(SPLIT_KEY))
  const [hideWhitespace, setHideWhitespace] = useState(() => readChoice(WHITESPACE_KEY))
  const split = splitChoice && !phone
  const parsed = useMemo(() => parseDiff(content), [content])
  const lines = useMemo(() => hideWhitespace ? hideWhitespaceChanges(parsed) : parsed, [parsed, hideWhitespace])
  const splitRows = useMemo(() => split ? toSplitRows(lines) : [], [lines, split])
  const palette = theme === 'dark' ? darkPalette : lightPalette

  const stats = useMemo(() => {
    return lines.reduce((acc, line) => {
      if (line.kind === 'addition') acc.additions += 1
      if (line.kind === 'deletion') acc.deletions += 1
      if (line.kind === 'hunk') acc.hunks += 1
      return acc
    }, { additions: 0, deletions: 0, hunks: 0 })
  }, [lines])

  const toggle = (key: string, value: boolean, set: (value: boolean) => void) => { set(value); saveChoice(key, value) }
  const toggleClass = (on: boolean) => `rounded border px-2 py-0.5 ${on ? 'border-current font-medium' : `${palette.markerBorder} opacity-70`}`
  const lineNumber = (line: ParsedDiffLine, side: 'before' | 'after') => onSelectLine && line.anchor?.side === side
    ? <button type="button" aria-label={`Comment on ${side} line ${line.anchor.line}`} className="underline" onClick={() => onSelectLine(line.anchor!)}>{line.anchor.line}</button>
    : line[side]
  const cell = (line: ParsedDiffLine | undefined, side: 'before' | 'after') => line
    ? <div className={`grid min-w-0 grid-cols-[3rem_1fr] ${palette.lineClasses[line.kind]}`}>
      <div className={`select-none border-r px-2 py-1 text-right ${palette.markerBorder} ${palette.markerClasses[line.kind]}`}>{lineNumber(line, side)}</div>
      <pre className="overflow-x-auto whitespace-pre-wrap break-words px-3 py-1 leading-5">{line.content.slice(1)}</pre>
    </div>
    : <div className={palette.lineClasses.meta} />

  return (
    <div className={`flex flex-col rounded-lg border ${palette.container} ${className}`}>
      <div className={`flex flex-wrap items-center gap-2 border-b px-3 py-2 text-xs ${palette.toolbar}`}>
        <span className={`rounded px-2 py-0.5 font-medium ${palette.stats.additions}`}>
          +{stats.additions}
        </span>
        <span className={`rounded px-2 py-0.5 font-medium ${palette.stats.deletions}`}>
          -{stats.deletions}
        </span>
        <span className={`rounded px-2 py-0.5 font-medium ${palette.stats.hunks}`}>
          {stats.hunks} sections
        </span>
        <span className="ml-auto flex gap-2">
          {!phone && <button type="button" aria-pressed={splitChoice} className={toggleClass(splitChoice)} onClick={() => toggle(SPLIT_KEY, !splitChoice, setSplitChoice)}>Side by side</button>}
          <button type="button" aria-pressed={hideWhitespace} className={toggleClass(hideWhitespace)} onClick={() => toggle(WHITESPACE_KEY, !hideWhitespace, setHideWhitespace)}>Hide whitespace</button>
        </span>
      </div>

      <div className={`min-h-0 flex-1 overflow-auto ${maxHeightClassName}`}>
        <div className="min-w-full font-mono text-xs">
          {split ? splitRows.map((row, index) => 'full' in row
            ? <div key={index} className={`border-b px-3 py-1 ${palette.rowBorder} ${palette.lineClasses[row.full.kind]}`}><pre className="whitespace-pre-wrap break-words leading-5">{row.full.content}</pre></div>
            : <div key={index} data-split-row className={`grid grid-cols-2 border-b ${palette.rowBorder}`}>
              <div className={`min-w-0 border-r ${palette.markerBorder}`}>{cell(row.left, 'before')}</div>
              {cell(row.right, 'after')}
            </div>)
          : lines.map((line, index) => {
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
