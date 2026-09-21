import type { ChatHistorySession } from '../services/api-types'

export function chatHistoryRuntimeLabel(session: ChatHistorySession): string | undefined {
  const runtime = session.runtime
  const provider = runtime?.provider?.trim()
  if (!runtime || !provider) return undefined

  const model = runtime.model_id?.trim()
  if (model && model !== provider) return `${provider} · ${model}`
  return provider
}

export function chatHistoryRuntimeShortLabel(session: ChatHistorySession): string | undefined {
  const provider = session.runtime?.provider?.trim()
  if (!provider) return undefined

  const knownLabels: Record<string, string> = {
    'claude-code': 'Claude',
    'codex-cli': 'Codex',
    'cursor-cli': 'Cursor',
    'gemini-cli': 'Gemini',
    'muse-cli': 'Muse',
    'pi-cli': 'Pi',
    'agy-cli': 'Agy',
  }
  return knownLabels[provider.toLowerCase()] || provider.replace(/[-_](cli|code)$/i, '')
}
