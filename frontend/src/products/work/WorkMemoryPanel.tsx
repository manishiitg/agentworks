import { useCallback, useEffect, useState } from 'react'
import { Brain, Loader2, Sparkles } from 'lucide-react'
import { MarkdownRenderer } from '../../components/ui/MarkdownRenderer'
import { WorkspaceViewActions } from '../../components/workflow/WorkspaceViewActions'
import { WorkspaceViewHeader } from '../../components/workflow/WorkspaceViewHeader'
import { flattenFiles, responseFiles } from '../../utils/plannerFiles'
import { proxyCrewFileClient, type CrewFileClient } from './sharedCrewFiles'

type CrewSkill = {
  name: string
  description: string
  filePath: string
}

function unquoteYamlValue(value: string): string {
  const trimmed = value.trim()
  if ((trimmed.startsWith('"') && trimmed.endsWith('"')) || (trimmed.startsWith("'") && trimmed.endsWith("'"))) {
    return trimmed.slice(1, -1)
  }
  return trimmed
}

function parseCrewSkill(filePath: string, content: string): CrewSkill {
  const fallbackName = filePath.split('/').slice(-2, -1)[0] || 'Custom skill'
  const frontmatter = content.startsWith('---') ? content.split(/\n---\s*\n/, 1)[0] : ''
  const name = frontmatter.match(/^name:\s*(.+)$/m)?.[1]
  const description = frontmatter.match(/^description:\s*(.+)$/m)?.[1]
  return {
    name: name ? unquoteYamlValue(name) : fallbackName,
    description: description ? unquoteYamlValue(description) : '',
    filePath,
  }
}

async function loadCrewSkills(workspacePath: string, files: Pick<CrewFileClient, 'listFiles' | 'readFile'> = proxyCrewFileClient): Promise<CrewSkill[]> {
  const skillsRoot = `${workspacePath}/skills`
  let listing
  try {
    listing = await files.listFiles(skillsRoot, -1, 4)
  } catch (cause) {
    const status = (cause as { response?: { status?: number } } | undefined)?.response?.status
    if (status === 404) return []
    throw cause
  }
  const skillFiles = [...new Set(flattenFiles(responseFiles(listing))
    .filter(file => file.type !== 'folder')
    .map(file => file.filepath.replace(/\\/g, '/'))
    // Crew-authored skills are project-local packages. Accept the old nested
    // custom/ form for projects that wrote it before this contract was made
    // explicit, but never read the account-wide skills/custom library.
    .filter(filePath => new RegExp(`^${skillsRoot.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}/(?:custom/)?[^/]+/SKILL\\.md$`).test(filePath)))]
    // Prefer the runtime-native project path when both it and the legacy
    // skills/custom path contain the same skill.
    .sort((a, b) => Number(a.startsWith(`${skillsRoot}/custom/`)) - Number(b.startsWith(`${skillsRoot}/custom/`)))
  const loaded = await Promise.all(skillFiles.map(async filePath => {
    const response = await files.readFile(filePath)
    return parseCrewSkill(filePath, typeof response?.data?.content === 'string' ? response.data.content : '')
  }))
  const deduplicated = new Map<string, CrewSkill>()
  for (const skill of loaded) {
    const identity = skill.name.trim().toLocaleLowerCase()
    if (!deduplicated.has(identity)) deduplicated.set(identity, skill)
  }
  return [...deduplicated.values()].sort((a, b) => a.name.localeCompare(b.name))
}

export function WorkMemoryPanel({ workspacePath, onAsk, onOpenFile, fileClient, readOnly }: {
  workspacePath: string
  onAsk: (message: string) => Promise<void>
  onOpenFile: (filePath: string) => void
  /** Shared Crews pass the mediated client; owned Crews use the proxy. */
  fileClient?: CrewFileClient
  readOnly?: boolean
}) {
  const [content, setContent] = useState('')
  const [skills, setSkills] = useState<CrewSkill[]>([])
  const [loading, setLoading] = useState(true)
  const [memoryError, setMemoryError] = useState<string | null>(null)
  const [skillsError, setSkillsError] = useState<string | null>(null)
  const [refreshNonce, setRefreshNonce] = useState(0)

  const refresh = useCallback(() => setRefreshNonce(value => value + 1), [])

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setMemoryError(null)
    setSkillsError(null)
    const files = fileClient ?? proxyCrewFileClient
    void Promise.allSettled([
      files.readFile(`${workspacePath}/MEMORY.md`),
      loadCrewSkills(workspacePath, files),
    ]).then(([memoryResult, skillsResult]) => {
      if (cancelled) return
      if (memoryResult.status === 'fulfilled') {
        const body = memoryResult.value?.data?.content
        setContent(typeof body === 'string' ? body : '')
      } else {
        const status = (memoryResult.reason as { response?: { status?: number } } | undefined)?.response?.status
        // A new Crew project legitimately has no memory file yet. Other
        // failures must stay visible instead of masquerading as empty memory.
        setContent('')
        if (status !== 404) setMemoryError('Project memory could not be loaded.')
      }
      if (skillsResult.status === 'fulfilled') {
        setSkills(skillsResult.value)
      } else {
        setSkills([])
        setSkillsError('Linked skills could not be loaded.')
      }
    }).finally(() => {
      if (!cancelled) setLoading(false)
    })
    return () => { cancelled = true }
  }, [fileClient, refreshNonce, workspacePath])

  return (
    <div className="flex h-full min-h-0 flex-col bg-background">
      <WorkspaceViewHeader
        icon={Brain}
        title="Memory"
        actions={<WorkspaceViewActions
          workspacePath={workspacePath}
          message={readOnly
            ? "Review this Crew project's MEMORY.md and its project-local skills. Explain what the Crew remembers and how it uses each skill. This Crew is read-only for me, so do not offer to change anything."
            : "Review this Crew project's MEMORY.md and its project-local skills. Explain what Crew currently remembers, identify anything stale or missing, and ask what I want to update. Keep project facts in memory and reusable procedures in focused skills inside this project."}
          onAsk={onAsk}
          onRefresh={refresh}
          refreshing={loading}
          refreshLabel="Refresh memory"
        />}
      />
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        {loading ? (
          <div className="flex h-full items-center justify-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" /> Loading memory…
          </div>
        ) : (
          <div className="mx-auto flex w-full max-w-5xl flex-col gap-4">
            <section className="rounded-lg border border-border bg-card p-4 shadow-sm">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <div className="flex items-center gap-2">
                    <Sparkles className="h-4 w-4 text-primary" />
                    <h3 className="text-sm font-semibold text-foreground">Custom skills</h3>
                  </div>
                  <p className="mt-1 text-xs leading-5 text-muted-foreground">
                    Reusable procedures stored inside this Crew project, alongside its memory.
                  </p>
                </div>
              </div>
              <div className="mt-3 flex flex-wrap gap-2">
                {skills.length === 0 ? (
                  <p className="w-full rounded-md bg-muted/60 px-3 py-2 text-xs text-muted-foreground">Crew has not created any custom skills yet.</p>
                ) : skills.map(skill => (
                  <button
                    key={skill.filePath}
                    type="button"
                    onClick={() => onOpenFile(skill.filePath)}
                    title={`Open ${skill.filePath} in Files`}
                    className="min-w-48 max-w-80 rounded-md border border-border px-3 py-2 text-left transition-colors hover:border-primary/40 hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                  >
                    <span className="flex items-center justify-between gap-2 text-xs font-medium text-foreground">
                      <span className="truncate">{skill.name}</span>
                      <span className="rounded-full bg-primary/10 px-2 py-0.5 text-[10px] font-medium text-primary">Custom</span>
                    </span>
                    {skill.description && <span className="mt-1 block line-clamp-2 text-[11px] leading-4 text-muted-foreground">{skill.description}</span>}
                  </button>
                ))}
              </div>
              {skillsError && <p className="mt-3 text-xs text-destructive">{skillsError}</p>}
            </section>

            <section className="min-w-0 rounded-lg border border-border bg-card p-5 shadow-sm">
              {memoryError && <p className="mb-4 rounded-md bg-destructive/10 p-3 text-xs text-destructive">{memoryError}</p>}
              {content.trim() ? (
                <MarkdownRenderer
                  content={content}
                  basePath={`${workspacePath}/MEMORY.md`}
                  className="[&_h1:first-child]:mt-0"
                />
              ) : (
                <div className="flex min-h-64 flex-col items-center justify-center px-6 text-center">
                  <Brain className="mb-3 h-9 w-9 text-muted-foreground/50" />
                  <h3 className="text-sm font-semibold text-foreground">No project memory yet</h3>
                  <p className="mt-1 max-w-md text-sm leading-6 text-muted-foreground">
                    Ask Crew to remember durable decisions, preferences, and learned context. Live facts that can be looked up again should stay out of memory.
                  </p>
                </div>
              )}
            </section>
          </div>
        )}
      </div>
    </div>
  )
}
