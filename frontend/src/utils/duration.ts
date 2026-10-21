/**
 * Duration formatting utilities for event components
 * 
 * Go's time.Duration serializes to JSON as nanoseconds (integer)
 * Frontend needs to convert nanoseconds to human-readable format
 */

import { getDisplayTimeZone } from './displayTime'

/**
 * Formats a duration in nanoseconds to a human-readable string
 * @param durationNs - Duration in nanoseconds (from Go time.Duration)
 * @returns Formatted duration string (e.g., "1.2s", "150ms", "2.5m")
 */
export function formatDuration(durationNs: number): string {
  if (!durationNs || durationNs <= 0) {
    return '0ms'
  }

  // Convert nanoseconds to milliseconds
  const durationMs = durationNs / 1000000

  if (durationMs < 1) {
    // Less than 1ms, show in microseconds
    const durationUs = durationNs / 1000
    return `${Math.round(durationUs)}μs`
  } else if (durationMs < 1000) {
    // Less than 1 second, show in milliseconds
    return `${Math.round(durationMs)}ms`
  } else if (durationMs < 60000) {
    // Less than 1 minute, show in seconds
    return `${(durationMs / 1000).toFixed(1)}s`
  } else {
    // 1 minute or more, show in minutes
    return `${(durationMs / 60000).toFixed(1)}m`
  }
}

/**
 * Formats a duration in nanoseconds to a compact format suitable for inline display
 * @param durationNs - Duration in nanoseconds (from Go time.Duration)
 * @returns Compact duration string
 */
export function formatStartedAt(iso?: string | null): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (isNaN(d.getTime())) return ''
  const timeZone = getDisplayTimeZone()
  const clock = d.toLocaleTimeString([], {
    timeZone,
    hour: '2-digit',
    minute: '2-digit',
    hour12: true,
    timeZoneName: timeZone ? 'short' : undefined,
  })
  const diffMs = Date.now() - d.getTime()
  const mins = Math.floor(diffMs / 60000)
  let rel: string
  if (mins < 1) rel = 'just now'
  else if (mins < 60) rel = `${mins}m ago`
  else if (mins < 24 * 60) {
    const h = Math.floor(mins / 60)
    const m = mins % 60
    rel = m > 0 ? `${h}h ${m}m ago` : `${h}h ago`
  } else rel = `${Math.floor(mins / (24 * 60))}d ago`
  return `Started ${clock} (${rel})`
}

export function formatDurationCompact(durationNs: number): string {
  if (!durationNs || durationNs <= 0) {
    return '0ms'
  }

  const durationMs = durationNs / 1000000

  if (durationMs < 1000) {
    return `${Math.round(durationMs)}ms`
  } else if (durationMs < 60000) {
    return `${(durationMs / 1000).toFixed(1)}s`
  } else {
    return `${(durationMs / 60000).toFixed(1)}m`
  }
}
