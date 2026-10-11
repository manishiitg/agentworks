import type { ExtensionAPI } from '@earendil-works/pi-coding-agent';
import { Type } from 'typebox';
import { randomUUID } from 'node:crypto';
import { LocalBridge } from './bridge.mjs';
import { Delivery } from './delivery.mjs';

export default function (pi: ExtensionAPI) {
  const bridge = new LocalBridge();
  const delivery = new Delivery(pi, bridge);

  pi.on('session_start', async (_event, ctx) => { await delivery.start(ctx); });
  pi.on('session_shutdown', async (event, ctx) => {
    const attached = delivery.attachment;
    const session = delivery.session;
    delivery.stop();
    // Reload preserves the exact lease for the replacement extension runtime.
    // Quit/new/resume/fork fences the old receiver rather than following it.
    if (attached && event.reason !== 'reload') {
      try { await bridge.call({ action: 'detach', session_id: session,
        attachment_id: attached.attachment_id }); }
      catch { ctx.ui.notify('AgentWorks could not detach. Revoke the attachment from Connect.', 'warning'); }
    }
  });
  pi.on('message_start', async (event) => {
    try { await delivery.observe(event.message); }
    catch { /* Native admission stays in the session journal; the listener retries its receipt. */ }
  });
  pi.on('agent_end', async () => {
    // Pi drains followUp itself at the end of a run, including tool execution.
    // A turn ending is not a processed acknowledgement.
    try { await delivery.flushReceipts(delivery.generation); } catch { /* listener retries */ }
  });

  pi.registerCommand('agentworks', {
    description: 'Person-controlled AgentWorks attach, detach, status, reconnect',
    handler: async (args, ctx) => {
      const [action, name, ...rest] = args.trim().split(/\s+/);
      try {
        const session = ctx.sessionManager.getSessionId();
        if (action === 'status') {
          if (!await bridge.available()) { ctx.ui.notify('AgentWorks: local bridge unavailable.', 'info'); return; }
          const result = await bridge.call({ action: 'status', session_id: session });
          ctx.ui.notify(result.attached ? `AgentWorks: attached to ${result.attachment.agent_name}; session ${session}.` :
            `AgentWorks: session ${session} is unattached.`, 'info');
          return;
        }
        if (action === 'reconnect') { await delivery.start(ctx); return; }
        // An actual TUI confirmation is required even if another extension calls
        // the slash command. No attach/detach tool is exposed to the model.
        if (ctx.mode !== 'tui' || !ctx.hasUI) throw new Error('Attach and detach require the person in the Pi TUI');
        if (action === 'detach') {
          if (!await ctx.ui.confirm('Detach AgentWorks', 'Stop delivery to this exact Pi conversation?')) return;
          delivery.stop();
          await bridge.call({ action: 'detach', session_id: session });
          ctx.ui.notify('AgentWorks detached.', 'info');
          return;
        }
        if (action !== 'attach' || !name || rest.length) {
          ctx.ui.notify('Usage: /agentworks attach <name> | detach | status | reconnect', 'info');
          return;
        }
        const purpose = (await ctx.ui.input('Purpose of this AgentWorks agent'))?.trim();
        if (!purpose) return;
        const senders = await ctx.ui.input('Approved registered senders (comma separated; blank allows none)');
        if (senders === undefined) return;
        const allowed = [...new Set(senders.split(',').map(value => value.trim()).filter(Boolean))];
        const config = (await ctx.ui.input('Absolute path to your AgentWorks CLI connection config'))?.trim();
        if (!config || !config.startsWith('/')) throw new Error('An absolute CLI config path is required');
        if (!await ctx.ui.confirm('Attach AgentWorks',
          `Name: ${name}\nPurpose: ${purpose}\nExact Pi session: ${session}\nSenders: ${allowed.join(', ') || '(none)'}\nThis explicitly shares incoming messages with this conversation.`)) return;
        delivery.stop();
        const result = await bridge.call({ action: 'attach', session_id: session,
          agent_name: name, purpose, allowed_senders: allowed, config_path: config });
        if (!result.attached || result.attachment?.provider !== 'pi' || result.attachment?.native_session_id !== session)
          throw new Error('AgentWorks did not attach this session');
        await delivery.start(ctx);
        ctx.ui.notify('AgentWorks attached. Native messages use Pi’s follow-up queue.', 'info');
      } catch (error) {
        ctx.ui.notify(error instanceof Error ? error.message : 'AgentWorks request failed', 'error');
      }
    },
  });

  pi.registerTool({
    name: 'agentworks_processed', label: 'AgentWorks processed',
    description: 'Explicitly acknowledge a received AgentWorks message after handling it. Does not reply or attach.',
    parameters: Type.Object({ message_id: Type.String() }),
    execute: async (_id, args) => {
      const result = await delivery.action('ack', args.message_id, { delivery: 'processed' });
      return { content: [{ type: 'text', text: JSON.stringify(result ?? {}) }], details: undefined };
    },
  });
  pi.registerTool({
    name: 'agentworks_reply', label: 'AgentWorks reply',
    description: 'Explicit reply to a received message in its original inbox, using this attached identity. ' +
      'Only reply when the person or authorized task requests it. Reuse submission_id after an uncertain retry.',
    parameters: Type.Object({ message_id: Type.String(), message: Type.String(),
      submission_id: Type.Optional(Type.String()) }),
    execute: async (_id, args) => {
      // Require caller-selected retry identity; inventing it after a failed call
      // would make an uncertain retry a second message.
      const submission = args.submission_id || randomUUID();
      try {
        const result = await delivery.action('reply', args.message_id,
          { message: args.message, submission_id: submission });
        return { content: [{ type: 'text', text: JSON.stringify({ submission_id: submission, result }) }], details: undefined };
      } catch {
        throw new Error(`AgentWorks reply unconfirmed. Retry with submission_id ${submission}.`);
      }
    },
  });
}
