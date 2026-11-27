import { GitFork, Layers, MessageCircleQuestion } from 'lucide-react'
import { forkChatTab } from '../utils/forkChatTab'
import { askBtw, btwUnavailableReason, showBtwHistory } from '../utils/btwSideQuestion'
import { useChatStore } from '../stores/useChatStore'
import type { CommandDefinition } from './types'
import { changeChatContext } from '../products/work/ChatContextItems'

/** Direct chat actions, not prompts sent to the agent. The server decides availability. */
export const sessionCommands: CommandDefinition[] = [{
  command: 'compact',
  description: 'Compact the current conversation',
  icon: <Layers className="h-4 w-4" />,
  source: 'builtin',
  validate: ctx => ctx.tabSessionId ? null : 'Send a message first to start this chat.',
  execute: ctx => { if (ctx.tabSessionId) void changeChatContext(ctx.tabSessionId, { action: 'compact' }) },
}, {
  command: 'fork',
  description: 'Copy this conversation into an independent chat tab',
  icon: <GitFork className="h-4 w-4" />,
  modes: ['multi-agent'],
  source: 'builtin',
  validate: ctx => {
    const tab = useChatStore.getState().chatTabs[ctx.activeTabId]
    return tab?.metadata?.agentProfileId === 'work' && tab.metadata.agentProfileTabCanWrite !== false ? null : 'Open one of your own project chats to fork it.'
  },
  execute: ctx => { void forkChatTab(ctx.activeTabId) },
}, {
  // A side question answered on a hidden copy of this chat; the chat keeps working (btwSideQuestion.ts).
  command: 'btw',
  description: 'Ask on the side: the chat keeps working, nothing is added to it',
  icon: <MessageCircleQuestion className="h-4 w-4" />,
  modes: ['multi-agent'],
  source: 'builtin',
  validate: ctx => ctx.tabSessionId ? btwUnavailableReason(ctx.activeTabId) : 'Send a message first to start this chat.',
  execute: ctx => {
    const question = ctx.beforeSlash.trim()
    if (question) {
      void askBtw(ctx.activeTabId, question)
      return
    }
    // /btw alone lists earlier side questions and leaves "/btw " in the box to type the next one.
    showBtwHistory(ctx.activeTabId)
    ctx.setInputText('/btw ')
  },
}]
