// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

vi.mock('./RunsOnPicker', () => ({ RunsOnPicker: () => null }))
import { CreateCodeWorkspaceDialog } from './CreateCodeWorkspaceDialog'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
afterEach(() => { act(() => root?.unmount()); root = undefined; document.body.innerHTML = '' })

async function render(onCreate: (...args: unknown[]) => void, initialMode?: 'dev' | 'cowork' | 'local') {
  const host = document.createElement('div'); document.body.append(host)
  root = createRoot(host)
  await act(async () => root?.render(<CreateCodeWorkspaceDialog onClose={() => {}} onCreate={onCreate as never} submitting={false} error={null} initialMode={initialMode} />))
  return host
}

it('asks for the mode (Dev, Cowork or Local), starts on Dev, and creates in the chosen one', async () => {
  const onCreate = vi.fn()
  const host = await render(onCreate)
  const radios = [...host.querySelectorAll<HTMLInputElement>('input[type="radio"]')]
  expect(radios).toHaveLength(3)
  expect(host.textContent).toContain('Dev'); expect(host.textContent).toContain('Cowork'); expect(host.textContent).toContain('Local')
  expect(radios[0].checked).toBe(true)
  await act(async () => radios[1].click())
  const name = host.querySelector<HTMLInputElement>('#code-create-name')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(name, 'Sales assistant')
    name.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => host.querySelector<HTMLFormElement>('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(onCreate).toHaveBeenCalledWith('Sales assistant', undefined, 'cowork')
})

it('can open on another mode (the Switch mode explanation sends people here)', async () => {
  const host = await render(vi.fn(), 'local')
  expect([...host.querySelectorAll<HTMLInputElement>('input[type="radio"]')].map(r => r.checked)).toEqual([false, false, true])
})
