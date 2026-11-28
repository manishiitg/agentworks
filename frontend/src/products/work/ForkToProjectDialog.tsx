import { useEffect, useMemo, useState } from 'react'
import { createPortal } from 'react-dom'
import { Check, FolderInput, Layers, Loader2, Search, X } from 'lucide-react'
import { loadProductProjects } from '../../platform/chat/productProjects'
import { WORK_PROFILE_ID, WORK_PROJECTS_ROOT } from './workData'
import { EntityIdentityIcon } from '../../components/ui/EntityIdentityIcon'
import { AGENTWORKS_SUBTITLE, AgentWorksIcon, agentWorksFirst, isOwnAgentWorks } from '../../components/ui/AgentWorksIdentity'
import { captureChatIdentity, isChatIdentityCurrent } from '../../utils/chatIdentity'
import { forkChatTab } from '../../utils/forkChatTab'

interface TargetProjectItem {
  id: string
  label: string
  secondaryLabel?: string
  icon?: string
  agentWorks?: boolean
}

export function ForkToProjectDialog({
  tabId,
  sourceProjectId,
  name,
  close,
}: {
  tabId: string
  sourceProjectId: string
  name: string
  close: () => void
}) {
  const [projects, setProjects] = useState<TargetProjectItem[]>([])
  const [status, setStatus] = useState<'loading' | 'ready' | 'error'>('loading')
  const [query, setQuery] = useState('')
  const [selectedId, setSelectedId] = useState('')
  const [forkName, setForkName] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    const identity = captureChatIdentity()
    setStatus('loading')
    void loadProductProjects(WORK_PROJECTS_ROOT, WORK_PROFILE_ID, { includeOwnSharedProjects: true }).then(crews => {
      if (cancelled || !isChatIdentityCurrent(identity)) return
      const items = crews
        .filter(crew => crew.id !== sourceProjectId && (!crew.shared || crew.shared.canBuild === true))
        .map(crew => {
          const label = crew.identity?.name?.trim() || crew.title
          const agentWorks = isOwnAgentWorks(crew)
          return {
            id: crew.id,
            label,
            secondaryLabel: agentWorks ? AGENTWORKS_SUBTITLE : label === crew.title ? undefined : crew.title,
            icon: crew.identity?.icon,
            agentWorks,
          }
        })
      setProjects(agentWorksFirst(items, item => !!item.agentWorks))
      setStatus('ready')
    }).catch(() => {
      if (cancelled || !isChatIdentityCurrent(identity)) return
      setProjects([])
      setStatus('error')
    })
    return () => { cancelled = true }
  }, [sourceProjectId])

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !submitting) {
        event.preventDefault()
        close()
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [close, submitting])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    if (!q) return projects
    return projects.filter(item =>
      item.label.toLowerCase().includes(q) ||
      Boolean(item.secondaryLabel?.toLowerCase().includes(q)) ||
      item.id.toLowerCase().includes(q)
    )
  }, [projects, query])

  const submit = async () => {
    if (!selectedId || submitting) return
    setSubmitting(true)
    setError('')
    try {
      const ok = await forkChatTab(tabId, undefined, { targetProjectId: selectedId, name: forkName.trim() || undefined })
      if (ok) close()
      else setError('Could not fork into this project. Try again.')
    } finally {
      setSubmitting(false)
    }
  }

  return createPortal(
    <div className="fixed inset-0 z-[110] flex items-center justify-center bg-background/70 p-4" onClick={() => { if (!submitting) close() }}>
      <div
        data-theme-surface="dialog"
        role="dialog"
        aria-modal="true"
        aria-label={`Fork ${name} to another project`}
        style={{ width: '28rem', maxWidth: 'calc(100vw - 2rem)', maxHeight: 'calc(100vh - 2rem)' }}
        className="flex min-w-0 flex-col overflow-hidden rounded-xl border border-border bg-surface-panel p-4 shadow-xl"
        onClick={event => event.stopPropagation()}
      >
        <div className="mb-1.5 flex items-center justify-between gap-2">
          <h2 className="flex items-center gap-2 text-sm font-semibold text-foreground">
            <FolderInput aria-hidden="true" className="h-4 w-4 text-muted-foreground" />
            Fork to another project
          </h2>
          <button
            type="button"
            disabled={submitting}
            aria-label="Close"
            onClick={close}
            className="rounded p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <p className="mb-3 text-xs text-muted-foreground">
          Copies this conversation into a separate agent in another project. Files, memory and logins stay here; the new agent starts in that project&apos;s folder.
        </p>

        <label className="mb-3 block text-xs font-medium text-foreground">
          Agent name (optional)
          <input
            type="text"
            value={forkName}
            onChange={event => setForkName(event.target.value)}
            maxLength={120}
            disabled={submitting}
            placeholder={`${name} (fork)`}
            className="mt-1 h-9 w-full rounded-md border border-input bg-background px-2.5 text-sm text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        </label>

        {projects.length > 5 && (
          <div className="relative mb-2">
            <Search aria-hidden="true" className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <input
              type="search"
              value={query}
              onChange={event => setQuery(event.target.value)}
              disabled={submitting}
              placeholder="Filter projects…"
              aria-label="Filter projects"
              className="h-8 w-full rounded-md border border-input bg-background pl-8 pr-2.5 text-xs text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            />
          </div>
        )}

        <div role="listbox" aria-label="Destination project" className="max-h-60 min-h-0 flex-1 space-y-1 overflow-y-auto rounded-lg border border-border bg-background/50 p-1">
          {status === 'loading' && (
            <div className="flex items-center justify-center gap-2 py-6 text-xs text-muted-foreground">
              <Loader2 className="h-3.5 w-3.5 animate-spin" />Loading projects…
            </div>
          )}
          {status === 'error' && (
            <p role="alert" className="px-2 py-4 text-center text-xs text-destructive">
              Could not load projects. Close and try again.
            </p>
          )}
          {status === 'ready' && filtered.length === 0 && (
            <p className="px-2 py-5 text-center text-xs text-muted-foreground">
              {projects.length === 0
                ? 'No other projects available. Create another project first, then fork this chat into it.'
                : 'No matching projects.'}
            </p>
          )}
          {status === 'ready' && filtered.map(item => {
            const active = selectedId === item.id
            return (
              <button
                key={item.id}
                type="button"
                role="option"
                aria-selected={active}
                disabled={submitting}
                onClick={() => setSelectedId(item.id)}
                className={`flex w-full items-center gap-2.5 rounded-md px-2.5 py-2 text-left text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
                  active ? 'bg-primary/10 text-foreground ring-1 ring-primary' : 'text-foreground hover:bg-muted'
                }`}
              >
                {item.agentWorks ? (
                  <AgentWorksIcon className="h-4 w-4 shrink-0" />
                ) : item.icon ? (
                  <EntityIdentityIcon icon={item.icon} className="h-4 w-4 shrink-0 text-muted-foreground" />
                ) : (
                  <Layers aria-hidden="true" className="h-4 w-4 shrink-0 text-muted-foreground" />
                )}
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-medium">{item.label}</span>
                  {item.secondaryLabel && <span className="block truncate text-xs text-muted-foreground">{item.secondaryLabel}</span>}
                </span>
                {active && <Check aria-hidden="true" className="h-4 w-4 shrink-0 text-primary" />}
              </button>
            )
          })}
        </div>

        {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}

        <div className="mt-3 flex justify-end gap-2">
          <button
            type="button"
            disabled={submitting}
            onClick={close}
            className="rounded-md px-3 py-1.5 text-sm text-foreground hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            Cancel
          </button>
          <button
            type="button"
            disabled={submitting || !selectedId}
            onClick={() => { void submit() }}
            className="inline-flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"
          >
            {submitting && <Loader2 aria-hidden="true" className="h-3.5 w-3.5 animate-spin" />}
            {submitting ? 'Forking…' : 'Fork to project'}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  )
}
