// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'
import { ConversationMarkdownRenderer } from './MarkdownRenderer'
import { ConversationImageRoot } from './ConversationImageRoot'

vi.mock('../../services/api', () => ({
  getApiBaseUrl: () => 'http://localhost:18743', getAuthToken: () => 'app-session',
  apiUrlForPath: (route: string) => `http://localhost:18743${route}`,
  workspaceApi: {}, agentApi: {},
}))
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

// #2973: a browser screenshot was saved, but the bare filename in the reply
// went to the app page instead of the authenticated project file endpoint.
it('loads relative reply images from the displayed project and changes scope without leaking the old root', async () => {
  const fetchFile = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response('image', { headers: { 'Content-Type': 'image/png' } }))
  vi.spyOn(URL, 'createObjectURL').mockReturnValue('blob:project-image')
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {})
  const host = document.createElement('div')
  const root = createRoot(host)
  const reply = '![Screenshot](shot-test.png)\n\n![External](https://example.com/photo.png)'
  const render = async (files: string) => act(async () => root.render(
    <ConversationImageRoot.Provider value={files}><ConversationMarkdownRenderer content={reply} /></ConversationImageRoot.Provider>,
  ))
  try {
    await render('Crew/first/_files')
    const path = () => atob(new URL(String(fetchFile.mock.calls.at(-1)![0])).searchParams.get('path')!)
    expect(path()).toBe('Crew/first/_files/shot-test.png')
    expect(fetchFile.mock.calls[0][1]?.headers).toEqual({ Authorization: 'Bearer app-session' })
    expect(host.querySelector('img[src="blob:project-image"]')).not.toBeNull()
    expect(host.querySelector('img[src="https://example.com/photo.png"]')).not.toBeNull()
    await render('Crew/second/_files')
    expect(path()).toBe('Crew/second/_files/shot-test.png')
    // Absolute and fully qualified workspace image paths keep their existing admission.
    await act(async () => root.render(<ConversationImageRoot.Provider value="Crew/second/_files"><ConversationMarkdownRenderer content={'![Absolute](/Users/owner/project/chart.png)\n\n![Workspace](Crew/first/_files/chart.png)'} /></ConversationImageRoot.Provider>))
    const paths = fetchFile.mock.calls.slice(-2).map(([url]) => atob(new URL(String(url)).searchParams.get('path')!))
    expect(paths).toEqual(['/Users/owner/project/chart.png', 'Crew/first/_files/chart.png'])
  } finally {
    await act(async () => root.unmount())
    vi.restoreAllMocks()
  }
})
