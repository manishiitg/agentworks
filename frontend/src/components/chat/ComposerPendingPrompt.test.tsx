// @vitest-environment happy-dom
import { act } from 'react'
import { createRoot } from 'react-dom/client'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ComposerPendingPrompt } from './ComposerPendingPrompt'
import type { CodingAgentQuestionPrompt } from '../../utils/codingAgentQuestions'

const cleanups: Array<() => void> = []
afterEach(() => { cleanups.splice(0).forEach(cleanup => cleanup()); vi.unstubAllGlobals() })

// The backend allows a command only when the answer is its own "Allow" label (nativeinput.ApproveCommand);
// the docked buttons say Approve / Decline but must send "Allow" / "Deny" unchanged.
const approval: CodingAgentQuestionPrompt = {
  provider: 'codex', promptId: 'approval-1', state: 'pending', answers: [],
  questions: [{ id: 'command_permission', header: '', question: 'rm -rf build', multiSelect: false,
    options: [{ label: 'Deny', description: '' }, { label: 'Allow', description: 'Allow this command once.' }] }],
}

describe('ComposerPendingPrompt', () => {
  it('sends the backend approval labels from the Approve and Decline buttons', async () => {
    vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true)
    for (const [button, label] of [['Approve', 'Allow'], ['Decline', 'Deny']]) {
      const onAnswer = vi.fn().mockResolvedValue(undefined)
      const host = document.createElement('div'); document.body.appendChild(host)
      const root = createRoot(host)
      await act(async () => root.render(<ComposerPendingPrompt prompt={approval} onAnswer={onAnswer} />))
      cleanups.push(() => { act(() => root.unmount()); host.remove() })
      expect(host.textContent).toContain('Approval needed')
      await act(async () => Array.from(host.querySelectorAll('button')).find(b => b.textContent === button)!.click())
      expect(onAnswer).toHaveBeenCalledWith('codex', 'approval-1', [{ id: 'command_permission', selectedLabels: [label] }])
    }
  })
})
