import { expect, it } from 'vitest'
import { supportsNativeGoal, visibleNativeGoalCommands } from './nativeGoalCapability'
it('offers goal only when the selected chat program has server-reported support (PLAT-746)', () => {
  const programs = { first: true, second: false }
  const commands = [{ command: 'goal' }, { command: 'compact' }]
  expect(visibleNativeGoalCommands(commands, supportsNativeGoal(programs, 'first'))).toEqual(commands)
  for (const provider of ['second', 'unknown', null]) expect(visibleNativeGoalCommands(commands, supportsNativeGoal(programs, provider))).toEqual([{ command: 'compact' }])
  expect(supportsNativeGoal(undefined, 'first')).toBe(false)
  // Adding native support is a server change, not a frontend program-name list.
  expect(supportsNativeGoal({ ...programs, second: true }, 'second')).toBe(true)
})
