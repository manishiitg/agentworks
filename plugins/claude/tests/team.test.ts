import { expect, test } from 'claude-code/testing';

const at = new Date(Date.now() - 12 * 60000).toISOString();
const PANE = JSON.stringify({
  attached: true, crew_name: 'Example Crew', team_on: true, in_team: true,
  needs: [{ line: 'Review the mockup', ref: '#12', agent: 'website-agent', person: 'Teammate', buttons: ['Approve', 'Not now'], at }],
  asks: [{ title: 'Fix helper rows', state: 'in_progress', asked_by: 'your reviewer', last: 'Merged on dev', at }],
  agents: [
    { id: 'a-own', mine: true, name: 'my-helper', kind: 'agent', online: false, offline_line: 'Offline for 12 minutes' },
    { id: 'a-1', name: 'reviewer', person: 'Owner', kind: 'agent', online: true, activity: 'running' },
  ],
});

// #3357: the Team pane only shows what the app returns, grouped by person; no answer/reply/done controls.
test('Team pane shows needs, asks and agents grouped by person, without answer controls', async ($, on) => {
  on('process.run', ($, e) => ({ value: { exitCode: 0, stdout: e.argv?.includes('team-pane') || JSON.stringify(e).includes('team-pane') ? PANE + '\n' : '', stderr: '' } }));
  on('env.get', () => ({ value: '0' }));
  on('session.id', () => ({ value: 'session-1' }));
  on('command.register', () => ({ value: undefined }));
  on('ui.open', () => ({ value: { isPlaced: true } }));
  on('ui.invalidate', () => ({ value: undefined }));
  on('session.start', ($, e) => e);
  on('command.run', () => ({}));
  await $.session.start({ cwd: '/tmp' });
  await $.command.run({ command: 'team', args: '' });
  for (const surface of ['terminal', 'desktop'] as const) {
    const pane = await $.ui.mount({ plugin: 'agentworks', component: 'Pane', requestId: 'agentworks-team', surface,
      props: { title: 'AgentWorks Team', isFocused: false, bodyColumns: 48, placement: 'dock' }, viewport: { columns: 140, rows: 40 } });
    expect(await pane.find({ type: 'Text', text: '#12 Review the mockup' })).toBeDefined();
    expect(await pane.find({ type: 'Text', text: 'Teammate’s website-agent' })).toBeDefined();
    expect(await pane.find({ type: 'Text', text: 'Fix helper rows' })).toBeDefined();
    expect(await pane.find({ type: 'Text', text: 'Working now' })).toBeDefined();
    expect(await pane.find({ type: 'Text', text: 'You' })).toBeDefined();
    expect(await pane.find({ type: 'Text', text: 'Owner' })).toBeDefined();
    expect(await pane.find({ type: 'Button', text: 'reviewer ›' })).toBeDefined();
    expect(await pane.find({ type: 'Button', text: 'Refresh' })).toBeDefined();
    expect(await pane.find({ type: 'Button', text: 'Approve' })).toBeUndefined();
    expect(await pane.find({ type: 'Input' })).toBeUndefined();
    await pane.unmount();
  }
});
