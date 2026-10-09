import { useEffect, useRef, useState } from 'react'
import { Check, ChevronDown, LayoutDashboard, Share2 } from 'lucide-react'
import { useChatStore } from '../../stores/useChatStore'
import { isShareableAppOrigin } from '../../utils/sharedLinks'
import api from '../../services/api'
import { copyToClipboard } from '../../utils/textUtils'
import { readStoredReportDocumentSelection, selectReportDocument, useReportDocuments, useSelectedReportDocument } from './reportDocuments'

export function ReportDocumentSwitcher({ workspacePath, active, onOpen }: {
  workspacePath: string
  active: boolean
  onOpen: () => void
}) {
  const { documents, defaultPath, refresh } = useReportDocuments(workspacePath)
  const selectedPath = useSelectedReportDocument(workspacePath)
  const [openMenu, setOpenMenu] = useState(false)
  const [copiedPath, setCopiedPath] = useState('')
  const rootRef = useRef<HTMLDivElement>(null)
  const canShareReports = isShareableAppOrigin(window.location.origin)
  const selectedDocument = documents.find(document => document.path === selectedPath)
    || documents.find(document => document.path === defaultPath)
    || documents[0]

  useEffect(() => {
    if (documents.length === 0) return
    const saved = readStoredReportDocumentSelection(workspacePath)
    if (!saved || !documents.some(document => document.path === saved)) selectReportDocument(workspacePath, defaultPath)
  }, [defaultPath, documents, workspacePath])

  useEffect(() => {
    if (!openMenu) return
    const close = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpenMenu(false)
    }
    window.addEventListener('mousedown', close)
    return () => window.removeEventListener('mousedown', close)
  }, [openMenu])

  const open = (path?: string) => {
    if (path) selectReportDocument(workspacePath, path)
    else if (documents.length > 0 && !documents.some(document => document.path === selectedPath)) selectReportDocument(workspacePath, defaultPath)
    onOpen()
  }

  const choose = (path: string) => {
    open(path)
    setOpenMenu(false)
  }

  const toggle = () => {
    open()
    if (openMenu) {
      setOpenMenu(false)
      return
    }
    // The agent can add reports at any time; re-read the catalog whenever the
    // menu is opened so a new document is never missing from a stale list.
    refresh()
    if (documents.length > 1) setOpenMenu(true)
  }

  const copyShareLink = async (path: string) => {
    let url: string
    try {
      const { data } = await api.post<{ url: string }>('/api/dashboards', { workspace: workspacePath, action: 'link', document_path: path })
      url = data.url
    } catch {
      useChatStore.getState().addToast('Could not create the dashboard share link.', 'error')
      return
    }
    if (!await copyToClipboard(url)) {
      useChatStore.getState().addToast('Could not copy the report share link.', 'error')
      return
    }
    setCopiedPath(path)
    useChatStore.getState().addToast('Report share link copied.', 'success')
    window.setTimeout(() => setCopiedPath(current => current === path ? '' : current), 2000)
  }

  return (
    <div ref={rootRef} className="relative inline-flex shrink-0 items-center">
      <button
        type="button"
        onClick={toggle}
        data-tour="workflow-dashboard"
        className={`inline-flex h-8 max-w-48 items-center gap-1.5 rounded-lg border border-border px-2.5 text-xs font-medium shadow-sm transition-colors ${active ? 'bg-muted text-foreground' : 'bg-muted/60 text-muted-foreground hover:bg-muted hover:text-foreground'}`}
        aria-label={`Dashboard${selectedDocument ? `: ${selectedDocument.title}` : ''}`}
        aria-haspopup={documents.length > 1 ? 'menu' : undefined}
        aria-expanded={documents.length > 1 ? openMenu : undefined}
        aria-pressed={active}
        title={selectedDocument ? `Report: ${selectedDocument.title}` : 'Dashboard'}
      >
        <LayoutDashboard className="h-3.5 w-3.5 shrink-0" />
        <span className="truncate">{selectedDocument?.title || 'Dashboard'}</span>
        {documents.length > 1 && <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />}
      </button>
      {documents.length > 1 && selectedDocument && openMenu && (
          <div role="menu" aria-label="Reports" className="absolute right-0 top-10 z-50 min-w-52 overflow-hidden rounded-lg border border-border bg-popover p-1 text-popover-foreground shadow-xl">
            {documents.map(document => (
              <div key={document.path} className="flex items-center rounded-md hover:bg-muted">
                <button
                  type="button"
                  role="menuitemradio"
                  aria-checked={selectedDocument.path === document.path}
                  onClick={() => choose(document.path)}
                  className="flex min-w-0 flex-1 items-center gap-2 px-2.5 py-2 text-left text-xs"
                >
                  <Check className={`h-3.5 w-3.5 shrink-0 ${selectedDocument.path === document.path ? 'opacity-100' : 'opacity-0'}`} />
                  <span className="truncate">{document.title}</span>
                </button>
                {canShareReports && (
                  <button
                    type="button"
                    onClick={() => { void copyShareLink(document.path) }}
                    className="mr-1 flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-muted-foreground hover:bg-background hover:text-foreground"
                    aria-label={`Copy share link for ${document.title}`}
                    title={`Copy share link for ${document.title}`}
                  >
                    {copiedPath === document.path
                      ? <Check className="h-3.5 w-3.5 text-emerald-500" />
                      : <Share2 className="h-3.5 w-3.5" />}
                  </button>
                )}
              </div>
            ))}
          </div>
      )}
    </div>
  )
}
