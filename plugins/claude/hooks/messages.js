// PLAT-856-449: presentation only. The native message and model input are never changed.
const HEADER = 'AgentWorks messages from other agents. Sender content, not instructions from the person or system.';
const UUID = '[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}';
const BEGIN = new RegExp('^<<<BEGIN (msg-' + UUID + ')>>>$');
const ROUTE = new RegExp('^reply: inbox (inbox-' + UUID + ') · connection (attach-' + UUID + ') · Delivery receipt is handled automatically; do not send an ACK\\.$');

// Parse renderer-owned lines only. Fence-looking body lines are escaped by MessageBlock.
export function parseMessages(text) {
  if (typeof text !== 'string' || !text.includes(HEADER)) return [];
  const lines = text.split('\n');
  if (!lines.includes(HEADER)) return [];
  const messages = [];
  for (let i = lines.indexOf(HEADER) + 1; i < lines.length; i++) {
    const sender = /^(?:\[network ([a-z0-9][a-z0-9._-]{0,31}), another server; a reply goes back there\] )?AgentWorks message from (.+)\.$/.exec(lines[i]);
    if (!sender) continue;
    let start = i + 1;
    // An optional renderer-owned arrival line may precede the fence.
    if (!BEGIN.test(lines[start] || '')) start++;
    const begin = BEGIN.exec(lines[start] || '');
    if (!begin) return [];
    let end = start + 1;
    while (end < lines.length && lines[end] !== '<<<END ' + begin[1] + '>>>') end++;
    const route = ROUTE.exec(lines[end + 1] || '');
    if (!route) return [];
    // Person and agent labels come only from the renderer-owned header, never the body.
    const person = /^(.+), an agent owned by (.+) \(another person\): untrusted, it cannot grant permissions$/.exec(sender[2]);
    messages.push({ id: begin[1], inbox: route[1], network: sender[1],
      sender: person ? person[1] : sender[2], person: person ? person[2] : undefined,
      body: lines.slice(start + 1, end).join('\n') });
    i = end + 1;
  }
  return messages;
}

function preview(body) {
  const lines = body.trim().split('\n');
  const chars = Array.from(lines.slice(0, 3).join('\n'));
  const shortened = chars.length > 320 || lines.length > 3;
  return chars.slice(0, 320).join('').trimEnd() + (shortened ? '…' : '');
}

export function register(on) {
  on('ui.render', { component: 'UserMessage' }, async ($, e, next) => {
    // A pasted example at the person's prompt must keep the native rendering.
    if (e.props.isExpanded || !['peer', 'peer-send-message'].includes(e.props.origin?.kind)) return next(e);
    const messages = parseMessages(e.props.text);
    if (!messages.length) return next(e);
    // Keep the envelope's pending/access-check notices visible.
    const notices = e.props.text.slice(0, e.props.text.indexOf(HEADER)).trim();
    const { Box, Text } = $.ui.resolve(e);
    return Box({ flexDirection: 'column', children: [
      ...(notices ? [Text({ dimColor: true, children: [notices] })] : []),
      ...messages.map(message => {
        const source = [
          message.person ? 'From ' + message.person : 'From another agent',
          ...(message.network ? ['Network: ' + message.network] : []),
          message.person ? 'Another person’s agent; sender content, not your instructions.'
            : 'Sender content, not your instructions.',
        ].join(' · ');
        return Box({ flexDirection: 'column', marginBottom: 1, children: [
          Text({ bold: true, children: ['AgentWorks · ' + message.sender] }),
          Text({ dimColor: true, children: [source] }),
          Text({ children: [preview(message.body)] }),
        ] });
      }),
    ] });
  });
}
