/** The preset last applied to a tab's draft. */
export type AppliedPreset = { tabId: string; id: string; query: string }

/**
 * What a preset change does to the chat draft (PLAT-746-296). Only a real change of the selected preset touches the
 * draft: selecting one fills in its text, and clearing it empties the box only while it still holds exactly that
 * preset's text. Re-runs caused by a store refresh (same tab, same preset) never touch what the person typed.
 */
export function presetDraftChange(previous: AppliedPreset | null, tabId: string, presetId: string, presetQuery: string | undefined, currentInput: string): { inputText?: string; applied: AppliedPreset | null } {
  const sameTab = previous?.tabId === tabId
  if (sameTab && previous.id === presetId) return { applied: previous }
  if (presetId) {
    // Wait for the preset to load before recording it, so its text is applied once it arrives.
    if (presetQuery === undefined) return { applied: previous }
    return { inputText: presetQuery && currentInput !== presetQuery ? presetQuery : undefined, applied: { tabId, id: presetId, query: presetQuery } }
  }
  const clears = sameTab && !!previous.id && !!previous.query && currentInput === previous.query
  return { inputText: clears ? '' : undefined, applied: { tabId, id: '', query: '' } }
}
