import { ArrowLeft } from 'lucide-react'

/** Expanded panels can return to the always-visible context summary. */
export function WorkspaceContextButton({ inContext, onClick }: {
  inContext: boolean
  onClick: () => void
}) {
  if (inContext) return null
  return (
    <button
      type="button"
      title="Back to context"
      aria-label="Back to context"
      onClick={onClick}
      className="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <ArrowLeft className="h-4 w-4" />
    </button>
  )
}
