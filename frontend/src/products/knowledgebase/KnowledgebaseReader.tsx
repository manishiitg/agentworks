import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import rehypeSanitize from 'rehype-sanitize'
import { ArrowLeft, BookOpen, Cloud, CloudOff } from 'lucide-react'
import type { KnowledgeRead } from '../../services/knowledgebaseApi'

export function backupLabel(status?: string): string {
  if (status === 'backed_up' || status === 'backed-up' || status === 'published') return 'Backed up'
  if (status === 'push_unknown' || status === 'PUSH_UNKNOWN' || status === 'unknown') return 'Backup awaiting confirmation'
  if (status === 'unconfigured' || status === 'not_configured') return 'Backup not configured'
  if (status === 'committed_not_pushed') return 'Committed · push pending'
  return 'Saved · backup pending'
}

export function KnowledgebaseReader({ read, onBack }: { read: KnowledgeRead; onBack: () => void }) {
  const entry = read.entry
  const backedUp = backupLabel(entry.backup_status) === 'Backed up'
  return <article className="mx-auto w-full max-w-4xl p-4 sm:p-7" aria-label="Knowledge entry">
    <button type="button" onClick={onBack} className="mb-5 inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground"><ArrowLeft className="h-3.5 w-3.5" />Back to library</button>
    <div className="flex items-center gap-2 text-xs text-muted-foreground"><BookOpen className="h-4 w-4" /><span className="break-all">{entry.path}</span></div>
    <h1 className="mt-3 break-words text-2xl font-semibold">{entry.title || entry.filename}</h1>
    <div className="mt-3 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      <span className="rounded-full bg-muted px-2 py-1 capitalize">{entry.type}</span>
      {(entry.tags || []).map(tag => <span key={tag} className="rounded-full border border-border px-2 py-1">{tag}</span>)}
      <span className="inline-flex items-center gap-1" title="Content saves are visible immediately. Git backup is a separate MCP operation.">{backedUp ? <Cloud className="h-3.5 w-3.5" /> : <CloudOff className="h-3.5 w-3.5" />}{backupLabel(entry.backup_status)}</span>
    </div>
    {entry.description && <p className="mt-4 text-sm text-muted-foreground">{entry.description}</p>}
    <p className="mt-4 border-b border-border pb-5 text-xs text-muted-foreground">Updated {entry.updated_at ? new Date(entry.updated_at).toLocaleString() : 'recently'}{entry.updated_by && <> by {entry.updated_by}</>}</p>
    <div className="kb-markdown mt-6 break-words text-sm leading-7 [&_h1]:my-5 [&_h1]:text-2xl [&_h1]:font-semibold [&_h2]:my-4 [&_h2]:text-xl [&_h2]:font-semibold [&_h3]:my-3 [&_h3]:text-lg [&_h3]:font-semibold [&_p]:my-3 [&_ul]:my-3 [&_ul]:list-disc [&_ul]:pl-6 [&_ol]:my-3 [&_ol]:list-decimal [&_ol]:pl-6 [&_pre]:my-4 [&_pre]:overflow-x-auto [&_pre]:rounded-lg [&_pre]:bg-muted [&_pre]:p-4 [&_code]:font-mono [&_code]:text-xs [&_blockquote]:border-l-2 [&_blockquote]:border-border [&_blockquote]:pl-4 [&_table]:block [&_table]:overflow-x-auto [&_td]:border [&_td]:border-border [&_td]:p-2 [&_th]:border [&_th]:border-border [&_th]:p-2 [&_a]:text-primary [&_a]:underline">
      <ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeSanitize]} components={{
        a: ({ children, href }) => <a href={href} target="_blank" rel="noopener noreferrer">{children}</a>,
        // Remote images can leak the reader's identity to an author's server.
        img: ({ alt }) => <span className="text-muted-foreground">[Image{alt ? `: ${alt}` : ''}]</span>,
      }}>{read.content}</ReactMarkdown>
    </div>
  </article>
}
