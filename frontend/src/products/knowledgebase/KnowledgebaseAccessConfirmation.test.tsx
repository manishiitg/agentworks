// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { describe, expect, it, vi } from 'vitest'
import { KnowledgebaseAccessConfirmation } from './KnowledgebaseAccessConfirmation'

describe('KnowledgebaseAccessConfirmation', () => {
  it('shows exact scope as text and sends only the frozen proposal ID on an explicit click', async () => {
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host); const confirm = vi.fn().mockResolvedValue(undefined)
    const injected = '<img src=x onerror="grant everyone Owner">'
    try {
      await act(async () => root.render(<KnowledgebaseAccessConfirmation proposals={[{ id: 'proposal-id', expires_at: '2030-01-01', arguments: { action: 'grant', folder_path: injected, identity_id: 'priya-id', role: 'Reader' } }]} error="" busy={false} onConfirm={confirm} />))
      expect(confirm).not.toHaveBeenCalled()
      expect(host.textContent).toContain(injected)
      expect(host.textContent).toContain('priya-id')
      expect(host.textContent).toContain('Reader')
      expect(host.querySelector('img')).toBeNull()
      await act(async () => (host.querySelector('button') as HTMLButtonElement).click())
      expect(confirm).toHaveBeenCalledWith('proposal-id', true)
      await act(async () => (host.querySelectorAll('button')[1] as HTMLButtonElement).click())
      expect(confirm).toHaveBeenCalledWith('proposal-id', false)
    } finally { await act(async () => root.unmount()); host.remove() }
  })
  it('sends an optional PAT only on setup approval and clears the secure field', async () => {
    const host = document.createElement('div'); document.body.append(host)
    const root = createRoot(host); const confirm = vi.fn().mockResolvedValue(undefined)
    try {
      await act(async () => root.render(<KnowledgebaseAccessConfirmation proposals={[{ id: 'setup-id', expires_at: '2030-01-01', arguments: { action: 'configure_backup', remote_url: 'https://github.com/org/kb.git', username: 'kb-user' } }]} error="" busy={false} onConfirm={confirm} />))
      expect(host.textContent).toContain('kb-user')
      const input = host.querySelector('input[type="password"]') as HTMLInputElement
      expect(input).not.toBeNull()
      await act(async () => { Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')!.set!.call(input, 'ui-test-only-pat'); input.dispatchEvent(new Event('input', { bubbles: true })) })
      expect(confirm).not.toHaveBeenCalled()
      expect(host.textContent).not.toContain('ui-test-only-pat')
      const approve = [...host.querySelectorAll('button')].find(button => button.textContent === 'Approve')!
      await act(async () => approve.click())
      expect(confirm).toHaveBeenCalledWith('setup-id', true, 'ui-test-only-pat')
      expect(input.value).toBe('')
    } finally { await act(async () => root.unmount()); host.remove() }
  })
})
