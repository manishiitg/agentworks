// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'

import PublishPopupBody from './PublishPopupBody'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const strategies = [
  { id: 'netlify', label: 'Netlify', method: 'cli', description: 'netlify deploy --prod.' },
  { id: 'vercel', label: 'Vercel', method: 'cli', description: 'vercel deploy --prod.' },
]

async function mount(info: Record<string, unknown>) {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(
    <PublishPopupBody
      loadInfo={async () => info as never}
      fallbackStrategies={strategies as never}
      subtitle="Share at a public URL"
      emptyDestinationsText="No hosts yet."
      destinationsHelp="Deploys update automatically."
      defaultTargetLabel="pulse, dashboard"
      getSummary={() => 'Not published yet. Set one up.'}
    />,
  ))
  return { host, unmount: async () => { await act(async () => root.unmount()); host.remove() } }
}

it('shows summary, URL, and access without internals', async () => {
  const { host, unmount } = await mount({
    effective_state: 'published',
    url: 'https://example.netlify.app',
    config: { enabled: true, destinations: [] },
    status: {},
  })
  try {
    expect(host.textContent).toContain('Not published yet. Set one up.')
    expect(host.querySelector('a[aria-label="Open URL"]')).not.toBeNull()
    expect(host.textContent).toContain('Public')
    expect(host.textContent).not.toContain('Public site')
    expect(host.textContent).not.toContain('Status:')
  } finally {
    await unmount()
  }
})

it('keeps hosts behind a collapsed disclosure', async () => {
  const { host, unmount } = await mount({ effective_state: 'published', config: { enabled: true, destinations: [] }, status: {} })
  try {
    const details = host.querySelector('details')
    const summary = details?.querySelector('summary')
    expect(summary?.textContent).toContain('Common hosts')
    expect(summary?.querySelector('.rounded-full')?.textContent).toBe('2')
    expect(details?.open).toBe(false)
  } finally {
    await unmount()
  }
})
