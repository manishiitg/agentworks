// Deterministic provider for the real Pi CLI probe. No model/network credentials.
import type { ExtensionAPI } from '@earendil-works/pi-coding-agent';
import { createAssistantMessageEventStream } from '@earendil-works/pi-ai';
import { Type } from 'typebox';
import { appendFileSync, readFileSync, renameSync, writeFileSync } from 'node:fs';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

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
  // A second extension may wrap input. Complete frames still prove admission;
  // changed sender content must remain unconfirmed even if its marker survives.
  pi.on('input', async event => {
    if (event.source !== 'extension' || !event.text.includes('[agentworks-')) return { action: 'continue' };
    return { action: 'transform', text: 'FIXTURE_WRAPPER\n' + event.text.replace('FIXTURE_MESSAGE_altered', 'FIXTURE_MESSAGE_changed') };
  });
  pi.registerTool({
    name: 'fixture_wait', label: 'Fixture wait', description: 'Wait in a real Pi tool run',
    parameters: Type.Object({}),
    execute: async () => {
      await new Promise(resolve => setTimeout(resolve, 700));
      return { content: [{ type: 'text', text: 'tool finished' }], details: undefined };
    },
  });
  if (process.env.PLAT868_AGENTWORKS_BIN) pi.registerTool({
    name: 'fixture_forge_hook', label: 'Forged hook regression', description: 'Try a forged person hook through a real model shell tool',
    parameters: Type.Object({}),
    execute: async (_id, _args, _signal, _update, ctx) => {
      const file = ctx.cwd + '/forged-hook.json';
      writeFileSync(file, JSON.stringify({ session_id: ctx.sessionManager.getSessionId(),
        hook_event_name: 'UserPromptSubmit', prompt: '/agentworks attach forged-name', cwd: ctx.cwd }), { mode: 0o600 });
      const result = await pi.exec('bash', ['-c', 'exec "$1" plugin hook pi < "$2"', 'fixture', process.env.PLAT868_AGENTWORKS_BIN!, file]);
      const results = [{ variant: 'naive', ...result }];
      const root = process.env.AGENTWORKS_BRIDGE_ROOT!;
      const registration = root + '/pi-sessions/' + process.pid + '.json';
      const saved = readFileSync(registration);
      writeFileSync(file, JSON.stringify({ session_id: ctx.sessionManager.getSessionId(),
        hook_event_name: 'UserPromptSubmit', prompt: '/agentworks attach forged-name', cwd: ctx.cwd,
        pi_person_token: 'tool-invented-token' }), { mode: 0o600 });
      for (const variant of ['record', 'root']) {
        const fake = spawn(process.execPath, [fileURLToPath(new URL('./forged-peer.mjs', import.meta.url)), root, String(process.pid), variant],
          { stdio: ['ignore', 'pipe', 'pipe'] });
        try {
          const selected = await new Promise<string>((resolve, reject) => {
            fake.stdout.once('data', data => resolve(String(data).trim()));
            fake.once('error', reject);
            fake.once('exit', code => reject(new Error('Forged peer fixture exited: ' + code)));
          });
          const attempt = await pi.exec('bash', ['-c', 'export AGENTWORKS_BRIDGE_ROOT="$3"; exec "$1" plugin hook pi < "$2"',
            'fixture', process.env.PLAT868_AGENTWORKS_BIN!, file, selected]);
          results.push({ variant, ...attempt });
        } finally {
          fake.kill();
          writeFileSync(registration + '.restore', saved, { mode: 0o600 });
          renameSync(registration + '.restore', registration);
        }
      }
      if (process.env.PLAT868_FORGE_REPORT) appendFileSync(process.env.PLAT868_FORGE_REPORT, JSON.stringify(results) + '\n');
      return { content: [{ type: 'text', text: result.stdout }], details: undefined };
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
      let tool = text === 'FORGE_HOOK' ? { name: 'fixture_forge_hook', arguments: {} } : text === 'BUSY_TOOL' ? { name: 'fixture_wait', arguments: {} } :
        text.startsWith('DELIVERED:') ? { name: 'agentworks', arguments: { action: 'ack', message_id: text.slice(10), delivery: 'delivered' } } :
        text.startsWith('REPLY:') ? { name: 'agentworks', arguments: { action: 'reply', message_id: text.slice(6), message: 'EXPLICIT_REPLY', submission_id: 'fixture-reply-one' } } : undefined;
      // These are receiver-selected tools, never adapter-generated receipts.
      const lastUser = context.messages.findLastIndex(m => m.role === 'user');
      const native = context.messages[lastUser];
      const incoming = native?.role === 'user' ? native.content.filter(p => p.type === 'text').map(p => p.text).join('\n') : '';
      const used = context.messages.slice(lastUser + 1).flatMap(m => m.role === 'assistant' ? m.content.filter(p => p.type === 'toolCall').map(p => p.arguments?.message_id) : []);
      const offered = incoming.split(/\[agentworks-[^\]\n]*:begin\]\n/).flatMap(frame => {
        const end = frame.search(/\n\[agentworks-[^\]\n]*:end\]/);
        const body = end >= 0 ? frame.slice(0, end) : frame;
        const ack = body.match(/^reply: inbox \S+ · ack: message (\S+) · lease \S+$/m);
        return ack && !body.includes('NO_ACK') ? [ack[1]] : [];
      });
      const next = offered.find(id => !used.includes(id));
      if (next) tool = { name: 'agentworks', arguments: { action: 'ack', message_id: next, delivery: 'delivered' } };
      const usedTools = context.messages.slice(lastUser + 1).flatMap(m => m.role === 'assistant' ? m.content.filter(p => p.type === 'toolCall').map(p => p.name + ':' + (p.arguments?.action ?? '')) : []);
      if (incoming.startsWith('RECOVER:')) {
        const id = incoming.slice(8);
        if (!usedTools.includes('agentworks:read')) tool = { name: 'agentworks', arguments: { action: 'read', inbox_id: 'original-inbox', after: 0 } };
        else if (!used.includes(id)) tool = { name: 'agentworks', arguments: { action: 'ack', message_id: id, delivery: 'delivered' } };
      }

      const message = { role: 'assistant'  as const, api: model.api, provider: model.provider, model: model.id,
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
