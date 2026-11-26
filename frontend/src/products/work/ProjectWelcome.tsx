import { useEffect, useState } from 'react'
import api from '../../services/api'
import { MarkdownRenderer } from '../../components/ui/MarkdownRenderer'
import { ConversationImageRoot } from '../../components/ui/ConversationImageRoot'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { useWorkspaceConnectionStore } from '../../stores/useWorkspaceConnectionStore'

export type ProjectWelcomeStarter = { label: string; note?: string; message: string }
export type ProjectWelcomePage = { body?: string; root?: string; starters: ProjectWelcomeStarter[] }

const MAX_WELCOME_PAGES = 64
const pages = new Map<string, ProjectWelcomePage>()
let cachedPagesIdentity = captureChatIdentity()

function currentWelcomePages(): Map<string, ProjectWelcomePage> {
  const identity = captureChatIdentity()
  if (identity !== cachedPagesIdentity) {
    cachedPagesIdentity = identity
    pages.clear()
  }
  return pages
}

/** The project's welcome page (its WELCOME.md, read by the server); undefined while loading or when it has none. */
export function useProjectWelcome(projectId?: string, profileId = 'work'): ProjectWelcomePage | undefined {
  const workspaceId = useWorkspaceConnectionStore(state => state.activeWorkspaceId)
  const identity = captureChatIdentity()
  const key = projectId ? `${identity}:${workspaceId}:${profileId}:${projectId}` : ''
  const [page, setPage] = useState(() => (key ? currentWelcomePages().get(key) : undefined))
  useEffect(() => {
    const cache = currentWelcomePages()
    setPage(key ? cache.get(key) : undefined)
    if (!projectId) return
    const abort = new AbortController()
    const capturedIdentity = captureChatIdentity()
    api.get<ProjectWelcomePage>(`/api/agent-profiles/${encodeURIComponent(profileId)}/welcome`, { params: { project_id: projectId }, signal: abort.signal, skipSessionContext: true })
      .then(response => {
        if (!isChatIdentityCurrent(capturedIdentity)) return
        const activeCache = currentWelcomePages()
        if (activeCache.size >= MAX_WELCOME_PAGES) {
          const oldest = activeCache.keys().next().value
          if (oldest !== undefined) activeCache.delete(oldest)
        }
        activeCache.set(key, response.data)
        setPage(response.data)
      })
      .catch(() => undefined)
    return () => abort.abort()
  }, [key, projectId, profileId])
  return page && (page.body || page.starters?.length) ? page : undefined
}

/** A project's welcome page: its Markdown (images and videos from the project folder) and its starter buttons. */
export function ProjectWelcome({ page, onStart }: { page: ProjectWelcomePage; onStart: (message: string) => void }) {
  return <div data-theme-surface="empty" className="mx-auto w-full max-w-2xl space-y-4 p-6 text-left text-sm">
    {/* Pictures and videos stay within about a third of the screen so the starters show without scrolling (owner, 25 Nov). */}
    {page.body && <div className="[&_img]:mx-auto [&_img]:max-h-[35vh] [&_img]:w-auto [&_video]:mx-auto [&_video]:max-h-[35vh] [&_video]:w-auto">
      <ConversationImageRoot.Provider value={page.root}><MarkdownRenderer content={page.body} /></ConversationImageRoot.Provider>
    </div>}
    {page.starters.length > 0 && <div className="grid gap-2 sm:grid-cols-2">
      {page.starters.map((starter, index) => <button key={index} type="button" onClick={() => onStart(starter.message)}
        className="rounded-lg border border-border bg-card px-3 py-2.5 text-left text-foreground hover:border-primary">
        <span className="block font-medium">{starter.label}</span>
        {starter.note && <span className="block text-xs text-muted-foreground">{starter.note}</span>}
      </button>)}
    </div>}
  </div>
}
