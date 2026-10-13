// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it } from 'vitest'
import { SlackPermissionsChecklist } from './SlackAppSetupSteps'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

async function render(node: React.ReactNode) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(node))
  return { host, unmount: async () => { await act(async () => root.unmount()); host.remove() } }
}

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
