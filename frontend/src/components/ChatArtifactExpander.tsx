import React, { useCallback, useState } from 'react'
import type { PollingEvent } from '../services/api-types'

// The chat journal stores a bounded summary of oversized rows (long answers,
// big tool outputs) and keeps the complete event in a private artifact.

function eventPayload(event: PollingEvent | undefined): Record<string, unknown> {
  const outer = event?.data as unknown
  if (!outer || typeof outer !== 'object') return {}
  const nested = (outer as { data?: unknown }).data
  return nested && typeof nested === 'object' ? nested as Record<string, unknown> : outer as Record<string, unknown>
}

export interface ChatArtifactRef {
  sessionId: string
  artifactId: string
  originalSizeBytes?: number
}

export function chatArtifactRef(event: PollingEvent | undefined): ChatArtifactRef | null {
  const payload = eventPayload(event)
  const artifactId = typeof payload.artifact_id === 'string' ? payload.artifact_id : ''
  const sessionId = typeof event?.session_id === 'string' ? event.session_id : ''
  if (payload.truncated !== true || !artifactId || !sessionId) return null
  const size = typeof payload.original_size_bytes === 'number' ? payload.original_size_bytes : undefined
  return { sessionId, artifactId, originalSizeBytes: size }
}

export function chatArtifactText(event: PollingEvent, keys: readonly string[]): string {
  const payload = eventPayload(event)
  for (const key of keys) {
    // Delivered-message bodies live in server-stamped metadata; the complete
    // prompt's content also contains instructions that should stay in details.
    const value = key.split('.').reduce<unknown>((current, part) =>
      current && typeof current === 'object' ? (current as Record<string, unknown>)[part] : undefined, payload)
    if (typeof value === 'string' && value.trim()) return value
    if (value != null && typeof value === 'object') {
      try {
        return JSON.stringify(value, null, 2)
      } catch {
        // fall through to the next key
      }
    }
  }
  return ''
}

function formatSize(bytes: number | undefined): string {
  if (!bytes) return ''
  return bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(1)} MB` : `${Math.round(bytes / 1024)} KB`
}

export function useChatArtifactText(ref: ChatArtifactRef | null, keys: readonly string[]) {
  const [text, setText] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const load = useCallback(async () => {
    if (!ref || loading) return
    setLoading(true)
    setError('')
    try {
      // Loaded on demand: keeps the API client out of the transcript's import graph.
      const { agentApi } = await import('../services/api')
      const full = await agentApi.getChatArtifact(ref.sessionId, ref.artifactId)
      setText(chatArtifactText(full, keys))
    } catch {
      setError('Could not load the full content.')
    } finally {
      setLoading(false)
    }
  }, [keys, loading, ref])
  return { text, loading, error, load }
}

export const ShowFullContentButton: React.FC<{
  ref_: ChatArtifactRef
  loading: boolean
  error: string
  onLoad: () => void
  label?: string
}> = ({ ref_, loading, error, onLoad, label = 'Show full' }) => (
  <div className="mt-1 flex items-center gap-2 text-[11px] text-muted-foreground" data-testid="chat-artifact-show-full">
    <span>Shortened for chat history{ref_.originalSizeBytes ? ` (${formatSize(ref_.originalSizeBytes)})` : ''}.</span>
    <button type="button" onClick={onLoad} disabled={loading} className="font-medium text-blue-600 hover:underline disabled:opacity-60 dark:text-blue-400">
      {loading ? 'Loading…' : label}
    </button>
    {error && <span className="text-red-500">{error}</span>}
  </div>
)
