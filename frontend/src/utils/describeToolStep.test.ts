import { expect, it } from 'vitest'
import { describeToolStep, summarizeSteps } from './describeToolStep'

// #3307: chat steps read as plain sentences from each provider's own tool input.
it('describes Claude, Codex and unknown tool steps in plain words', () => {
  const cases: [Parameters<typeof describeToolStep>[0], string][] = [
    [{ name: 'Bash', args: '{"command":"npx vitest run","description":"Run unit tests"}' }, 'Ran "Run unit tests"'],
    [{ name: 'exec_command', args: '{"cmd":["git","status"]}' }, 'Ran git status'],
    [{ name: 'Read', args: '{"file_path":"/repo/src/ChatArea.tsx"}' }, 'Read ChatArea.tsx'],
    [{ name: 'Write', args: '{"file_path":"/repo/notes.md"}', result: 'File created successfully' }, 'Created notes.md'],
    [{ name: 'Write', args: '{"file_path":"/repo/notes.md"}', result: 'The file has been updated' }, 'Edited notes.md'],
    [{ name: 'apply_patch', args: '{"input":"*** Begin Patch\\n*** Update File: a/x.ts\\n*** Add File: a/y.ts\\n*** End Patch"}' }, 'Edited 2 files'],
    [{ name: 'WebSearch', args: '{"query":"nested tables"}' }, 'Searched the web for "nested tables"'],
    [{ name: 'createIssue', server: 'github' }, 'Used create issue in github'],
    [{ name: 'Read', args: 'not json' }, 'Read a file'],
    [{ name: 'Bash', args: '{"command":"git commit","description":"Commit the changes","run_in_background":true}' }, 'Started "Commit the changes" in the background'],
    [{ name: 'wait_for_background_tasks', server: 'api-bridge' }, 'Waited for a background command'],
    [{ name: 'mcp__api-bridge__wait_for_background_tasks' }, 'Waited for a background command'],
    [{ name: 'BashOutput', args: '{"bash_id":"b1"}' }, 'Waited for a background command'],
    [{ name: 'KillShell', args: '{"shell_id":"b1"}' }, 'Stopped a background command'],
  ]
  for (const [input, line] of cases) expect(describeToolStep(input).done).toBe(line)
})

it('counts a file edited twice once in the summary', () => {
  const steps = [
    describeToolStep({ name: 'Read', args: '{"file_path":"a.ts"}' }),
    describeToolStep({ name: 'Edit', args: '{"file_path":"a.ts"}' }),
    describeToolStep({ name: 'Edit', args: '{"file_path":"a.ts"}' }),
    describeToolStep({ name: 'Bash', args: '{"command":"npm test"}' }),
  ]
  expect(summarizeSteps(steps)).toBe('Read 1 file, edited 1 file, ran 1 command')
})

it('does not count waiting for a background command as another command (owner, 20 Nov)', () => {
  const steps = [
    describeToolStep({ name: 'Bash', args: '{"command":"npm test","run_in_background":true}' }),
    describeToolStep({ name: 'wait_for_background_tasks' }),
  ]
  expect(summarizeSteps(steps)).toBe('Ran 1 command')
  expect(summarizeSteps([describeToolStep({ name: 'TaskOutput' })])).toBe('Waited for a background command')
  expect(describeToolStep({ name: 'wait_for_background_tasks' }).live).toBe('Waiting for a background command')
})

// #3919: Antigravity's shell step is named by its command text and carries CommandLine and toolSummary.
it('describes an Antigravity shell step like other shell steps', () => {
  expect(describeToolStep({ name: 'sleep 15 && echo done', server: 'agy-cli', args: '{"CommandLine":"sleep 15 && echo done","toolSummary":"Sleep and echo done"}' }))
    .toMatchObject({ kind: 'run', done: 'Ran "Sleep and echo done"', live: 'Running "Sleep and echo done"' })
  expect(describeToolStep({ name: 'npm test', server: 'agy-cli', args: '{"CommandLine":"npm test"}' }))
    .toMatchObject({ kind: 'run', done: 'Ran npm test', live: 'Running npm test' })
})
