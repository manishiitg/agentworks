// Deterministic provider for the real Pi CLI probe. No model/network credentials.
import type { ExtensionAPI } from '@earendil-works/pi-coding-agent';
import { createAssistantMessageEventStream } from '@earendil-works/pi-ai';
import { Type } from 'typebox';
import { appendFileSync } from 'node:fs';

export default function (pi: ExtensionAPI) {
  // Only the TUI probe supplies these test-only paths.
  pi.on('session_start', async (_event, ctx) => {
    if (process.env.PLAT868_DRAFT_REPORT && ctx.mode === 'tui') ctx.ui.setEditorText('PERSON_DRAFT');
  });
  pi.on('message_start', async (event, ctx) => {
    if (process.env.PLAT868_DRAFT_REPORT && ctx.mode === 'tui' && event.message.role === 'user') {
      appendFileSync(process.env.PLAT868_DRAFT_REPORT, JSON.stringify({ draft: ctx.ui.getEditorText() }) + '\n');
    }
  });
  pi.registerTool({
    name: 'fixture_wait', label: 'Fixture wait', description: 'Wait in a real Pi tool run',
    parameters: Type.Object({}),
    execute: async () => {
      await new Promise(resolve => setTimeout(resolve, 700));
      return { content: [{ type: 'text', text: 'tool finished' }], details: undefined };
    },
  });
  pi.registerProvider('plat868-fixture', {
    baseUrl: 'http://localhost', apiKey: 'fixture-only', api: 'plat868-fixture-api',
    models: [{ id: 'fixture', name: 'Fixture', reasoning: false, input: ['text'],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 100000, maxTokens: 1000 }],
    streamSimple: (model, context) => {
      const stream = createAssistantMessageEventStream();
      const latest = context.messages.at(-1);
      const text = latest?.role === 'user' ? latest.content.filter(p => p.type === 'text').map(p => p.text).join('\n') : '';
      const tool = text === 'BUSY_TOOL' ? { name: 'fixture_wait', arguments: {} } :
        text.startsWith('PROCESSED:') ? { name: 'agentworks_processed', arguments: { message_id: text.slice(10) } } :
        text.startsWith('REPLY:') ? { name: 'agentworks_reply', arguments: { message_id: text.slice(6), message: 'EXPLICIT_REPLY', submission_id: 'fixture-reply-one' } } : undefined;
      const message = { role: 'assistant' as const, api: model.api, provider: model.provider, model: model.id,
        content: tool ? [{ type: 'toolCall' as const, id: 'wait-one', name: tool.name, arguments: tool.arguments }] :
          [{ type: 'text' as const, text: 'FIXTURE_RESPONSE' }],
        stopReason: tool ? 'toolUse' as const : 'stop' as const, timestamp: Date.now(),
        usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0,
          cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } } };
      queueMicrotask(() => {
        stream.push({ type: 'start', partial: message });
        stream.push({ type: 'done', reason: message.stopReason, message });
      });
      return stream;
    },
  });
}
