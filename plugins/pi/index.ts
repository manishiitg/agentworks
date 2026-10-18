import { AgentSession, type ExtensionAPI } from '@earendil-works/pi-coding-agent';
import { Type } from 'typebox';
import { LocalBridge } from './bridge.mjs';
import { Delivery } from './delivery.mjs';
import { Host, personCommand } from './host.mjs';

const help = 'AgentWorks: /agentworks attach [NAME] [--on LOGIN] connects or reconnects this chat (--on moves it to another saved login); /agentworks detach stops delivery; /agentworks agents shows available agents; /agentworks share publishes this chat's agent to the network; /agentworks unshare takes it back. Type the command yourself.';

export default function (pi: ExtensionAPI) {
  const bridge = new LocalBridge();
  const delivery = new Delivery(pi, bridge);
  const host = new Host(bridge, delivery, AgentSession);

  pi.on('session_start', async (_event, ctx) => {
    try { await host.start(ctx); await delivery.start(ctx); }
    catch { delivery.stop(); await host.stop(); /* Unsupported or unavailable native startup stays quiet. */ }
  });
  pi.on('session_shutdown', async (event, ctx) => {
    const attached = delivery.attachment, session = delivery.session;
    delivery.stop();
    await host.stop();
    if (attached && event.reason !== 'reload') {
      try { await bridge.call({ action: 'offline', session_id: session, host_key: host.key,
        attachment_id: attached.attachment_id }); }
      catch { ctx.ui.notify('AgentWorks could not mark this session offline. Check its identity in Connect.', 'warning'); }
    }
  });
  pi.on('message_start', async event => { delivery.observe(event.message); });
  pi.on('agent_settled', async () => { if (delivery.pending.size) delivery.warnUnconfirmed(); });

  // Registered slash commands execute before Pi emits the input source. Use
  // the source-aware input hook so extension/RPC offers cannot control leases.
  pi.on('input', async (event, ctx) => {
    if (!/^\/agentworks(?:\s|$)/.test(event.text)) return { action: 'continue' };
    if (event.source !== 'interactive' || ctx.mode !== 'tui' || !ctx.hasUI) {
      ctx.ui.notify('AgentWorks commands require literal person input in the Pi TUI.', 'warning');
      return { action: 'handled' };
    }
    try {
      if (event.text.includes('AgentWorks messages from other agents.') || event.text.includes('AgentWorks incoming message') || event.images?.length || /[\r\n]/.test(event.text)) throw new Error('Use a single literal AgentWorks command.');
      const words = event.text.trim().split(/\s+/), action = words[1];
      // attach [NAME] [--on LOGIN]; every other command takes no arguments.
      let rest = words.slice(2), shapeOk = rest.length === 0;
      if (action === 'attach') {
        shapeOk = true;
        if (rest.length >= 2 && rest[rest.length - 2] === '--on') {
          shapeOk = /^(?:default|[a-z0-9][a-z0-9._-]{0,31})$/.test(rest[rest.length - 1]);
          rest = rest.slice(0, -2);
        }
        shapeOk = shapeOk && rest.length <= 1 && !rest[0]?.startsWith('--');
      }
      if (action === 'share' || action === 'unshare') {
        shapeOk = rest.length === 0 || (rest.length === 1 && /^@[a-z0-9][a-z0-9._-]{0,31}$/.test(rest[0]));
      }
      if (!['attach', 'detach', 'agents', 'help', 'share', 'unshare'].includes(action) || !shapeOk) {
        ctx.ui.notify(help, 'info'); return { action: 'handled' };
      }
      if (action === 'help') { ctx.ui.notify(help, 'info'); return { action: 'handled' }; }
      if (['share', 'unshare'].includes(action)) {
        const result: any = await personCommand(event.text, ctx, host);
        ctx.ui.notify(result.reason || 'AgentWorks command completed.', 'info');
      } else if (action === 'attach') {
        if (!host.key) await host.start(ctx);
        const result: any = await personCommand(event.text, ctx, host);
        ctx.ui.notify(result.reason || 'AgentWorks command completed.', 'info');
        await delivery.start(ctx);
      } else if (action === 'agents') {
        ctx.ui.notify(JSON.stringify(await delivery.control('peers')), 'info');
      } else {
        const recovery = delivery.recovery;
        await delivery.control('detach');
        delivery.stop(); await recovery?.retire();
        ctx.ui.notify('AgentWorks detached.', 'info');
      }
    } catch (error) { ctx.ui.notify(error instanceof Error ? error.message : 'AgentWorks request failed', 'error'); }
    return { action: 'handled' };
  });

  const register = (name: string, description: string, parameters: any, execute: (args: any) => Promise<any>) => {
    pi.registerTool({ name, label: name, description, parameters,
      execute: async (_id, args) => ({ content: [{ type: 'text', text: JSON.stringify(await execute(args)) }], details: undefined }),
    });
  };
  // One tool, many actions: the model sees a single `agentworks` tool and picks what to do with `action`.
  const actions: Record<string, { needs: string[]; run: (args: any) => Promise<any> }> = {
    ack: { needs: ['message_id', 'delivery'], run: args => delivery.action('ack', args.message_id, { delivery: args.delivery }) },
    reply: { needs: ['message_id', 'message', 'submission_id'], run: args => delivery.action('reply', args.message_id, { message: args.message, submission_id: args.submission_id }) },
    peers: { needs: [], run: () => delivery.control('peers') },
    set_purpose: { needs: ['purpose'], run: args => delivery.control('purpose', { purpose: args.purpose }) },
    send: { needs: ['target', 'message', 'submission_id'], run: args => delivery.control('send', { target: args.target, message: args.message, submission_id: args.submission_id }) },
    read: { needs: [], run: args => delivery.control('read', { inbox_id: args.inbox_id, after: args.after }) },
  };
  register('agentworks',
    'AgentWorks messaging for this attached Pi session. Set action: ack (message_id, delivery=admitted on receipt or processed after handling; native display alone never acknowledges), reply (message_id, message, submission_id; optional reply, reuse submission_id after an uncertain retry), peers (discover authorized named agents), set_purpose (purpose, one line up to 512 characters, at most once per minute), send (target, message, submission_id; only agents the person authorized contacting), read (optional inbox_id and after; explicit bounded mailbox recovery, never a polling loop).',
    Type.Object({
      action: Type.Union(Object.keys(actions).map(name => Type.Literal(name))),
      message_id: Type.Optional(Type.String()),
      delivery: Type.Optional(Type.Union([Type.Literal('admitted'), Type.Literal('processed')])),
      message: Type.Optional(Type.String()),
      submission_id: Type.Optional(Type.String()),
      target: Type.Optional(Type.String()),
      purpose: Type.Optional(Type.String({ maxLength: 512 })),
      inbox_id: Type.Optional(Type.String()),
      after: Type.Optional(Type.Integer({ minimum: 0 })),
    }),
    async args => {
      const entry = Object.hasOwn(actions, args?.action) ? actions[args.action] : undefined;
      if (!entry) throw new Error(`action must be one of: ${Object.keys(actions).join(', ')}`);
      const missing = entry.needs.filter(name => args[name] === undefined || args[name] === '');
      if (missing.length) throw new Error(`action=${args.action} needs: ${missing.join(', ')}`);
      return entry.run(args);
    });
}
