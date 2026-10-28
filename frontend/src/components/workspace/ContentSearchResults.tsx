import { FileText } from 'lucide-react'

export interface ContentSearchMatch {
  filepath: string
  line_number: number
  matched_text: string
}

/** "Search in files" results, grouped by file. Clicking a line opens the file there. */
export function ContentSearchResults({ matches, rootPath, onOpen }: {
  matches: ContentSearchMatch[]
  /** Shown paths are relative to this folder. */
  rootPath?: string
  onOpen: (filepath: string, line: number) => void
}) {
  const groups = new Map<string, ContentSearchMatch[]>()
  for (const match of matches) {
    const list = groups.get(match.filepath)
    if (list) list.push(match)
    else groups.set(match.filepath, [match])
  }
  const prefix = rootPath ? rootPath.replace(/\/+$/, '') + '/' : ''
  return (
    <div className="space-y-2" data-testid="content-search-results">
      {[...groups].map(([filepath, lines]) => (
        <div key={filepath}>
          <div className="flex items-center gap-1.5 px-1 py-0.5 text-xs font-medium text-foreground" title={filepath}>
            <FileText className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            <span className="truncate">{prefix && filepath.startsWith(prefix) ? filepath.slice(prefix.length) : filepath}</span>
            <span className="shrink-0 text-muted-foreground">{lines.length}</span>
          </div>
          {lines.map(match => (
            <button
              type="button"
              key={match.line_number}
              onClick={() => onOpen(match.filepath, match.line_number)}
              className="flex w-full gap-2 rounded px-1 py-0.5 pl-6 text-left text-xs hover:bg-muted/60"
            >
              <span className="w-8 shrink-0 text-right tabular-nums text-muted-foreground">{match.line_number}</span>
              <span className="min-w-0 flex-1 truncate font-mono text-foreground">{match.matched_text}</span>
            </button>
          ))}
        </div>
      ))}
    </div>
  )
}
