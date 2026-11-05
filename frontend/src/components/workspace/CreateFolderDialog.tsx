import { useState, useEffect, useCallback } from 'react'
import { X, FolderPlus } from 'lucide-react'

interface CreateFolderDialogProps {
  isOpen: boolean
  onClose: () => void
  onCreateFolder: (folderPath: string, commitMessage?: string) => Promise<void>
  parentPath?: string
  /** False hides the optional commit message where the folder is not a versioned workspace (the Team tab). */
  commitMessage?: boolean
}

export default function CreateFolderDialog({ 
  isOpen, 
  onClose, 
  onCreateFolder, 
  parentPath = '',
  commitMessage: showCommitMessage = true
}: CreateFolderDialogProps) {
  const [folderName, setFolderName] = useState('')
  const [commitMessage, setCommitMessage] = useState('')
  const [isCreating, setIsCreating] = useState(false)
  const [error, setError] = useState('')

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    
    if (!folderName.trim()) {
      setError('Folder name is required')
      return
    }

    // Validate folder name — no spaces or special characters (only alphanumeric, hyphens, underscores)
    const invalidChars = /[^a-zA-Z0-9_\-./]/
    if (invalidChars.test(folderName)) {
      setError('Folder name can only contain letters, numbers, hyphens (-) and underscores (_)')
      return
    }

    // Check for reserved names
    const reservedNames = ['CON', 'PRN', 'AUX', 'NUL', 'COM1', 'COM2', 'COM3', 'COM4', 'COM5', 'COM6', 'COM7', 'COM8', 'COM9', 'LPT1', 'LPT2', 'LPT3', 'LPT4', 'LPT5', 'LPT6', 'LPT7', 'LPT8', 'LPT9']
    if (reservedNames.includes(folderName.toUpperCase())) {
      setError('Folder name is reserved')
      return
    }

    setIsCreating(true)
    setError('')

    try {
      const fullPath = parentPath ? `${parentPath}/${folderName}` : folderName
      await onCreateFolder(fullPath, commitMessage || undefined)
      
      // Reset form
      setFolderName('')
      setCommitMessage('')
      onClose()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create folder')
    } finally {
      setIsCreating(false)
    }
  }

  const handleClose = useCallback(() => {
    if (!isCreating) {
      setFolderName('')
      setCommitMessage('')
      setError('')
      onClose()
    }
  }, [isCreating, onClose])

  // Keyboard shortcuts
  useEffect(() => {
    if (!isOpen) return

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        if (!isCreating) {
          handleClose()
        }
      }
      // Enter key is handled by the form's onSubmit
    }

    document.addEventListener('keydown', handleKeyDown)
    return () => document.removeEventListener('keydown', handleKeyDown)
  }, [isOpen, isCreating, handleClose])

  if (!isOpen) return null

  return (
    <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
      <div className="bg-card rounded-md shadow-md border border-border w-full max-w-md mx-4">
        <div className="flex items-center justify-between border-b border-border p-4">
          <div className="flex items-center gap-2">
            <FolderPlus className="w-5 h-5 text-primary" />
            <h3 className="text-sm font-semibold text-foreground">
              Create New Folder
            </h3>
          </div>
          <button
            onClick={handleClose}
            disabled={isCreating}
            className="text-muted-foreground hover:text-foreground disabled:opacity-50"
          >
            <X className="w-5 h-5" />
          </button>
        </div>

        <form onSubmit={handleSubmit} className="p-4 space-y-4">
          <div>
            <label className="block text-sm font-medium text-foreground mb-1">
              Folder Name
            </label>
            <input
              type="text"
              value={folderName}
              onChange={(e) => setFolderName(e.target.value)}
              placeholder="Enter folder name"
              disabled={isCreating}
              className="w-full px-3 py-2 rounded-md border border-input bg-transparent text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50"
              autoFocus
            />
            {parentPath && (
              <p className="text-xs text-muted-foreground mt-1">
                Will be created in: {parentPath}
              </p>
            )}
          </div>

          {showCommitMessage && <div>
            <label className="block text-sm font-medium text-foreground mb-1">
              Commit Message (Optional)
            </label>
            <input
              type="text"
              value={commitMessage}
              onChange={(e) => setCommitMessage(e.target.value)}
              placeholder="Add commit message for version control"
              disabled={isCreating}
              className="w-full px-3 py-2 rounded-md border border-input bg-transparent text-foreground placeholder:text-muted-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50"
            />
          </div>}

          {error && (
            <div className="text-sm text-destructive bg-destructive/10 border border-destructive/20 rounded-md p-2">
              {error}
            </div>
          )}

          <div className="flex justify-end gap-2 pt-4">
            <button
              type="button"
              onClick={handleClose}
              disabled={isCreating}
              className="px-4 py-2 text-sm font-medium text-secondary-foreground bg-secondary hover:bg-secondary/80 rounded-md disabled:opacity-50"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={isCreating || !folderName.trim()}
              className="px-4 py-2 text-sm font-medium text-primary-foreground bg-primary hover:bg-primary/90 rounded-md disabled:opacity-50 flex items-center gap-2"
            >
              {isCreating ? (
                <>
                  <div className="w-4 h-4 border-2 border-white border-t-transparent rounded-full animate-spin" />
                  Creating...
                </>
              ) : (
                <>
                  <FolderPlus className="w-4 h-4" />
                  Create Folder
                </>
              )}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
