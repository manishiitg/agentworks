import { memo } from 'react'
import { Loader2 } from 'lucide-react'
import type { ChatRuntimeActivity } from '../utils/chatRuntimeActivity'

// Only lifecycle changes rerender the animation; token arrival does not.
export const AgentRuntimeActivityIndicator = memo(function AgentRuntimeActivityIndicator({ state, label }: ChatRuntimeActivity) {
  if (state === 'ready') return null
  return (
    <span
      data-testid="agent-runtime-activity"
      className="inline-flex h-4 shrink-0 items-center"
      role="status"
      aria-label={label}
      title={label}
    >
      {state === 'running'
        ? <Loader2 className="h-3 w-3 animate-spin motion-reduce:animate-none text-lime-300" aria-hidden="true" />
        : <span className="h-2 w-2 rounded-full bg-amber-400" aria-hidden="true" />}
    </span>
  )
})
