import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const read = (path: string) => readFileSync(path, 'utf8')

function rawCount(source: string) {
  return source.match(/<(input|button|textarea|select)[\s>]/g)?.length ?? 0
}

describe('settings form kit adoption', () => {
  it('builds Slack from the shared kit with only native radios left raw', () => {
    const slack = read('src/components/workflow/bots/SlackSetup.tsx')

    expect(slack).toContain("from '../../ui/SecretField'")
    expect(slack).toContain("from '../../ui/ToggleRow'")
    expect(slack).toContain("from '../../ui/FormSection'")
    expect(slack).toContain("from '../../ui/Input'")
    expect(slack).not.toContain('toggleClass')
    expect(slack).not.toContain('peer-checked')
    expect(slack).not.toContain('<button')
    expect(slack).not.toContain('<textarea')
    // The own-bot / shared-bot radio stays native (one ModeOption renders
    // both): no RadioGroup in the kit. The "One of my bots" picker is a
    // native select, like the other settings dropdowns.
    expect(rawCount(slack)).toBe(2)
  })

  it('builds Gmail from the shared kit with zero raw form elements', () => {
    const gmail = read('src/components/workflow/bots/GmailNotifications.tsx')

    expect(gmail).toContain("from '../../ui/ToggleRow'")
    expect(gmail).toContain("from '../../ui/FormSection'")
    expect(gmail).toContain("from '../../ui/Input'")
    expect(gmail).toContain("from '../../ui/checkbox'")
    expect(gmail).not.toContain('peer-checked')
    expect(gmail).not.toContain('<input')
    expect(gmail).not.toContain('<textarea')
    expect(gmail).not.toContain('<button')
    expect(rawCount(gmail)).toBe(0)
  })

  it('builds the secrets section with zero raw form elements', () => {
    for (const path of [
      'src/components/secrets/SecretSelectionSection.tsx',
    ]) {
      const source = read(path)
      expect(source).toContain("from '../ui/Button'")
      expect(source).toContain("from '../ui/badge'")
      expect(source).toContain("from '../ui/SettingsCard'")
      expect(source).not.toContain('bg-amber-600')
      expect(source).not.toContain('focus:ring-amber-500')
      expect(source).not.toContain('dark:bg-gray-800')
      expect(source).not.toContain('dark:bg-gray-900')
      expect(source).not.toContain('dark:hover:bg-gray-700')
      expect(source).not.toContain('dark:border-gray-')
      expect(source).not.toContain('text-red-500')
      expect(source).not.toContain('text-red-600')
      expect(source).not.toContain('bg-amber-100')
      expect(source).not.toContain('bg-blue-100')
      expect(rawCount(source)).toBe(0)
    }
  })

  it('builds identity cards from the shared settings card', () => {
    for (const path of [
      'src/components/workflow/WorkflowIdentityPanel.tsx',
      'src/products/work/WorkIdentityPanel.tsx',
      'src/components/workflow/WorkflowFolderAccessView.tsx',
    ]) {
      const source = read(path)
      expect(source).toContain('SettingsCard')
      expect(source).toContain('<SettingsCard')
      expect(source).not.toContain('<FormSection')
    }
  })

  it('builds folders and browser settings from the kit', () => {
    const folders = read('src/components/workflow/WorkflowFolderAccessView.tsx')
    expect(folders).toContain("from '../ui/Button'")
    expect(folders).toContain("from '../ui/Input'")
    expect(folders).not.toContain('<input')
    expect(folders).not.toContain('<button')
    // The access dropdown stays a native select: same behavior, themed classes.
    expect(rawCount(folders)).toBe(1)

    const browser = read('src/components/workflow/BrowserWorkspacePanel.tsx')
    expect(browser).toContain("from '../ui/Button'")
    expect(rawCount(browser)).toBe(0)

    const settings = read('src/components/BrowserAutomationSettings.tsx')
    expect(settings).toContain("from './ui/Button'")
    expect(settings).not.toContain('<button')
    expect(settings).not.toContain('<textarea')
  })

  it('builds playbooks from the kit with navigation rows left raw', () => {
    const playbooks = read('src/components/playbooks/PlaybooksPanel.tsx')

    expect(playbooks).toContain("from '../ui/Button'")
    expect(playbooks).toContain("from '../ui/Input'")
    expect(playbooks).toContain('<WorkspaceViewTabs')
    expect(playbooks).not.toContain('<input')
    expect(playbooks).not.toContain('tabClass')
  })

  it('builds LLM actions from the kit with selection controls left raw', () => {
    const llm = read('src/components/workflow/WorkflowLLMConfigurationPanel.tsx')

    expect(llm).toContain("from '../ui/Button'")
    expect(llm).toContain("from '../ui/Input'")
    expect(llm).toContain('size="xs"')
    // Dense rows keep native radios and disclosure toggles; actions are kit.
    expect(llm).not.toContain('<input')
    expect(llm).not.toContain('px-2 py-0.5 text-xs font-medium text-primary-foreground')
  })

  it('builds access bodies from settings cards with selects left raw', () => {
    const users = read('src/components/admin/UsersAdminPanel.tsx')
    expect(users).toContain("from '../ui/SettingsCard'")
    expect(users).toContain("from '../ui/Button'")
    expect(users).toContain("from '../ui/checkbox'")
    expect(users).toContain("from '../ui/badge'")
    expect(users).toContain("from '../ui/SecretField'")
    expect(users).toContain("from '../ui/ConfirmationDialog'")
    expect(users).toContain('requireText')
    expect(users).not.toContain('<input')
    expect(users).not.toContain('<button')
    expect(users).not.toContain('window.confirm')
    // The role dropdowns (each user's, and the Add a user form's) stay native
    // selects: same behavior, themed classes.
    expect(rawCount(users)).toBe(2)

    const share = read('src/components/workflow/WorkflowSharePopup.tsx')
    expect(share).toContain("from '../ui/SettingsCard'")
    expect(share).toContain("from '../ui/Button'")
    expect(share).not.toContain('<input')
    expect(share).not.toContain('<button')
    // The person, role and each person's role dropdowns stay native selects.
    expect(rawCount(share)).toBe(3)
  })

  it('puts access tabs in the shared header with standard actions', () => {
    const shell = read('src/components/workflow/WorkflowAccessView.tsx')
    expect(shell).toContain('tabs={')
    expect(shell).toContain("ariaLabel: 'Access sections'")
    expect(shell).toContain('<WorkspaceViewActions')
    expect(shell).toContain('usePersistentTab')
    expect(shell).toContain('getAccessTabAskAIMessage')
    expect(shell).not.toContain('tabClass')
    expect(shell).not.toContain('role="tablist"')
    // The shell owns the single Ask AI + refresh pair; the host passes none.
    expect(shell).not.toContain('headerAction')
  })
})
