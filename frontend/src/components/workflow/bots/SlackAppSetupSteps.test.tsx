// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import { SlackManifestSetup, SlackPermissionsChecklist } from './SlackAppSetupSteps'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

async function render(node: React.ReactNode) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(node))
  return { host, unmount: async () => { await act(async () => root.unmount()); host.remove() } }
}

it('copies the Slack manifest without submitting or closing the bot form', async () => {
  const writeText = vi.fn().mockResolvedValue(undefined)
  vi.stubGlobal('navigator', { clipboard: { writeText } })
  const submit = vi.fn((event: React.FormEvent) => event.preventDefault())
  const { host, unmount } = await render(
    <form onSubmit={submit}>
      <details open>
        <summary>Slack app setup instructions</summary>
        <SlackManifestSetup finalStep="Save the bot here." />
      </details>
    </form>,
  )
  try {
    const manifest = host.querySelector('pre')!.textContent
    await act(async () => host.querySelector<HTMLButtonElement>('button')!.click())
    expect(writeText).toHaveBeenCalledWith(manifest)
    expect(submit).not.toHaveBeenCalled()
    expect(host.querySelector('details')!.open).toBe(true)
    expect(host.querySelector('button')!.textContent).toBe('Copied')
  } finally {
    await unmount()
    vi.unstubAllGlobals()
  }
})

describe('SlackPermissionsChecklist', () => {
  it('talks about channels for channel bots', async () => {
    const { host, unmount } = await render(<SlackPermissionsChecklist />)
    expect(host.textContent).toContain('channels always run in Run mode')
    expect(host.textContent).toContain('confirm delivery with an @mention')
    await unmount()
  })
  it('drops channel talk for DMs-only Codes', async () => {
    const { host, unmount } = await render(<SlackPermissionsChecklist directMessagesOnly />)
    expect(host.textContent).not.toContain('channel')
    expect(host.textContent).not.toContain('@mention')
    expect(host.textContent).toContain('confirm delivery with a direct message to the bot')
    await unmount()
  })
})
