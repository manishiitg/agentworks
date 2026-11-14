// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it } from 'vitest'
import { SearchMatch } from './SearchMatch'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })
it('highlights mixed-case incoming message matches as text, not HTML (PLAT-856-568)', async () => {
  const host = document.createElement('div'), root = createRoot(host)
  try {
    const text = '<script>DEADLOCK</script> and deadlock'
    await act(async () => root.render(<SearchMatch text={text} query="deadlock" />))
    expect(host.querySelectorAll('mark').length).toBe(2)
    expect(host.textContent).toBe(text)
    expect(host.querySelector('script')).toBeNull()
  } finally { await act(async () => root.unmount()) }
})
