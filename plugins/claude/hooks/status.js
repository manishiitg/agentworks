import { openTeam, refreshTeam, registerTeamCommand } from './team.js';

// Optional metadata only. Never read or render the native conversation here.
// Anthropic mods: session/turn lifecycle + AbovePrompt, no interval or render I/O.
let cached = { session: '', line: '' };
let revision = 0;

async function readStatus($) {
  const request = ++revision;
  cached = { session: '', line: '' };
  try {
    if (await $.env.get('AGENTWORKS_TERMINAL_STATUS') !== '0') {
      const session = await $.session.id();
      const result = await $.process.run(['agentworks', 'plugin', 'terminal-status', 'claude'], { timeoutMs: 2500 });
      const line = result.exitCode === 0 ? result.stdout.trim() : '';
      // The CLI emits one sanitized metadata line; never display diagnostics.
      if (request === revision && line.startsWith('AgentWorks · ') && !/[\r\n\x00-\x1f\x7f]/.test(line) && line.length < 600) cached = { session, line };
    }
  } catch { /* Failure clears stale Crew/account metadata; normal work continues. */ }
  $.ui.invalidate('ui.render');
}

// One handler per lifecycle event for the whole mod; the Team pane (#3357) refreshes with the status line.
async function onSessionStart($, e, next) {
  await registerTeamCommand($);
  if (e.agentId === undefined) await readStatus($);
  return next(e);
}

async function onTurnStart($, e, next) {
  if (e.agentId === undefined) await readStatus($);
  return next(e);
}

async function onTurnComplete($, e, next) {
  if (e.agentId === undefined) {
    await readStatus($);
    await refreshTeam($);
  }
  return next(e);
}

async function onSessionEnd($, e, next) {
  ++revision;
  cached = { session: '', line: '' };
  return next(e);
}

async function renderStatus($, e, next) {
  if (!cached.line || cached.session !== await $.session.id()) return next(e);
  const { Box, Text, Button } = $.ui.resolve(e);
  // One line: the Team button first so it never wraps below, then the short status.
  return Box({ flexDirection: 'row', columnGap: 1, children: [
    Button({ key: 'agentworks-team', label: 'Team ›', plain: true, onPress: () => openTeam($) }),
    Text({ dimColor: true, wrap: 'truncate-end', children: [compactStatus(cached.line)] }),
  ] });
}

// The CLI line is shared with terminals ("AgentWorks · Crew X · Shared with team · N online · M new");
// the strip above the prompt keeps what changes: the Crew, who is online and what is new.
export function compactStatus(line) {
  const parts = line.split(' · ').slice(1).filter(p => p !== 'Shared with team' && p !== '0 new');
  if (parts[0] && parts[0].startsWith('Crew ')) parts[0] = parts[0].slice(5);
  if (parts.length === 1 && parts[0] === 'connected') return 'Connected to AgentWorks';
  return parts.join(' · ') || line;
}

export function registerStatus(on) {
  on('session.start', onSessionStart);
  on('turn.start', onTurnStart);
  on('turn.complete', onTurnComplete);
  on('session.end', onSessionEnd);
  on('ui.render', { component: 'AbovePrompt' }, renderStatus);
}
