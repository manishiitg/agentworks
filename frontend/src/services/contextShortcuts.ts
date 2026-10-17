import api from './api'
import { captureChatIdentity } from '../utils/chatIdentity'

export type ShortcutProduct = 'agentworks' | 'relays' | 'work' | 'code'
export type ContextShortcutPreference = { version: 1; revision: number; shortcuts: string[] | null; inherited_from?: ShortcutProduct }

function validate(value: ContextShortcutPreference): ContextShortcutPreference {
  if (value.version !== 1 || !Number.isSafeInteger(value.revision) || value.revision < 0 ||
    (value.shortcuts !== null && (!Array.isArray(value.shortcuts) || value.shortcuts.length > 64 ||
      value.shortcuts.some(id => typeof id !== 'string' || !/^[a-z][a-z0-9:-]{0,79}$/.test(id)) || new Set(value.shortcuts).size !== value.shortcuts.length))) throw new Error('Invalid shortcut preferences')
  return value
}
export const contextShortcutsApi = {
  get: async (product: ShortcutProduct, legacy?: ShortcutProduct, signal?: AbortSignal) => validate((await api.get<ContextShortcutPreference>(`/api/workspace-shortcuts/${product}`, {
    params: legacy ? { legacy } : undefined, signal, skipSessionContext: true, chatIdentityGeneration: captureChatIdentity(),
  })).data),
  save: async (product: ShortcutProduct, record: ContextShortcutPreference, signal?: AbortSignal) => validate((await api.put<ContextShortcutPreference>(`/api/workspace-shortcuts/${product}`, {
    version: 1, revision: record.revision, shortcuts: record.shortcuts,
  }, { signal, skipSessionContext: true, chatIdentityGeneration: captureChatIdentity() })).data),
}
