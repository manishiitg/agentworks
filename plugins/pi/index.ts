import { AgentSession, type ExtensionAPI } from '@earendil-works/pi-coding-agent';
import { Type } from 'typebox';
import { LocalBridge } from './bridge.mjs';
import { Delivery } from './delivery.mjs';
import { Host, personCommand } from './host.mjs';

const help = 'AgentWorks: /agentworks attach [NAME] [--on LOGIN] connects or reconnects this chat (--on moves it to another saved login); /agentworks detach stops delivery; /agentworks agents shows available agents; /agentworks share makes this chat\'s agent visible to everyone on the server and /agentworks unshare makes it private again. Type the command yourself.';

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
      if (action === 'share') {
        shapeOk = /^[a-z0-9][a-z0-9._-]{0,63}$/.test(rest[0] ?? '') && (rest.length === 1 || (rest[1] === '--purpose' && rest.length >= 3));
      } else if (action === 'unshare') {
        shapeOk = rest.length === 0 || (rest.length === 1 && /^[a-z0-9][a-z0-9._-]{0,63}$/.test(rest[0]));
      } else if (action === 'group') {
        shapeOk = rest.length === 1 && /^[a-z0-9][a-z0-9._-]{0,63}$/.test(rest[0]);
      }
      if (!['attach', 'detach', 'agents', 'help', 'share', 'unshare', 'groups', 'group'].includes(action) || !shapeOk) {
        ctx.ui.notify(help, 'info'); return { action: 'handled' };
      }
      if (action === 'help') { ctx.ui.notify(help, 'info'); return { action: 'handled' }; }
      if (['share', 'unshare', 'groups', 'group'].includes(action)) {
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
  register('agent_message_ack', 'Explicitly acknowledge an intact message in this exact attached session: admitted on receipt, processed after handling. Native display alone never acknowledges it.',
    Type.Object({ message_id: Type.String(), delivery: Type.Union([Type.Literal('admitted'), Type.Literal('processed')]) }),
    args => delivery.action('ack', args.message_id, { delivery: args.delivery }));
  register('agent_message_reply', 'Explicit optional reply in the original inbox using this attached identity. Contact requires person authorization. Reuse submission_id after an uncertain retry.',
    Type.Object({ message_id: Type.String(), message: Type.String(), submission_id: Type.String() }),
    args => delivery.action('reply', args.message_id, { message: args.message, submission_id: args.submission_id }));
  register('agent_message_peers', 'Discover authorized named agents for this attached identity.', Type.Object({}),
    () => delivery.control('peers'));
  register('agent_message_set_purpose', 'Set your own one-line purpose (up to 512 characters); at most once per minute. Does not attach or grant permission.',
    Type.Object({ purpose: Type.String({ maxLength: 512 }) }), args => delivery.control('purpose', args));
  register('agent_message_send', 'Explicitly send from this attached identity to a named agent the person authorized contacting. Reuse submission_id after an uncertain retry.',
    Type.Object({ target: Type.String(), message: Type.String(), submission_id: Type.String() }),
    args => delivery.control('send', args));
  register('agent_message_read', 'Explicit bounded mailbox recovery for this attached identity. Never start a watch or polling loop.',
    Type.Object({ inbox_id: Type.Optional(Type.String()), after: Type.Optional(Type.Integer({ minimum: 0 })) }),
    args => delivery.control('read', args));
}
