import { LoadingIndicator } from './LoadingIndicator'

interface LazyModalFallbackProps {
  label: string
}

export default function LazyModalFallback({ label }: LazyModalFallbackProps) {
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-transparent p-4" role="status">
      <div className="flex items-center gap-2 rounded-lg border border-border bg-background px-4 py-3 text-sm text-muted-foreground shadow-xl">
        <LoadingIndicator className="h-4 w-4" aria-hidden="true" />
        <span>{label}</span>
      </div>
    </div>
  )
}
