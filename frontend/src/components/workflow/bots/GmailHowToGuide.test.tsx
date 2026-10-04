// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { GmailHowToGuide } from './GmailHowToGuide'
import { GmailSetupGuide } from './GmailSetupGuide'

vi.mock('../../../services/api', () => ({ getApiBaseUrl: () => 'http://localhost:8000' }))

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

describe('Gmail help', () => {
  it('answers common Gmail tasks inside the walkthrough', () => {
    const host = document.createElement('div')
    host.innerHTML = renderToStaticMarkup(<GmailHowToGuide scopeNoun="project" />)

    const answers = host.querySelectorAll('details')
    expect(answers).toHaveLength(10)
    expect(host.querySelector('details[open]')).toBeNull()
    expect(host.textContent).toContain('Connect Google account')
    expect(host.textContent).toContain('Sign in again with Google')
    expect(host.textContent).toContain('AgentWorks settings')
    expect(host.textContent).toContain('Default recipients')
    expect(host.textContent).toContain('Enable Gmail')
    expect(host.textContent).toContain('Google Auth Platform → Audience → Test users')
    expect(host.textContent).toContain('Crew conversation')
  })

  it('names the workflow context', () => {
    const host = document.createElement('div')
    host.innerHTML = renderToStaticMarkup(<GmailHowToGuide scopeNoun="workflow" />)
    expect(host.textContent).toContain('workflow run')
  })

  it('uses the active Gmail backend and current add-account labels in first-time setup', async () => {
    const host = document.createElement('div')
    document.body.append(host)
    const root = createRoot(host)
    try {
      await act(async () => root.render(<GmailSetupGuide backend="gog" />))
      await act(async () => (host.querySelector('button') as HTMLButtonElement).click())
      expect(host.textContent).toContain('brew install openclaw/tap/gogcli')
      expect(host.textContent).toContain('Connect Google account')
      expect(host.textContent).toContain('gmail.send')
      expect(host.textContent).toContain('gmail.compose')
      expect(host.textContent).not.toContain('Under OAuth clients')

      await act(async () => root.render(<GmailSetupGuide backend="gws" />))
      expect(host.textContent).toContain('@googleworkspace/cli')
      expect(host.textContent).not.toContain('brew install openclaw/tap/gogcli')
    } finally {
      await act(async () => root.unmount())
      host.remove()
    }
  })
})
