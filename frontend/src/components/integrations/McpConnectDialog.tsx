import { useEffect, useRef } from 'react'
import { ArrowLeft, X } from 'lucide-react'
import McpConnectBody from '../topbar/McpConnectBody'
import { useAuthStore } from '../../stores/useAuthStore'

/** Welcome keeps its place while the existing, permission-aware setup runs in a focused dialog. */
export default function McpConnectDialog({ onClose, onNavigate }: { onClose: () => void; onNavigate: () => void }) {
  const dialog = useRef<HTMLDialogElement>(null)
  const user = useAuthStore(state => state.user)
  const multiUser = useAuthStore(state => state.isMultiUserMode)
  const codeReview = user?.is_crew_reviewer === true || (user?.is_admin === true && multiUser)
  useEffect(() => {
    const element = dialog.current!
    element.showModal()
    return () => element.close()
  }, [])
  return <dialog ref={dialog} aria-labelledby="ai-connect-title" onCancel={event => { event.preventDefault(); onClose() }} className="fixed inset-0 m-auto w-[calc(100vw-2rem)] max-w-4xl max-h-[calc(100dvh-2rem)] overflow-hidden rounded-2xl border border-border bg-background p-0 text-foreground shadow-2xl backdrop:bg-black/60">
    <div className="flex max-h-[calc(100dvh-2rem)] flex-col">
      <header className="flex shrink-0 items-center justify-between gap-4 border-b border-border p-4 sm:px-6">
        <div className="min-w-0">
          <button type="button" onClick={onClose} className="mb-2 inline-flex min-h-8 items-center gap-2 rounded-md text-xs text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><ArrowLeft className="h-4 w-4" aria-hidden="true" />Back to welcome</button>
          <h2 id="ai-connect-title" className="text-lg font-semibold">Connect your AI app</h2>
        </div>
        <button type="button" onClick={onClose} aria-label="Close connection setup" className="shrink-0 rounded-md p-2 text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><X className="h-5 w-5" aria-hidden="true" /></button>
      </header>
      <div className="min-h-0 overflow-y-auto p-4 sm:p-6"><McpConnectBody codeReview={codeReview} onNavigate={onNavigate} /></div>
    </div>
  </dialog>
}
