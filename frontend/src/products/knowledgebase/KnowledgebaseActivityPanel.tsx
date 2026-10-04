import { History } from 'lucide-react'
import type { KnowledgeEvent } from '../../services/knowledgebaseApi'

export function KnowledgebaseActivityPanel({ events, nextCursor, loading, onMore }: { events: KnowledgeEvent[]; nextCursor?: string; loading: boolean; onMore: () => void }) {
  return <section className="mx-auto max-w-4xl p-4 sm:p-7"><div className="mb-5 flex items-center gap-3"><History className="h-5 w-5 text-primary" /><h1 className="text-xl font-semibold">Activity</h1></div>
    <p className="mb-5 text-sm text-muted-foreground">Changes to content, access, and backups in folders you can read.</p>
    {!events.length ? <p className="rounded-xl border border-dashed border-border p-5 text-sm text-muted-foreground">No activity in this folder yet.</p> : <ol className="divide-y divide-border rounded-xl border border-border">{events.map(event => <li key={event.id} className="p-4"><div className="flex flex-wrap justify-between gap-2"><strong className="text-sm">{(event.action || event.type || 'Change').replace(/[_:.]/g, ' ')}</strong><time className="text-xs text-muted-foreground" dateTime={event.timestamp}>{new Date(event.timestamp).toLocaleString()}</time></div><p className="mt-1 break-all text-xs text-muted-foreground">{event.path || event.folder_path || 'Organization root'} · {event.identity_id}</p>{event.message && <p className="mt-2 text-sm">{event.message}</p>}</li>)}</ol>}
    {nextCursor && <button type="button" disabled={loading} onClick={onMore} className="mt-4 rounded-md border border-border px-4 py-2 text-xs hover:bg-muted disabled:opacity-50">{loading ? 'Loading…' : 'Load more activity'}</button>}
  </section>
}
