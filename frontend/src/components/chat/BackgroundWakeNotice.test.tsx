// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { BackgroundWakeNotice } from './BackgroundWakeNotice'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('shows server-marked native wakes compactly with plain output collapsed, never guesses from prompt text (PLAT-746-993)', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  try {
    for (const kind of ['background_command_finished', 'background_work_finished']) {
      await act(async () => root.render(<BackgroundWakeNotice metadata={{ system_notice_kind: kind, background_failed: false, background_output: '<script>output</script>' }} />))
      expect(host.textContent).toContain('the agent is continuing')
      expect(host.querySelector('details')?.open).toBe(false)
      expect(host.querySelector('summary')?.textContent).toBe('Show output')
      expect(host.querySelector('pre')?.textContent).toBe('<script>output</script>')
      expect(host.querySelector('script')).toBeNull()
    }
    await act(async () => root.render(<BackgroundWakeNotice metadata={{ system_notice_kind: 'background_command_finished', background_failed: true }} />))
    expect(host.textContent).toContain('✗ Background command finished with an error')
    expect(host.querySelector('details')).toBeNull()
    await act(async () => root.render(<BackgroundWakeNotice metadata={{ content: '[Background command finished]' }} />))
    expect(host.textContent).toBe('')
  } finally { await act(async () => root.unmount()) }
})
