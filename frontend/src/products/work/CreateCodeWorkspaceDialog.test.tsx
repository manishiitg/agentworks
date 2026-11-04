// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot, type Root } from 'react-dom/client'
import { afterEach, expect, it, vi } from 'vitest'

vi.mock('./RunsOnPicker', () => ({ RunsOnPicker: () => null }))
import { CreateCodeWorkspaceDialog } from './CreateCodeWorkspaceDialog'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
let root: Root | undefined
afterEach(() => { act(() => root?.unmount()); root = undefined; document.body.innerHTML = '' })

async function render(onCreate: (...args: unknown[]) => void) {
  const host = document.createElement('div'); document.body.append(host)
  root = createRoot(host)
  await act(async () => root?.render(<CreateCodeWorkspaceDialog onClose={() => {}} onCreate={onCreate as never} submitting={false} error={null} />))
  return host
}

it('creates from the name alone', async () => {
  const onCreate = vi.fn()
  const host = await render(onCreate)
  const name = host.querySelector<HTMLInputElement>('#code-create-name')!
  await act(async () => {
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(name, 'Sales assistant')
    name.dispatchEvent(new Event('input', { bubbles: true }))
  })
  await act(async () => host.querySelector<HTMLFormElement>('form')!.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true })))
  expect(onCreate).toHaveBeenCalledWith('Sales assistant', undefined)
})
