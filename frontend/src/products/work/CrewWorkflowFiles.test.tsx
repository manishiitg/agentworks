// @vitest-environment happy-dom
import React, { act } from 'react'
import { createRoot } from 'react-dom/client'
import { expect, it, vi } from 'vitest'

const get = vi.hoisted(() => vi.fn())
vi.mock('../../api/crewCodeFiles', () => ({ crewCodeFilesApi: { get } }))

import { CrewWorkflowFiles } from './CrewWorkflowFiles'
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true })

const file = (name: string, lines = 10) => ({ name, size: lines * 20, lines })

it('lists main files, one file per task and the rest, and opens a file with Open', async () => {
  get.mockResolvedValue([file('main.py', 15), file('script.json', 60), file('github_issues_tracker.py', 163), file('slack_message_notifier.py', 98), file('helpers/util.py', 5)])
  const opened: string[] = []
  const host = document.createElement('div')
  document.body.appendChild(host)
  const root = createRoot(host)
  await act(async () => root.render(<CrewWorkflowFiles workspacePath="Workflow/w1" tasks={['github-issues-tracker', 'slack-message-notifier']} descriptions={{ 'github-issues-tracker': 'Tracks issues.' }} onOpenFile={name => opened.push(name)} />))
  await act(async () => undefined)
  try {
    expect(host.querySelector('[data-testid="crew-workflow-files-count"]')?.textContent).toBe('5 files in this workflow\'s code folder.')
    expect(host.textContent).toContain('Main files')
    expect(host.textContent).toContain('Task: github-issues-tracker')
    expect(host.textContent).toContain('Tracks issues.')
    expect(host.textContent).toContain('Other files')
    expect(host.querySelector('[data-testid="file-helpers/util.py"]')).not.toBeNull()
    await act(async () => { (host.querySelector('[data-testid="file-github_issues_tracker.py"] button[title="Open github_issues_tracker.py"]') as HTMLButtonElement).click() })
    expect(opened).toEqual(['github_issues_tracker.py'])
  } finally { await act(async () => root.unmount()); host.remove() }
})
