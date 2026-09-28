import { describe, expect, it } from 'vitest'
import type { PlannerFile } from '../services/api-types'
import {
  hideManagedWorkspaceRootEntries,
  EXPAND_FIRST_LEVEL_FOLDERS_BY_DEFAULT,
  getInitialExpandedWorkspaceFolders,
  isIterationFolder,
  publicWorkspacePathForUser,
  resolveWorkspaceUploadPath,
  workspacePathsEqualForUser,
} from './workspacePathUtils'

describe('workspace upload destinations', () => {
  const crewPath = 'Chats/Work/projects/my-crew'

  it('uses the crew path for its displayed root and children', () => {
    expect(resolveWorkspaceUploadPath('/', crewPath)).toBe(crewPath)
    expect(resolveWorkspaceUploadPath('notes', crewPath)).toBe(`${crewPath}/notes`)
    expect(resolveWorkspaceUploadPath('/notes', crewPath)).toBe(`${crewPath}/notes`)
    expect(resolveWorkspaceUploadPath(`${crewPath}/notes`, crewPath)).toBe(`${crewPath}/notes`)
    expect(resolveWorkspaceUploadPath(`${crewPath}-other`, crewPath)).toBe(`${crewPath}/${crewPath}-other`)
  })

  it('keeps the global root for an unscoped workspace', () => {
    expect(resolveWorkspaceUploadPath('/')).toBe('/')
    expect(resolveWorkspaceUploadPath('Chats', null)).toBe('Chats')
  })
})

describe('shared workspace folder defaults', () => {
  it('opens only the workspace root and keeps every first-level folder collapsed', () => {
    const root = {
      filepath: 'project',
      type: 'folder',
      children: [
        { filepath: 'project/.agents', type: 'folder' },
        { filepath: 'project/.codex', type: 'folder' },
        { filepath: 'project/frontend', type: 'folder' },
      ],
    } satisfies PlannerFile

    expect(EXPAND_FIRST_LEVEL_FOLDERS_BY_DEFAULT).toBe(false)
    expect([...getInitialExpandedWorkspaceFolders(root)]).toEqual(['project'])
    expect([...getInitialExpandedWorkspaceFolders(null)]).toEqual([])
  })

  it('hides only managed entries directly below a scoped root', () => {
    const root = {
      filepath: 'project',
      type: 'folder',
      children: [
        { filepath: 'project/.agents', type: 'folder' },
        { filepath: 'project/AGENTS.md', type: 'file' },
        { filepath: 'project/builder', type: 'folder' },
        { filepath: 'project/config', type: 'folder' },
        { filepath: 'project/costs', type: 'folder' },
        { filepath: 'project/db', type: 'folder' },
        { filepath: 'project/evaluation', type: 'folder' },
        { filepath: 'project/execution', type: 'folder' },
        { filepath: 'project/experiments', type: 'folder' },
        { filepath: 'project/knowledgebase', type: 'folder' },
        { filepath: 'project/learnings', type: 'folder' },
        { filepath: 'project/memory', type: 'folder' },
        { filepath: 'project/planning', type: 'folder' },
        { filepath: 'project/publish', type: 'folder' },
        { filepath: 'project/reports', type: 'folder' },
        { filepath: 'project/runs', type: 'folder' },
        { filepath: 'project/scores', type: 'folder' },
        {
          filepath: 'project/skills',
          type: 'folder',
          children: [
            {
              filepath: 'project/skills/custom',
              type: 'folder',
              children: [{ filepath: 'project/skills/custom/company-ca-context/SKILL.md', type: 'file' }],
            },
            { filepath: 'project/skills/internal-platform-skill', type: 'folder' },
          ],
        },
        { filepath: 'project/soul', type: 'folder' },
        { filepath: 'project/variables', type: 'folder' },
        { filepath: 'project/tool_output_folder', type: 'folder' },
        {
          filepath: 'project/code',
          type: 'folder',
          children: [{ filepath: 'project/code/db', type: 'folder' }],
        },
        { filepath: 'project/report.md', type: 'file' },
      ],
    } satisfies PlannerFile

    const [visibleRoot] = hideManagedWorkspaceRootEntries([root])
    expect(visibleRoot.children?.map(file => file.filepath)).toEqual([
      'project/db',
      'project/evaluation',
      'project/execution',
      'project/experiments',
      'project/knowledgebase',
      'project/learnings',
      'project/memory',
      'project/publish',
      'project/reports',
      'project/runs',
      'project/scores',
      'project/skills',
      'project/code',
      'project/report.md',
    ])
    const visibleSkills = visibleRoot.children?.find(file => file.filepath === 'project/skills')
    expect(visibleSkills?.children?.map(file => file.filepath)).toEqual(['project/skills/custom'])
    expect(visibleSkills?.children?.[0].children?.[0].filepath).toBe('project/skills/custom/company-ca-context/SKILL.md')
    expect(visibleRoot.children?.find(file => file.filepath === 'project/code')?.children?.[0].filepath).toBe('project/code/db')
  })

  it('keeps an internal-only skills folder hidden', () => {
    const [visibleRoot] = hideManagedWorkspaceRootEntries([{
      filepath: 'project',
      type: 'folder',
      children: [{
        filepath: 'project/skills',
        type: 'folder',
        children: [{ filepath: 'project/skills/internal-platform-skill', type: 'folder' }],
      }],
    }])

    expect(visibleRoot.children).toEqual([])
  })

  it('supports product-specific hidden root entries', () => {
    const visible = hideManagedWorkspaceRootEntries([
      { filepath: 'code', type: 'folder' },
      { filepath: 'generated', type: 'folder' },
    ], ['generated'])

    expect(visible.map(file => file.filepath)).toEqual(['code'])
  })
})

describe('trigger iteration paths', () => {
  it('recognizes hook and schedule roots and groups alongside ordinary runs', () => {
    for (const path of ['runs/iteration-1-hook', 'iteration-2-hook/dev', 'runs/iteration-3-sched', 'iteration-4-sched/measurement', 'iteration-0/dev']) {
      expect(isIterationFolder(path)).toBe(true)
    }
    expect(isIterationFolder('runs/iteration-1-hook/dev/execution')).toBe(false)
    expect(isIterationFolder('iteration-1-hook/../other')).toBe(false)
  })
})

describe('user-scoped workspace path identity', () => {
  it('maps the current user physical Work path to its public identity', () => {
    expect(publicWorkspacePathForUser(
      '_users/user-1/Chats/Work/projects/project-1/',
      'user-1',
    )).toBe('Chats/Work/projects/project-1')
    expect(workspacePathsEqualForUser(
      'Chats/Work/projects/project-1',
      '_users/user-1/Chats/Work/projects/project-1',
      'user-1',
    )).toBe(true)
  })

  it('does not collapse another user physical path', () => {
    expect(publicWorkspacePathForUser(
      '_users/user-2/Chats/Work/projects/project-1',
      'user-1',
    )).toBe('_users/user-2/Chats/Work/projects/project-1')
    expect(workspacePathsEqualForUser(
      'Chats/Work/projects/project-1',
      '_users/user-2/Chats/Work/projects/project-1',
      'user-1',
    )).toBe(false)
  })
})
