import { describe, expect, it } from 'vitest'
import { applyRuntimeBranding, runtimeLoginNote } from './runtime-branding'

function fakeDocument() {
  const favicon = { rel: 'icon', type: 'image/svg+xml', href: '/logo.svg' }
  return {
    favicon,
    document: {
      title: 'AgentWorks',
      head: { appendChild: () => undefined },
      querySelector: () => favicon,
      createElement: () => favicon,
    } as unknown as Document,
  }
}

describe('runtime branding', () => {
  it('applies an isolated app name and same-origin favicon', () => {
    const target = fakeDocument()
    applyRuntimeBranding({
      appName: 'Video Studio (Dev)',
      faviconUrl: '/video-studio-favicon.svg',
    }, target.document)

    expect(target.document.title).toBe('Video Studio (Dev)')
    expect(target.favicon.href).toBe('/video-studio-favicon.svg')
    expect(target.favicon.type).toBe('image/svg+xml')
  })

  it('rejects an external favicon URL', () => {
    const target = fakeDocument()
    applyRuntimeBranding({ faviconUrl: 'https://example.com/tracker.svg' }, target.document)
    expect(target.favicon.href).toBe('/logo.svg')
  })

  it('rejects control characters in runtime branding values', () => {
    const target = fakeDocument()
    applyRuntimeBranding({
      appName: 'Video Studio\nInjected',
      faviconUrl: '/video-studio-favicon.svg\u0000.png',
    }, target.document)

    expect(target.document.title).toBe('AgentWorks')
    expect(target.favicon.href).toBe('/logo.svg')
  })

  // A deployment can show a note on the sign-in card (for example a shared demo login). Only plain text is
  // kept, bounded; anything malformed is ignored so a bad config never breaks sign-in.
  it('reads a sign-in note and ignores a malformed one', () => {
    expect(runtimeLoginNote({ loginNote: { title: 'Demo login', rows: [['Username', 'demo'], ['Password', 'pw']], text: 'Shared.' } }))
      .toEqual({ title: 'Demo login', rows: [['Username', 'demo'], ['Password', 'pw']], text: 'Shared.' })
    expect(runtimeLoginNote({ loginNote: 'text' })).toBeNull()
    expect(runtimeLoginNote({ loginNote: { rows: [['only label'], 'x', ['', 'v']] } })).toBeNull()
    expect(runtimeLoginNote({})).toBeNull()
  })
})
