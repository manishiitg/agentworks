// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const { askMessages } = vi.hoisted(() => ({ askMessages: [] as string[] }))
vi.mock('../workflow/AskAIButton', () => ({
  AskAIButton: (props: Record<string, unknown>) => {
    askMessages.push(String(props.message))
    return <button type="button" data-testid="ask-ai" data-message={String(props.message)}>{String(props.label ?? 'Ask AI')}</button>
  },
}))

import BackupPopupBody from './BackupPopupBody'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const strategies = [
  { id: 'git', label: 'GitHub / remote Git', description: 'Off-device protection.', best_for: ['config', 'learnings'] },
  { id: 'object_store', label: 'R2 / S3 / B2', description: 'For large files.', best_for: ['media'] },
]

async function mount(info: Record<string, unknown>, extra: Record<string, unknown> = {}) {
  askMessages.length = 0
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(
    <BackupPopupBody
      loadInfo={async () => info as never}
      fallbackStrategies={strategies as never}
      subtitle="Remote backups"
      emptyDestinationsText="No backup destinations yet."
      destinationsHelp="Status updates automatically."
      getSummary={() => 'No backup yet. Set one up.'}
      askContext={{ workspacePath: 'Workflow/test', strategyVerb: 'back up this workflow with' }}
      {...extra}
    />,
  ))
  return { host, unmount: async () => { await act(async () => root.unmount()); host.remove() } }
}

it('shows a short summary with three stats and no internals', async () => {
  const { host, unmount } = await mount({ effective_state: 'not_configured', config: { enabled: false, destinations: [] }, status: {} })
  try {
    expect(host.textContent).toContain('No backup yet. Set one up.')
    expect(host.textContent).toContain('Last success')
    expect(host.textContent).toContain('Tracked files')
    expect(host.textContent).not.toContain('Source hash')
    expect(host.textContent).not.toContain('Status:')
    expect([...host.querySelectorAll('h3')].map(heading => heading.textContent)).not.toContain('Remote backup')
  } finally {
    await unmount()
  }
})

it('keeps strategies behind a collapsed disclosure', async () => {
  const { host, unmount } = await mount({ effective_state: 'healthy', config: { enabled: true, destinations: [] }, status: {} })
  try {
    const details = host.querySelector('details')
    const summary = details?.querySelector('summary')
    expect(summary?.textContent).toContain('Supported strategies')
    expect(summary?.querySelector('.rounded-full')?.textContent).toBe('2')
    expect(details?.open).toBe(false)
    expect(host.textContent).toContain('learnings')
  } finally {
    await unmount()
  }
})

it('renders destinations without kickers or commit hashes', async () => {
  const { host, unmount } = await mount({
    effective_state: 'healthy',
    config: { enabled: true, destinations: [{ id: 'github', provider: 'github', type: 'git', repo: 'org/repo', covers: ['config'] }] },
    status: { destinations: [{ id: 'github', state: 'healthy', commit: 'abcdef123456', summary: 'Backed up 34 files with mirrors.' }] },
  })
  try {
    expect(host.textContent).toContain('github')
    expect(host.textContent).toContain('org/repo')
    expect(host.textContent).not.toContain('abcdef12')
    expect(host.textContent).not.toContain('Backed up 34 files with mirrors.')
    for (const chip of host.querySelectorAll('.rounded.bg-muted')) {
      expect(chip.className).not.toContain('uppercase')
    }
  } finally {
    await unmount()
  }
})

it('asks in chat to set up and per strategy', async () => {
  const { host, unmount } = await mount(
    { effective_state: 'healthy', config: { enabled: true, destinations: [] }, status: {} },
  )
  try {
    expect(askMessages).toContain('/backup')
    expect(askMessages).toContain('Help me back up this workflow with GitHub / remote Git. Explain what I need and walk me through it.')
    expect(host.querySelectorAll('[data-testid="ask-ai"]').length).toBe(3)
  } finally {
    await unmount()
  }
})
