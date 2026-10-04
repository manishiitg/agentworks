import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('ConnectorsBrowser header', () => {
  const connectors = readFileSync('src/components/connectors/ConnectorsBrowser.tsx', 'utf8')

  it('has no equal-weight Add via JSON button in the find row', () => {
    expect(connectors).not.toContain('Add via JSON')
  })

  it('shows a single connected-first list instead of connected/not-connected filter tabs', () => {
    expect(connectors).not.toContain('setFilter')
    expect(connectors).not.toContain('Not connected (')
    expect(connectors).toContain("connection === 'connected' ? 0 : 1")
  })

  it('splits the single list under Connected and Others headings with no counts', () => {
    expect(connectors).toContain('<span>Connected</span>')
    expect(connectors).toContain('<span>Others</span>')
    expect(connectors).not.toMatch(/\(\{\w+\.length\}\)/)
    expect(connectors).not.toContain('of ${total}')
  })

  it('groups unconnected connectors into fine job-based shelves', () => {
    const catalog = readFileSync('src/components/connectors/catalog.ts', 'utf8')

    expect(catalog).toContain("Stripe: 'payments'")
    expect(catalog).toContain("Intercom: 'customers'")
    expect(catalog).toContain("Indeed: 'hiring'")
    expect(catalog).toContain("Exa: 'search'")
    expect(catalog).toContain("Airtable: 'data'")
    expect(catalog).toContain("Apify: 'advanced'")
    expect(catalog).toContain("Sentry: 'developer'")
    expect(catalog).toContain("label: 'Payments'")
    expect(catalog).toContain("label: 'Hiring & HR'")
    expect(catalog).toContain("label: 'Social networks'")
    expect(catalog).toContain("label: 'SEO'")
    expect(catalog).toContain("label: 'GTM'")
    expect(catalog).toContain("label: 'Releases'")
    expect(catalog).toContain("label: 'Search'")
    expect(catalog).toContain("label: 'Data'")
    expect(catalog).not.toContain('Data & research')
    expect(catalog).toContain("label: 'Developer tools'")
    expect(connectors).toContain('GROUP_ORDER')
    expect(connectors).toContain('groupFor(')
  })

  it('keeps the raw JSON config behind a small debug icon that opens the existing popup', () => {
    expect(connectors).toContain('<Bug')
    expect(connectors).toContain('setShowJsonConfig(true)')
    expect(connectors).toContain('<MCPConfigPopup')
  })

  it('labels the banner action as asking the assistant to connect', () => {
    expect(connectors).not.toContain('Add platform MCP')
    expect(connectors).toContain('Ask ${assistantLabel} to connect')
  })

  it('keeps the banner in plain non-technical language', () => {
    expect(connectors).not.toContain('Need another connection?')
    expect(connectors).not.toContain('Why admin access?')
    expect(connectors).toContain("Can't find the app you need?")
    // Everyone can now connect an app privately, so the "Why can't I add one?"
    // explainer is gone; the banner says plainly who connects it and how to share.
    expect(connectors).not.toContain("Why can't I add one?")
    expect(connectors).toContain('can connect it privately for you. Use Vault to share access.')
    expect(connectors).toContain('Ask ${assistantLabel} to connect')
  })
})
