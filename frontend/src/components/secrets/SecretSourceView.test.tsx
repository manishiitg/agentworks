// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import SecretSourceView from './SecretSourceView'

const load = vi.hoisted(() => vi.fn())
vi.mock('../../api/secrets', () => ({ secretsApi: { getSecretSources: load } }))

it('shows server source explanations and keeps unavailable distinct from an empty setup', async () => {
  const host = document.createElement('div')
  document.body.append(host)
  const root = createRoot(host)
  load.mockResolvedValueOnce({ status: 'available', note: 'Saved setup; values are not checked.', warnings: [], secrets: [{ name: 'REVIEW_TOKEN', display_name: 'REVIEW_TOKEN', explanation: 'The own copy takes priority over the company Vault copy.' }] })
  await act(async () => root.render(<SecretSourceView workflowPath="Workflow/check" />))
  expect(host.textContent).toContain('REVIEW_TOKEN')
  expect(host.textContent).toContain('own copy takes priority')
  load.mockResolvedValueOnce({ status: 'unavailable', note: 'Secret sources are unavailable.', warnings: [], secrets: [] })
  await act(async () => (host.querySelector('button') as HTMLButtonElement).click())
  expect(host.textContent).toContain('unavailable')
  expect(host.textContent).not.toContain('No secrets are stored')
  await act(async () => root.unmount())
  host.remove()
})
