import { useEffect, useRef, type CSSProperties, type ReactNode } from 'react'
import { X } from 'lucide-react'
import ModalPortal from '../../ui/ModalPortal'
import type { ReportTheme } from './dashboardChatWidgets'

/** The same on-demand Ask dialog for published and existing HTML pages. */
export function DashboardChatDialog({ theme, onClose, children }: { theme?: ReportTheme; onClose: () => void; children: ReactNode }) {
  const dialog = useRef<HTMLDialogElement>(null)
  useEffect(() => { dialog.current?.showModal() }, [])
  const mode = theme?.mode === 'light' ? 'force-light' : theme?.mode === 'dark' ? 'dark' : ''
  return <ModalPortal><dialog ref={dialog} aria-label="Dashboard chat" style={theme?.vars as CSSProperties} onCancel={onClose} className={`${mode} m-auto h-[min(80vh,42rem)] w-[calc(100%_-_2rem)] max-w-xl overflow-hidden rounded-xl border border-border bg-background p-3 text-foreground shadow-xl backdrop:bg-black/50`}>
    <div className="flex h-full min-h-0 flex-col">
      <header className="mb-2 flex items-center justify-between"><span>Ask</span><button type="button" aria-label="Close chat" onClick={onClose}><X className="h-4 w-4" /></button></header>
      <div className="min-h-0 flex-1">{children}</div>
    </div>
  </dialog></ModalPortal>
}
