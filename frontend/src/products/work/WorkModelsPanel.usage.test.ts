import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

// The Models screen's usage check must ask about the account the project actually uses and leave the decision of who may
// see it to the server (the same /api/provider-setup/sessions rules as the Providers page): an admin-only button that
// always asked about the server's own account showed the wrong account and hid usage from everyone else.
describe('WorkModelsPanel usage check', () => {
  const source = readFileSync('src/products/work/WorkModelsPanel.tsx', 'utf8')

  it('asks about the project\'s own connection, not always the server account', () => {
    expect(source).toContain('checkProviderUsage(selectedOption.provider, savedSelection?.connectionId, replaceRunning)')
    expect(source).not.toContain("startProviderSetup(\n        selectedOption.provider,\n        'usage'")
  })

  it('is not hidden from non-admins in the browser: the server enforces who may see it', () => {
    expect(source).not.toContain('canCheckUsage')
    expect(source).not.toContain('is_admin')
  })

  it('shows read-only usage text when the server returns text instead of a terminal', () => {
    expect(source).toContain('result.usage_output')
    expect(source).toContain('aria-label="Provider usage output"')
  })
})
