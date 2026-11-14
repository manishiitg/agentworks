import { AlertTriangle, X } from 'lucide-react'
import { useEffect, useState } from 'react'
import ModalPortal from './ModalPortal'
import { Input } from './Input'
import { Label } from './label'

interface ConfirmationDialogProps {
  isOpen: boolean
  onClose: () => void
  onConfirm: () => void
  title: string
  message: string
  confirmText?: string
  cancelText?: string
  type?: 'danger' | 'warning' | 'info'
  isLoading?: boolean
  loadingText?: string
  ignoreWorkspaceAutoCollapse?: boolean
  /**
   * GitHub-style delete: the exact text the user must type before the
   * confirm button enables. Omit for a plain two-button confirm.
   */
  requireText?: string
}

export default function ConfirmationDialog({
  isOpen,
  onClose,
  onConfirm,
  title,
  message,
  confirmText = 'Confirm',
  cancelText = 'Cancel',
  type = 'danger',
  isLoading = false,
  loadingText = 'Deleting...',
  ignoreWorkspaceAutoCollapse = false,
  requireText
}: ConfirmationDialogProps) {
  const [typed, setTyped] = useState('')
  useEffect(() => {
    if (isOpen) setTyped('')
  }, [isOpen])
  const confirmed = requireText === undefined || typed === requireText

  // Keyboard shortcuts
  useEffect(() => {
    if (!isOpen) return

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        if (!isLoading) {
          onClose()
        }
      } else if (event.key === 'Enter') {
        event.preventDefault()
        if (!isLoading && confirmed) {
          onConfirm()
        }
      }
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [isOpen, isLoading, confirmed, onClose, onConfirm])

  if (!isOpen) return null

  const getTypeStyles = () => {
    switch (type) {
      case 'danger':
        return {
          icon: 'text-destructive',
          confirmButton: 'bg-destructive hover:bg-destructive/90 text-destructive-foreground',
        }
      case 'warning':
        return {
          icon: 'text-amber-500',
          confirmButton: 'bg-amber-600 hover:bg-amber-700 text-white',
        }
      case 'info':
        return {
          icon: 'text-primary',
          confirmButton: 'bg-primary hover:bg-primary/90 text-primary-foreground',
        }
      default:
        return {
          icon: 'text-destructive',
          confirmButton: 'bg-destructive hover:bg-destructive/90 text-destructive-foreground',
        }
    }
  }

  const styles = getTypeStyles()

  return (
    <ModalPortal>
    <div
      className="fixed inset-0 bg-black/50 flex items-center justify-center z-[10000]"
      data-workspace-collapse-ignore={ignoreWorkspaceAutoCollapse ? 'true' : undefined}
    >
      <div role="dialog" aria-modal="true" aria-label={title} className="bg-card rounded-md shadow-md border border-border max-w-md w-full mx-4">
        {/* Header */}
        <div className="flex items-center justify-between p-6 border-b border-border">
          <div className="flex items-center gap-3">
            <AlertTriangle className={`w-6 h-6 ${styles.icon}`} />
            <h3 className="text-sm font-semibold text-foreground">
              {title}
            </h3>
          </div>
          <button
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground"
            disabled={isLoading}
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        {/* Content */}
        <div className="p-6">
          <p className="text-muted-foreground mb-6">
            {message}
          </p>

          {requireText !== undefined && (
            <div className="mb-6">
              <Label className="mb-2 block text-muted-foreground">
                Type <span className="font-semibold text-foreground">{requireText}</span> to confirm
              </Label>
              <Input
                value={typed}
                onChange={event => setTyped(event.target.value)}
                placeholder={requireText}
                autoComplete="off"
                aria-label="Confirmation text"
              />
            </div>
          )}

          {/* Actions */}
          <div className="flex gap-3 justify-end">
            <button
              onClick={onClose}
              className="px-4 py-2 text-sm font-medium text-secondary-foreground bg-secondary hover:bg-secondary/80 rounded-md transition-colors"
              disabled={isLoading}
            >
              {cancelText}
            </button>
            <button
              onClick={onConfirm}
              className={`px-4 py-2 text-sm font-medium rounded-md transition-colors ${styles.confirmButton} disabled:opacity-50 disabled:cursor-not-allowed`}
              disabled={isLoading || !confirmed}
            >
              {isLoading ? (
                <div className="flex items-center gap-2">
                  <div className="w-4 h-4 border-2 border-white border-t-transparent rounded-full animate-spin"></div>
                  {loadingText}
                </div>
              ) : (
                confirmText
              )}
            </button>
          </div>
        </div>
      </div>
    </div>
    </ModalPortal>
  )
}
