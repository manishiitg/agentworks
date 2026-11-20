// #3357 Team pane: what is waiting for this chat and who is around in its Team.
// It only shows what the AgentWorks app returns for this exact attached session
// (agentworks plugin team-pane); it never answers or sends anything. An agent opens
// to its last messages when the server allows it; "Use in this chat" only fills the
// prompt box, the person presses Enter.
// #3393 Team kit: the person's Team setups (state from the app); "Set this up" only fills the
// prompt box, the agent does the setup with the Team setup tools after the person presses Enter.
// Refreshes at session start, turn end and on Refresh, never on a timer.
const PANE = 'agentworks-team';
let open = false;
let busy = false;
let data = null; // the last JSON line, or { failed: true }
let readAt = 0;
let revision = 0;
// One agent opened in the pane (its row, last messages when shared Live), or null for the list.
let view = null; // { id, name, busy, data, used }

export async function refreshTeam($) {
  if (!open) return;
  const request = ++revision;
  busy = true;
  $.ui.invalidate('ui.render');
  const next = await readPane($, []);
  if (request !== revision) return;
  data = next;
  readAt = Date.now();
  busy = false;
  $.ui.invalidate('ui.render');
}

async function readPane($, extra, timeoutMs) {
  try {
    const result = await $.process.run(['agentworks', 'plugin', 'team-pane', 'claude', ...extra], { timeoutMs: timeoutMs || (extra.length ? 20000 : 6000) });
    const line = result.exitCode === 0 ? result.stdout.trim() : '';
    if (line.startsWith('{') && line.length < 512 * 1024) return JSON.parse(line);
  } catch { /* shown as "couldn't reach the app" */ }
  return { failed: true };
}

async function openAgent($, a) {
  const request = ++revision;
  view = { id: a.id, name: a.name, busy: true, data: null, used: false };
  $.ui.invalidate('ui.render');
  const d = await readPane($, ['--agent', a.id]);
  if (request !== revision || !view || view.id !== a.id) return;
  view = { ...view, busy: false, data: d };
  $.ui.invalidate('ui.render');
}

// Fork to my Mac: AgentWorks copies a teammate's agent to this person's own Mac (the server decides if it may).
async function forkToMac($, d) {
  if (!view || view.fork === 'working') return;
  view = { ...view, fork: 'working', forkError: '' };
  $.ui.invalidate('ui.render');
  const r = await readPane($, ['--fork', d.agent.id], 110000);
  if (!view) return;
  view = { ...view, fork: r.forked ? 'done' : '', forkError: r.forked ? '' : clean(r.error, 240) || 'Couldn’t copy this agent.' };
  $.ui.invalidate('ui.render');
}

// One Team kit setup opened in the pane, or null.
let kitId = null;
let kitFilled = false;

async function fillKit($, k) {
  try {
    const filled = await $.prompt.fill({ text: 'Set up ' + clean(k.title, 160) + ' from my Team kit', mode: 'append' });
    kitFilled = !(filled && filled.isFilled === false);
  } catch { /* the prompt box is not available here */ }
  $.ui.invalidate('ui.render');
}

function backToTeam($) {
  view = null;
  ++revision;
  void refreshTeam($);
}

// Puts a short note about the opened chat in the prompt box; nothing is sent for the person.
async function useInChat($, d) {
  const a = d.agent;
  const who = a.mine ? 'my agent' : a.kind === 'team_agent' ? 'the Team agent' : (a.person ? clean(a.person, 40) + '’s agent' : 'a Team agent');
  const last = (a.messages || []).slice(-3).map(m => (m.role === 'user' ? 'Asked: ' : 'Said: ') + clean(m.text, 240)).join(' / ');
  const text = 'About “' + clean(a.name, 60) + '” (' + who + ', ' + statusLine(a) + ')' + (last ? ': ' + last : '') + ' ';
  try {
    const filled = await $.prompt.fill({ text, mode: 'append' });
    if (view && filled && filled.isFilled !== false) view = { ...view, used: true };
  } catch { /* the prompt box is not available here */ }
  $.ui.invalidate('ui.render');
}

export async function openTeam($) {
  if (!open) {
    const shown = await $.ui.open({ id: PANE, title: 'AgentWorks Team', closeOnEscape: true });
    if (shown && shown.isPlaced === false && !shown.reason) return;
    open = true;
  }
  await refreshTeam($);
}

export async function registerTeamCommand($) {
  try {
    await $.command.register({ name: 'team', description: 'Show what is waiting for you and your AgentWorks Team' });
  } catch { /* another plugin owns /team; the chip above the prompt still opens the pane */ }
}

function ago(at) {
  const t = Date.parse(at || '');
  if (!t) return '';
  const m = Math.max(0, Math.round((Date.now() - t) / 60000));
  if (m < 1) return 'just now';
  if (m < 60) return m + ' min ago';
  const h = Math.round(m / 60);
  return h < 24 ? h + ' h ago' : Math.round(h / 24) + ' d ago';
}

const ASK_STATE = { open: 'New', in_progress: 'In progress', blocked: 'Blocked', needs_you: 'Waiting on you', stalled: 'No reply yet' };
const clean = (s, max = 200) => {
  const text = String(s || '').replace(/[\u0000-\u001f\u007f]+/g, ' ').trim();
  return text.length > max ? text.slice(0, max - 1) + '…' : text;
};
const titled = (ref, title) => (/^#\d+$/.test(ref || '') ? ref + ' ' : '') + clean(title);

export function teamSections(d) {
  if (!d || d.failed) return { notice: ['Couldn’t reach the AgentWorks app.', 'Open the AgentWorks app on this Mac, then press Refresh. Your chat keeps working meanwhile.'] };
  if (!d.attached) return { notice: ['This chat isn’t connected to AgentWorks.', 'Type /agentworks attach to connect it. Then your Team shows here.'] };
  if (!d.crew_name) return { notice: ['This chat isn’t in a Crew’s Team yet.', 'Ask your agent to join your Crew’s Team. Then what’s waiting shows here.'] };
  if (!d.team_on) return { notice: ['The Team for ' + clean(d.crew_name, 80) + ' is off.', 'The Crew’s owner turns it on in AgentWorks.'] };
  if (!d.in_team) return { notice: ['This agent isn’t shared with the Team.', 'Share it from the Team page in AgentWorks to see the Team here.'] };
  const needs = (d.needs || []).map(n => ({
    title: titled(n.ref, n.line),
    meta: 'From ' + (n.mine ? 'your ' : n.person ? clean(n.person, 40) + '’s ' : '') + clean(n.agent, 60) + ' · ' + ago(n.at) + ' · answer in AgentWorks',
  }));
  const asks = (d.asks || []).map(a => ({
    title: titled(a.ref, a.title),
    state: ASK_STATE[a.state] || 'Open',
    meta: (a.asked_by ? 'Asked by ' + clean(a.asked_by, 60) + ' · ' : '') + ago(a.at),
    last: a.last ? clean(a.last, 160) : '',
  }));
  // Grouped by Team member like the Team panel: you first, teammates in order, the Team agent last.
  const groups = [];
  const group = key => {
    let g = groups.find(x => x.key === key);
    if (!g) groups.push(g = { key, who: key === 'mine' ? 'You' : key === 'team' ? 'Team agent' : key.slice(2), agents: [] });
    return g;
  };
  for (const a of d.agents || []) {
    const g = group(a.mine ? 'mine' : a.kind === 'team_agent' ? 'team' : 'p:' + (clean(a.person, 40) || 'Teammate'));
    g.agents.push({ id: String(a.id || ''), name: clean(a.name, 60), online: !!a.online, line: statusLine(a) });
  }
  const rank = g => (g.key === 'mine' ? 0 : g.key === 'team' ? 2 : 1);
  groups.sort((x, y) => rank(x) - rank(y));
  for (const g of groups) g.agents.sort((x, y) => Number(y.online) - Number(x.online));
  // Setups still to do first, Done ones after, each group in the app's order.
  const kit = (d.kit || []).map(k => ({
    id: String(k.id || ''), title: clean(k.title, 160), purpose: clean(k.purpose, 200), state: KIT_STATE[k.state] ? k.state : 'none',
    steps: (k.steps || []).slice(0, 12).map(x => clean(x, 200)), done: Number(k.done) || 0, keys: (k.keys || []).slice(0, 12).map(x => clean(x, 80)),
  })).sort((x, y) => Number(x.state === 'done') - Number(y.state === 'done'));
  const online = groups.reduce((n, g) => n + g.agents.filter(a => a.online).length, 0);
  return { crew: clean(d.crew_name, 80), needs, asks, groups, online, kit };
}

function statusLine(a) {
  if (a.kind === 'team_agent') return a.online ? 'Always on · ' + (a.activity === 'running' ? 'Working now' : 'Idle') : clean(a.offline_line, 80) || 'Offline';
  return a.online ? (a.activity === 'running' ? 'Working now' : 'Idle') : clean(a.offline_line, 80) || 'Offline';
}

const KIT_STATE = { none: 'Not started', progress: 'In progress', done: 'Done', key: 'Needs a key you add in AgentWorks' };

const NOTE = {
  not_live: 'This chat isn’t shared with the Team, so only its status shows here. Its owner can share it from the Team page in AgentWorks.',
  open_in_app: 'This agent runs in AgentWorks. Open it there to see its chat.',
  unavailable: 'Couldn’t read this chat right now. Its Mac may be off; try again later.',
};

async function onTeamCommand($) {
  await openTeam($);
  return {};
}

async function onPaneClose($, e, next) {
  if (e.id === PANE) {
    open = false;
    view = null;
    kitId = null;
  }
  return next(e);
}

async function renderTeam($, e, next) {
  if (e.requestId !== PANE) return next(e);
  const { Box, Text, Button } = $.ui.resolve(e);
  const s = teamSections(data);
  const rows = [];
  const heading = (label, count, color) => Text({ key: 'h-' + label, weight: 'semibold', color, children: [label + (count === undefined ? '' : ' (' + count + ')')] });
  const kitOpen = kitId && s.kit ? s.kit.find(k => k.id === kitId) : null;
  if (kitOpen) return Box({ flexDirection: 'column', paddingX: 1, children: renderKit($, e, kitOpen) });
  if (view) return Box({ flexDirection: 'column', paddingX: 1, children: renderAgent($, e, view) });
  if (busy && !data) rows.push(Text({ key: 'loading', dimColor: true, children: ['Loading your Team…'] }));
  else if (s.notice) {
    rows.push(Text({ key: 'n1', weight: 'semibold', children: [s.notice[0]] }));
    rows.push(Text({ key: 'n2', dimColor: true, children: [s.notice[1]] }));
  } else {
    rows.push(heading('Needs you', s.needs.length, s.needs.length ? 'warning' : undefined));
    if (!s.needs.length) rows.push(Text({ key: 'n0', dimColor: true, children: ['Nothing needs you.'] }));
    s.needs.forEach((n, i) => rows.push(Box({ key: 'need' + i, flexDirection: 'column', children: [
      Text({ wrap: 'truncate-end', children: [n.title] }),
      Text({ dimColor: true, wrap: 'truncate-end', children: [n.meta] }),
    ] })));
    rows.push(Text({ key: 'sp1', children: [' '] }));
    const todo = s.kit.filter(k => k.state !== 'done').length;
    rows.push(heading('Team kit', todo + ' to do'));
    if (!s.kit.length) rows.push(Text({ key: 'k0', dimColor: true, wrap: 'wrap', children: ['Your Team hasn’t prepared any setups yet. The Team owner adds them in AgentWorks › Team › Kit.'] }));
    s.kit.forEach(k => rows.push(Box({ key: 'kit-' + k.id, flexDirection: 'column', children: [
      Button({ key: 'kit-open-' + k.id, label: k.title + ' ›', plain: true, onPress: () => { kitId = k.id; kitFilled = false; $.ui.invalidate('ui.render'); } }),
      ...(k.purpose ? [Text({ dimColor: true, wrap: 'wrap', children: [k.purpose] })] : []),
      Text({ color: k.state === 'done' ? 'success' : k.state === 'key' ? 'warning' : undefined, dimColor: k.state === 'none', children: [KIT_STATE[k.state]] }),
    ] })));
    rows.push(Text({ key: 'spk', children: [' '] }));
    rows.push(heading('Asks for this agent', s.asks.length));
    if (!s.asks.length) rows.push(Text({ key: 'a0', dimColor: true, children: ['No agent has asked this one for anything.'] }));
    s.asks.forEach((a, i) => rows.push(Box({ key: 'ask' + i, flexDirection: 'column', children: [
      Text({ wrap: 'truncate-end', children: [a.title + '  · ' + a.state] }),
      Text({ dimColor: true, wrap: 'truncate-end', children: [a.meta] }),
      ...(a.last ? [Text({ dimColor: true, italic: true, wrap: 'truncate-end', children: ['Last: ' + a.last] })] : []),
    ] })));
    rows.push(Text({ key: 'sp2', children: [' '] }));
    rows.push(heading('Team', s.online + ' online'));
    if (!s.groups.some(g => g.key !== 'mine')) rows.push(Text({ key: 'g0', dimColor: true, wrap: 'wrap', children: ['Only your agents are in this Team so far. Teammates show here when they join.'] }));
    s.groups.forEach(g => {
      const on = g.agents.filter(a => a.online).length;
      rows.push(Box({ key: 'who-' + g.key, flexDirection: 'row', columnGap: 1, children: [
        Text({ weight: 'semibold', children: [g.who] }),
        ...(g.key === 'team' ? [] : [Text({ dimColor: true, children: [on + ' of ' + g.agents.length + ' online'] })]),
      ] }));
      g.agents.forEach((a, i) => rows.push(Box({ key: 'agent-' + g.key + i, flexDirection: 'row', columnGap: 1, children: [
        Text({ color: a.online ? 'success' : undefined, dimColor: !a.online, children: [a.online ? '●' : '○'] }),
        Box({ flexDirection: 'column', children: [
          a.id ? Button({ key: 'open-' + a.id, label: a.name + ' ›', plain: true, onPress: () => openAgent($, a) }) : Text({ children: [a.name] }),
          Text({ dimColor: true, wrap: 'wrap', children: [a.line] }), // the desktop app clips instead of adding an ellipsis
        ] }),
      ] })));
    });
  }
  rows.push(Text({ key: 'sp3', children: [' '] }));
  rows.push(Box({ key: 'foot', flexDirection: 'row', columnGap: 2, children: [
    Button({ key: 'refresh', label: busy ? 'Refreshing…' : 'Refresh', plain: true, onPress: () => refreshTeam($) }),
    Text({ dimColor: true, children: [readAt ? 'Updated ' + ago(new Date(readAt).toISOString()) : ''] }),
  ] }));
  return Box({ flexDirection: 'column', paddingX: 1, children: rows });
}

function renderKit($, e, k) {
  const { Box, Text, Button } = $.ui.resolve(e);
  const rows = [Button({ key: 'back', label: '‹ Team kit', plain: true, onPress: () => { kitId = null; kitFilled = false; $.ui.invalidate('ui.render'); } })];
  rows.push(Text({ key: 'name', weight: 'semibold', wrap: 'wrap', children: [k.title] }));
  if (k.purpose) rows.push(Text({ key: 'purpose', dimColor: true, wrap: 'wrap', children: [k.purpose] }));
  rows.push(Text({ key: 'state', color: k.state === 'done' ? 'success' : k.state === 'key' ? 'warning' : undefined, children: [KIT_STATE[k.state]] }));
  rows.push(Text({ key: 'sp', children: [' '] }));
  rows.push(Text({ key: 'steps', weight: 'semibold', children: ['Steps' + (k.steps.length ? ' (' + Math.min(k.done, k.steps.length) + ' of ' + k.steps.length + ' checked)' : '')] }));
  k.steps.forEach((x, i) => rows.push(Text({ key: 'step' + i, wrap: 'wrap', children: [(k.state === 'done' ? '✓ ' : '○ ') + x] })));
  if (k.keys.length) {
    rows.push(Text({ key: 'sp2', children: [' '] }));
    rows.push(Text({ key: 'keys', color: 'warning', wrap: 'wrap', children: ['Needs a key: ' + k.keys.join(', ')] }));
    rows.push(Text({ key: 'keys2', dimColor: true, wrap: 'wrap', children: ['Add it in AgentWorks › Team › Kit. The value stays there and never shows in this chat.'] }));
  }
  rows.push(Text({ key: 'sp3', children: [' '] }));
  rows.push(Button({ key: 'set', label: k.state === 'done' ? 'Check it again' : 'Set this up', onPress: () => fillKit($, k) }));
  rows.push(Text({ key: 'set2', dimColor: true, wrap: 'wrap', children: [kitFilled ? 'Added to your prompt box. Press Enter to send it.' : 'Puts a line in your prompt box. You press Enter.'] }));
  return rows;
}

function renderAgent($, e, v) {
  const { Box, Text, Button } = $.ui.resolve(e);
  const rows = [Button({ key: 'back', label: '‹ Team', plain: true, onPress: () => backToTeam($) })];
  if (v.busy) {
    rows.push(Text({ key: 'opening', dimColor: true, children: ['Opening ' + v.name + '…'] }));
    return rows;
  }
  const d = v.data;
  if (!d || d.failed || !d.agent) {
    rows.push(Text({ key: 'err', weight: 'semibold', children: ['Couldn’t open ' + v.name + '.'] }));
    rows.push(Text({ key: 'err2', dimColor: true, wrap: 'wrap', children: ['It may have left your Team view. Go back and press Refresh.'] }));
    return rows;
  }
  const a = d.agent;
  rows.push(Text({ key: 'name', weight: 'semibold', children: [clean(a.name, 60)] }));
  rows.push(Text({ key: 'meta', dimColor: true, wrap: 'wrap', children: [(a.mine ? 'Your agent' : a.kind === 'team_agent' ? 'Team agent' : (clean(a.person, 40) || 'Teammate') + '’s agent') + ' · ' + statusLine(a)] }));
  rows.push(Text({ key: 'sp', children: [' '] }));
  (a.messages || []).forEach((m, i) => rows.push(Box({ key: 'm' + i, flexDirection: 'column', children: [
    Text({ dimColor: true, children: [m.role === 'user' ? (a.mine ? 'You' : 'Asked') : clean(a.name, 40)] }),
    Text({ wrap: 'wrap', children: [clean(m.text, 600)] }),
  ] })));
  if ((a.messages || []).length) rows.push(Text({ key: 'count', dimColor: true, children: ['Last ' + a.messages.length + ' messages'] }));
  else rows.push(Text({ key: 'note', dimColor: true, wrap: 'wrap', children: [NOTE[a.note] || 'No messages to show yet.'] }));
  rows.push(Text({ key: 'sp2', children: [' '] }));
  rows.push(Button({ key: 'use', label: 'Use in this chat', onPress: () => useInChat($, d) }));
  rows.push(Text({ key: 'use2', dimColor: true, wrap: 'wrap', children: [v.used ? 'Added to your prompt box. Edit it or press Enter.' : 'Puts a short note about this chat in your prompt box. You press Enter.'] }));
  if (!a.mine && a.kind !== 'team_agent') {
    rows.push(Text({ key: 'sp3', children: [' '] }));
    rows.push(Button({ key: 'fork', label: v.fork === 'working' ? 'Copying to your Mac…' : 'Fork to my Mac', onPress: () => forkToMac($, d) }));
    if (v.fork === 'done') rows.push(Text({ key: 'fork2', color: 'success', wrap: 'wrap', children: ['Copied. It opens in AgentWorks on your Mac.'] }));
    else if (v.forkError) rows.push(Text({ key: 'fork2', dimColor: true, wrap: 'wrap', children: [v.forkError] }));
    else rows.push(Text({ key: 'fork2', dimColor: true, wrap: 'wrap', children: ['Copies this agent and its chat into your own AgentWorks on your Mac.'] }));
  }
  return rows;
}

export function registerTeam(on) {
  on('command.run', { command: 'team' }, onTeamCommand);
  on('ui.close', onPaneClose);
  on('ui.render', { component: 'Pane' }, renderTeam);
}
