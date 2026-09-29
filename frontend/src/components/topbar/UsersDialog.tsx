import { useEffect } from 'react'
import { Users, X } from 'lucide-react'
import ModalPortal from '../ui/ModalPortal'
import UsersAdminPanel from '../admin/UsersAdminPanel'

/**
 * UsersDialog - the admin's user directory from the top bar: add people by
 * email (they sign in with SSO), set roles and products, reset passwords,
 * disable or delete. The same panel as Access → Users in a workflow.
 */
export default function UsersDialog({ isOpen, onClose }: { isOpen: boolean; onClose: () => void }) {
  useEffect(() => {
    if (!isOpen) return
    const onKeyDown = (event: KeyboardEvent) => { if (event.key === 'Escape') onClose() }
    document.addEventListener('keydown', onKeyDown)
    return () => document.removeEventListener('keydown', onKeyDown)
  }, [isOpen, onClose])

  if (!isOpen) return null
  return (
    <ModalPortal>
      <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onClick={onClose}>
        <div
          role="dialog"
          aria-modal="true"
          aria-labelledby="users-dialog-title"
          className="flex max-h-[90vh] w-full max-w-4xl flex-col rounded-lg border border-border bg-background shadow-xl"
          onClick={(event) => event.stopPropagation()}
        >
          <div className="flex items-center justify-between border-b border-border p-4">
            <div className="flex items-center gap-2">
              <Users className="h-5 w-5 text-muted-foreground" />
              <h2 id="users-dialog-title" className="text-lg font-semibold">Users &amp; access</h2>
            </div>
            <button onClick={onClose} className="rounded p-1 hover:bg-accent" aria-label="Close">
              <X className="h-4 w-4" />
            </button>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto p-4">
            <UsersAdminPanel />
          </div>
        </div>
      </div>
    </ModalPortal>
  )
}
