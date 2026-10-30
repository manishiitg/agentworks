// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

vi.mock('../../services/workflow-notifications', () => ({ loadWorkflowNotificationInfo: vi.fn() }))
vi.mock('../../services/api', () => ({ workflowManifestApi: { updateWorkflowManifest: vi.fn(async () => ({})) } }))
vi.mock('../../hooks/useCanWriteWorkflow', () => ({ useCanWriteWorkflow: () => true, READ_ONLY_TITLE: 'Read-only access' }))

import WorkflowNotificationView from './WorkflowNotificationView'
import { workflowManifestApi } from '../../services/api'

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const info = {
  breakAlerts: true,
  scopeLabel: 'Shop',
  effectiveState: 'ready',
  gmail: { state: 'ready', default_recipient: 'me@x.com', blocked_recipients: [], default_sender: 'me@x.com', sender_choices: [
    { id: 'gmail_001', email: 'me@x.com', ready: true },
    { id: 'gmail_002', email: 'other@x.com', ready: true },
  ] },
  runSummaryInstructions: 'Summarize the run.',
  runSummaryChannels: ['slack'],
  runSummaryRecipients: [],
  gmailConnectionId: '',
  excludeChannels: [],
}

async function mount() {
  const host = document.createElement('div'); document.body.append(host)
  const root = createRoot(host)
  await act(async () => root.render(
    <WorkflowNotificationView workspacePath="Workflow/test" loadInfo={async () => info as never} />,
  ))
  return { host, unmount: async () => { await act(async () => root.unmount()); host.remove() } }
}

it('keeps the header to scope, one badge, and a short line', async () => {
  const { host, unmount } = await mount()
  try {
    expect(host.textContent).toContain('Shop · Notifications on')
    expect(host.textContent).not.toContain('Agentic notification delivery')
    expect(host.textContent?.match(/Ready/g)?.length).toBe(1)
  } finally {
    await unmount()
  }
})

it('uses title-case section heads without kickers or accent bars', async () => {
  const { host, unmount } = await mount()
  try {
    for (const heading of host.querySelectorAll('h3')) {
      expect(heading.className).not.toContain('uppercase')
      expect(heading.className).not.toContain('tracking-')
    }
    expect(host.querySelector('.border-l-2')).toBeNull()
  } finally {
    await unmount()
  }
})

it('lays summaries out as labeled Channels, From, and To lines', async () => {
  const { host, unmount } = await mount()
  try {
    expect(host.textContent).toContain('Run summary')
    expect(host.textContent).toContain('Channels')
    expect(host.textContent).not.toContain('→ me@x.com')
    expect(host.textContent).toContain('me@x.com')
  } finally {
    await unmount()
  }
})

it('saves one Notify sender without constraining workflow Gmail actions', async () => {
  vi.mocked(workflowManifestApi.updateWorkflowManifest).mockClear()
  const { host, unmount } = await mount()
  try {
    const sender = host.querySelector('select[aria-label="Notify Gmail sender"]') as HTMLSelectElement
    expect(sender).not.toBeNull()
    expect(host.textContent).toContain('Workflow and Builder Gmail actions choose independently.')
    await act(async () => {
      sender.value = 'gmail_002'
      sender.dispatchEvent(new Event('change', { bubbles: true }))
    })
    expect(workflowManifestApi.updateWorkflowManifest).toHaveBeenCalledWith({
      workspace_path: 'Workflow/test',
      notification_gmail_connection_id: 'gmail_002',
    })
  } finally {
    await unmount()
  }
})

it('saves the Goal failure alert setting through the existing manifest endpoint', async () => {
  vi.mocked(workflowManifestApi.updateWorkflowManifest).mockClear()
  const { host, unmount } = await mount()
  try {
    const checkbox = host.querySelector('input[type="checkbox"]') as HTMLInputElement
    expect(checkbox.checked).toBe(true)
    await act(async () => checkbox.click())
    expect(workflowManifestApi.updateWorkflowManifest).toHaveBeenCalledWith({ workspace_path: 'Workflow/test', break_alerts: false })
  } finally { await unmount() }
})
