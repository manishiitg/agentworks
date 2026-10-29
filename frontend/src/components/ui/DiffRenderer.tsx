import { lazy, Suspense, useMemo, useState, type ReactNode } from 'react'
import { useTheme } from '../../hooks/useTheme'
import { useMobileViewport } from '../../hooks/useMobileViewport'

const DiffCode = lazy(() => import('./DiffCode'))

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
  /** On a hunk line: the unchanged lines skipped before it. */
  skipped?: number
}

type SplitRow = { full: ParsedDiffLine } | { left?: ParsedDiffLine; right?: ParsedDiffLine }

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

// Like VS Code: rows are tinted, code keeps its syntax colours, headers are hidden.
const rowTint: Partial<Record<DiffLineKind, string>> = { addition: 'bg-diff-add/10', deletion: 'bg-diff-del/10' }
const gutterTint: Partial<Record<DiffLineKind, string>> = { addition: 'bg-diff-add/20 text-diff-add', deletion: 'bg-diff-del/20 text-diff-del' }
const LANGUAGES: Record<string, string> = {
  ts: 'typescript', tsx: 'tsx', js: 'javascript', jsx: 'jsx', mjs: 'javascript', cjs: 'javascript', go: 'go', py: 'python',
  json: 'json', md: 'markdown', css: 'css', scss: 'scss', html: 'markup', xml: 'markup', svg: 'markup', yml: 'yaml', yaml: 'yaml',
  sh: 'bash', bash: 'bash', zsh: 'bash', rs: 'rust', java: 'java', kt: 'kotlin', rb: 'ruby', sql: 'sql', swift: 'swift',
  c: 'c', h: 'c', cpp: 'cpp', cs: 'csharp', php: 'php', toml: 'toml', dockerfile: 'docker',
}
const languageOf = (lines: ParsedDiffLine[]) => {
  const header = lines.find(line => line.content.startsWith('+++ ') && !line.content.endsWith('/dev/null')) ?? lines.find(line => line.content.startsWith('--- '))
  const name = header?.content.slice(4).trim().replace(/^[ab]\//, '').split('/').pop()?.toLowerCase() ?? ''
  return LANGUAGES[name === 'dockerfile' ? name : name.split('.').pop() ?? ''] ?? ''
}
const MAX_HIGHLIGHTED_LINES = 3000

const parseDiff = (content: string): ParsedDiffLine[] => {
  let before = 0, after = 0
  return content.split('\n').map(content => {
    const kind = classifyDiffLine(content)
    const hunk = content.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@/)
    if (hunk) {
      const skipped = Number(hunk[2]) - Math.max(after, 1)
      before = Number(hunk[1]); after = Number(hunk[2])
      return { content, kind, skipped }
    }
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
  const language = useMemo(() => parsed.length <= MAX_HIGHLIGHTED_LINES ? languageOf(parsed) : '', [parsed])
  const lines = useMemo(() => (hideWhitespace ? hideWhitespaceChanges(parsed) : parsed).filter(line => line.kind !== 'git-header' && line.kind !== 'file-header' && !(line.kind === 'meta' && !line.content.trim())), [parsed, hideWhitespace])
  const splitRows = useMemo(() => split ? toSplitRows(lines) : [], [lines, split])
  const isDark = theme === 'dark'

  const stats = useMemo(() => {
    return lines.reduce((acc, line) => {
      if (line.kind === 'addition') acc.additions += 1
      if (line.kind === 'deletion') acc.deletions += 1
      return acc
    }, { additions: 0, deletions: 0 })
  }, [lines])

  const toggle = (key: string, value: boolean, set: (value: boolean) => void) => { set(value); saveChoice(key, value) }
  const toggleClass = (on: boolean) => `rounded px-2 py-0.5 ${on ? 'bg-muted font-medium text-foreground' : 'text-muted-foreground hover:text-foreground'}`
  const plain = (text: string) => <span>{text}</span>
  const code = (text: string): ReactNode => language && text.trim()
    ? <Suspense fallback={plain(text)}><DiffCode text={text} language={language} isDark={isDark} /></Suspense>
    : plain(text)
  const lineNumber = (line: ParsedDiffLine, side: 'before' | 'after') => onSelectLine && line.anchor?.side === side
    ? <button type="button" aria-label={`Comment on ${side} line ${line.anchor.line}`} className="hover:underline" onClick={() => onSelectLine(line.anchor!)}>{line.anchor.line}</button>
    : line[side]
  const gutter = (line: ParsedDiffLine, number: ReactNode) =>
    <div className={`select-none px-2 text-right tabular-nums ${gutterTint[line.kind] ?? 'text-muted-foreground/60'}`}>{number}</div>
  const text = (line: ParsedDiffLine) =>
    <pre className="min-w-0 whitespace-pre-wrap break-words px-3 text-foreground/90">{code(line.content.slice(1))}</pre>
  // A hunk header becomes a quiet "N unmodified lines" divider, as in VS Code.
  const divider = (line: ParsedDiffLine, key: number) =>
    <div key={key} className="select-none border-y border-border/50 bg-muted/40 py-0.5 text-center text-[11px] text-muted-foreground">
      {line.kind === 'hunk' ? (line.skipped ? `${line.skipped} unmodified ${line.skipped === 1 ? 'line' : 'lines'}` : '⋯') : line.content}
    </div>
  const cell = (line: ParsedDiffLine | undefined, side: 'before' | 'after') => line
    ? <div className={`grid min-w-0 grid-cols-[3.25rem_1fr] ${rowTint[line.kind] ?? ''}`}>
      {gutter(line, lineNumber(line, side))}
      {text(line)}
    </div>
    : <div className="bg-muted/30" />

  return (
    <div className={`flex flex-col overflow-hidden rounded-md border border-border bg-background ${className}`}>
      <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-1.5 text-xs">
        <span className="font-medium text-diff-add">+{stats.additions}</span>
        <span className="font-medium text-diff-del">-{stats.deletions}</span>
        <span className="ml-auto flex gap-1">
          {!phone && <button type="button" aria-pressed={splitChoice} className={toggleClass(splitChoice)} onClick={() => toggle(SPLIT_KEY, !splitChoice, setSplitChoice)}>Side by side</button>}
          <button type="button" aria-pressed={hideWhitespace} className={toggleClass(hideWhitespace)} onClick={() => toggle(WHITESPACE_KEY, !hideWhitespace, setHideWhitespace)}>Hide whitespace</button>
        </span>
      </div>

      <div className={`min-h-0 flex-1 overflow-auto ${maxHeightClassName}`}>
        <div className="min-w-full font-mono text-xs leading-5">
          {split ? splitRows.map((row, index) => 'full' in row
            ? divider(row.full, index)
            : <div key={index} data-split-row className="grid grid-cols-2">
              <div className="min-w-0 border-r border-border/60">{cell(row.left, 'before')}</div>
              {cell(row.right, 'after')}
            </div>)
          : lines.map((line, index) => line.kind === 'hunk' || line.kind === 'meta'
            ? divider(line, index)
            : <div key={`${index}-${line.content}`} className={`grid grid-cols-[3.25rem_1fr] items-start ${rowTint[line.kind] ?? ''}`}>
              {gutter(line, onSelectLine && line.anchor
                ? <button type="button" aria-label={`Comment on ${line.anchor.side} line ${line.anchor.line}`} className="hover:underline" onClick={() => onSelectLine(line.anchor!)}>{line.anchor.line}</button>
                : line.after ?? line.before)}
              {text(line)}
            </div>)}
        </div>
      </div>
    </div>
  )
}

export default DiffRenderer
