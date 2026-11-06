// @vitest-environment happy-dom
import { expect, it, vi } from 'vitest'
import { installDashboardActionButtons } from './reportActionButtons'

// Owner, 6 Nov: no button that can't run. A button whose function can't run here is not shown, and comes back as it was.
it('shows no function button when nothing here can run it', () => {
  document.body.innerHTML = '<button data-report-action="refresh_leads">Refresh leads</button>'
  const button = document.querySelector('button')!
  const actions = installDashboardActionButtons(document, { available: false, open: vi.fn() } as never)
  expect(button.hidden).toBe(true)
  actions.dispose()
  expect(button.hidden).toBe(false)
  const live = installDashboardActionButtons(document, { available: true, open: vi.fn() } as never)
  expect(button.hidden).toBe(false)
  live.dispose()
})
