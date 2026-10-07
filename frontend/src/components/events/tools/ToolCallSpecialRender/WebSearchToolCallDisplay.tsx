import React, { useMemo, useState } from 'react'
import { ChevronDown, ChevronRight, Globe } from 'lucide-react'
import { parseWebSearchToolCall, type WebSearchView } from '../../../../utils/webSearchToolCall'

interface WebSearchToolCallDisplayProps {
  name: string
  args?: string
  result?: string
  status?: 'running' | 'ok' | 'error'
  /** Already-formatted duration label, e.g. "3.4s". */
  duration?: string | null
  /** Controlled disclosure (the chat transcript keeps it across virtualised re-mounts). */
  open?: boolean
  onToggle?: () => void
}

function countLabel(view: WebSearchView, running: boolean): string {
  if (running) return ''
  if (view.isError) return 'failed'
  if (view.results.length > 0) return `${view.results.length} result${view.results.length === 1 ? '' : 's'}`
  if (view.opened.length > 0) return `${view.opened.length} page${view.opened.length === 1 ? '' : 's'} opened`
  return ''
}

/**
 * A coding agent's web search: one compact row ("Searched: <query> · 9 results")
 * that opens to the result links. Collapsed by default.
 */
export const WebSearchToolCallDisplay: React.FC<WebSearchToolCallDisplayProps> = ({ name, args, result, status = 'ok', duration, open, onToggle }) => {
  const [localOpen, setLocalOpen] = useState(false)
  const expanded = open ?? localOpen
  const toggle = onToggle ?? (() => setLocalOpen(value => !value))
  const isError = status === 'error'
  const view = useMemo(() => parseWebSearchToolCall({ name, args, result, isError }), [name, args, result, isError])
  if (!view) return null

  const running = status === 'running'
  const subject = view.query || view.opened[0] || ''
  const verb = running
    ? (subject ? 'Searching' : 'Searching the web…')
    : view.query ? 'Searched' : view.opened.length > 0 ? 'Opened' : 'Web search'
  const count = countLabel(view, running)
  const extraQueries = view.queries.slice(1)

  return (
    <div
      data-testid="web-search-card"
      className={`min-w-0 rounded border ${view.isError
        ? 'border-red-300/70 bg-red-50/70 dark:border-red-900/80 dark:bg-red-950/20'
        : 'border-border/70 bg-card'}`}
    >
      <button
        type="button"
        onClick={toggle}
        aria-expanded={expanded}
        className="flex w-full min-w-0 items-center gap-2 px-2 py-1.5 text-left text-xs hover:bg-muted/60"
      >
        <Globe className={`h-3.5 w-3.5 shrink-0 ${view.isError ? 'text-red-500' : 'text-sky-600 dark:text-sky-400'}`} aria-hidden="true" />
        <span className="shrink-0 text-muted-foreground">{verb}{subject ? ':' : ''}</span>
        {subject && <span className="min-w-0 truncate font-medium text-foreground" title={subject}>{subject}</span>}
        {extraQueries.length > 0 && <span className="shrink-0 text-[10px] text-muted-foreground">+{extraQueries.length}</span>}
        {count && (
          <span className={`shrink-0 text-[10px] ${view.isError ? 'text-red-600 dark:text-red-300' : 'text-muted-foreground'}`}>{count}</span>
        )}
        {duration && <span className="hidden shrink-0 tabular-nums text-[10px] text-muted-foreground sm:inline">{duration}</span>}
        {expanded
          ? <ChevronDown className="ml-auto h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-label="Hide search results" />
          : <ChevronRight className="ml-auto h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-label="Show search results" />}
      </button>

      {expanded && (
        <div className="space-y-1 border-t border-border/70 px-2 py-1.5">
          {extraQueries.length > 0 && (
            <ul className="space-y-0.5 text-[11px] text-muted-foreground">
              {view.queries.map(query => <li key={query} className="truncate" title={query}>{query}</li>)}
            </ul>
          )}
          {view.results.length > 0 && (
            <ul data-testid="web-search-results" className="max-h-64 space-y-px overflow-y-auto">
              {view.results.map(item => (
                <li key={item.url}>
                  <a
                    href={item.url}
                    target="_blank"
                    rel="noopener noreferrer"
                    title={item.url}
                    className="flex min-w-0 items-baseline gap-2 rounded px-1.5 py-1 text-[11px] hover:bg-muted/60"
                  >
                    <span className="min-w-0 flex-1 truncate text-foreground">{item.title}</span>
                    <span className="max-w-[40%] shrink-0 truncate text-[10px] text-muted-foreground">{item.domain}</span>
                  </a>
                </li>
              ))}
            </ul>
          )}
          {view.opened.length > 0 && (
            <ul className="space-y-px">
              {view.opened.map(url => (
                <li key={url}>
                  <a href={url} target="_blank" rel="noopener noreferrer" className="block truncate rounded px-1.5 py-1 text-[11px] text-sky-700 hover:bg-muted/60 dark:text-sky-300" title={url}>
                    {url}
                  </a>
                </li>
              ))}
            </ul>
          )}
          {view.isError && view.text && (
            <pre className="max-h-40 overflow-auto whitespace-pre-wrap break-words rounded border border-red-200 bg-red-50 p-2 text-[11px] leading-5 text-red-800 dark:border-red-900/70 dark:bg-red-950/30 dark:text-red-200">{view.text}</pre>
          )}
          {!view.isError && view.results.length === 0 && view.opened.length === 0 && (
            <p className="text-[11px] text-muted-foreground">
              {running ? 'Waiting for results…' : view.query ? 'This agent does not report a result list for its searches.' : 'This agent did not report the query or results for this search.'}
            </p>
          )}
        </div>
      )}
    </div>
  )
}
