// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

const auth = vi.hoisted(() => ({ user: { can_create: true }, isMultiUserMode: true }))
vi.mock('../../stores/useAuthStore', () => ({ useAuthStore: (selector: (state: typeof auth) => unknown) => selector(auth) }))

import { RelayIntro } from './RelayIntro'
import { useCommandDialogStore } from '../../stores/useCommandDialogStore'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
const host = document.createElement('div')
const root = createRoot(host)
afterEach(async () => {
  await act(async () => root.render(null))
  useCommandDialogStore.getState().closeAll()
})

it.each([true, false])('only requests the shared creation dialog when allowed (%s)', async allowed => {
  auth.user.can_create = allowed
  await act(async () => root.render(<RelayIntro />))
  const button = host.querySelector('button')!
  expect(button.disabled).toBe(!allowed)
  await act(async () => button.click())
  expect(useCommandDialogStore.getState().showPresetCreate).toBe(allowed)
})
