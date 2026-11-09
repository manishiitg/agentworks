import { Layers } from 'lucide-react'
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
}]
