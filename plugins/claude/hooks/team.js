// #3357 Team pane: what is waiting for this chat and who is around in its Team.
// It only shows what the AgentWorks app returns for this exact attached session
// (agentworks plugin team-pane); it never answers, sends or decides anything.
// Refreshes at session start, turn end and on Refresh, never on a timer.
const PANE = 'agentworks-team';
let open = false;
let busy = false;
let data = null; // the last JSON line, or { failed: true }
let readAt = 0;
let revision = 0;

export async function refreshTeam($) {
  if (!open) return;
  const request = ++revision;
  busy = true;
  $.ui.invalidate('ui.render');
  let next = { failed: true };
  try {
    const result = await $.process.run(['agentworks', 'plugin', 'team-pane', 'claude'], { timeoutMs: 6000 });
    const line = result.exitCode === 0 ? result.stdout.trim() : '';
    if (line.startsWith('{') && line.length < 512 * 1024) next = JSON.parse(line);
  } catch { /* shown as "couldn't reach the app" */ }
  if (request !== revision) return;
  data = next;
  readAt = Date.now();
  busy = false;
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
  const agents = (d.agents || []).map(a => ({
    name: clean(a.name, 60),
    online: !!a.online,
    line: (a.kind === 'team_agent' ? 'Team agent · ' : a.person ? clean(a.person, 40) + '’s agent · ' : '')
      + (a.online ? (a.activity === 'running' ? 'Working now' : 'Idle') : clean(a.offline_line, 80) || 'Offline'),
  })).sort((x, y) => Number(y.online) - Number(x.online));
  return { crew: clean(d.crew_name, 80), needs, asks, agents };
}

async function onTeamCommand($) {
  await openTeam($);
  return {};
}

async function onPaneClose($, e, next) {
  if (e.id === PANE) open = false;
  return next(e);
}

async function renderTeam($, e, next) {
  if (e.requestId !== PANE) return next(e);
  const { Box, Text, Button } = $.ui.resolve(e);
  const s = teamSections(data);
  const rows = [];
  const heading = (label, count, color) => Text({ key: 'h-' + label, weight: 'semibold', color, children: [label + (count === undefined ? '' : ' (' + count + ')')] });
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
    rows.push(heading('Asks for this agent', s.asks.length));
    if (!s.asks.length) rows.push(Text({ key: 'a0', dimColor: true, children: ['No agent has asked this one for anything.'] }));
    s.asks.forEach((a, i) => rows.push(Box({ key: 'ask' + i, flexDirection: 'column', children: [
      Text({ wrap: 'truncate-end', children: [a.title + '  · ' + a.state] }),
      Text({ dimColor: true, wrap: 'truncate-end', children: [a.meta] }),
      ...(a.last ? [Text({ dimColor: true, italic: true, wrap: 'truncate-end', children: ['Last: ' + a.last] })] : []),
    ] })));
    rows.push(Text({ key: 'sp2', children: [' '] }));
    rows.push(heading('Team agents', s.agents.filter(a => a.online).length + ' online'));
    if (!s.agents.length) rows.push(Text({ key: 'g0', dimColor: true, children: ['No other agents in this Team yet.'] }));
    s.agents.forEach((a, i) => rows.push(Box({ key: 'agent' + i, flexDirection: 'row', columnGap: 1, children: [
      Text({ color: a.online ? 'success' : undefined, dimColor: !a.online, children: [a.online ? '●' : '○'] }),
      Box({ flexDirection: 'column', children: [
        Text({ dimColor: !a.online, wrap: 'truncate-end', children: [a.name] }),
        Text({ dimColor: true, wrap: 'wrap', children: [a.line] }), // the desktop app clips instead of adding an ellipsis
      ] }),
    ] })));
  }
  rows.push(Text({ key: 'sp3', children: [' '] }));
  rows.push(Box({ key: 'foot', flexDirection: 'row', columnGap: 2, children: [
    Button({ key: 'refresh', label: busy ? 'Refreshing…' : 'Refresh', plain: true, onPress: () => refreshTeam($) }),
    Text({ dimColor: true, children: [readAt ? 'Updated ' + ago(new Date(readAt).toISOString()) : ''] }),
  ] }));
  return Box({ flexDirection: 'column', paddingX: 1, children: rows });
}

export function registerTeam(on) {
  on('command.run', { command: 'team' }, onTeamCommand);
  on('ui.close', onPaneClose);
  on('ui.render', { component: 'Pane' }, renderTeam);
}
