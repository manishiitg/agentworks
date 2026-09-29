import { expect, it } from 'vitest'
import { placeViewKey } from './placeViewMode'

const crew = { metadata: { agentProfileId: 'work', agentProfileProjectId: 'p1' } } as never
const code = { metadata: { agentProfileId: 'code', agentProfileProjectId: 'p1' } } as never
const workflow = { metadata: { presetQueryId: 'wf_1' } } as never

it('keys a tab by its Crew, Code or workflow', () => {
  expect(placeViewKey(crew)).toBe('work:p1')
  expect(placeViewKey(code)).toBe('code:p1')
  expect(placeViewKey(workflow)).toBe('workflow:wf_1')
  expect(placeViewKey({ metadata: {} } as never)).toBeNull()
})
