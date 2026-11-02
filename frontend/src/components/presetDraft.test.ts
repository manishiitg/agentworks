import { expect, it } from 'vitest'
import { presetDraftChange } from './presetDraft'

// PLAT-746-296: a store refresh re-ran the preset effect and wiped what the person typed.
it('keeps typing through refreshes and touches the draft only when the selected preset changes', () => {
  // No preset, a refresh (same tab, same empty preset): typed text survives.
  let state = presetDraftChange(null, 'A', '', '', '')
  expect(presetDraftChange(state.applied, 'A', '', '', 'half typed').inputText).toBeUndefined()

  // Selecting a preset fills in its text once.
  state = presetDraftChange(state.applied, 'A', 'p1', 'Summarise the report', 'half typed')
  expect(state.inputText).toBe('Summarise the report')
  // The person edits it; a refresh of the same preset must not put the original text back.
  expect(presetDraftChange(state.applied, 'A', 'p1', 'Summarise the report', 'Summarise the report for Monday').inputText).toBeUndefined()

  // Clearing the preset empties the box only while it still holds exactly the preset's text.
  expect(presetDraftChange(state.applied, 'A', '', '', 'Summarise the report for Monday').inputText).toBeUndefined()
  expect(presetDraftChange(state.applied, 'A', '', '', 'Summarise the report').inputText).toBe('')
})
