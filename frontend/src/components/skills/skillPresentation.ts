import type { Skill } from '../../types/skills'

interface ProfileSkillPresentation {
  byMode: Record<string, string[]>
  definitionsByMode: Record<string, Skill[]>
  modesReported: boolean
}

/** Presentation reads declarations only; filenames never establish origin or inclusion. */
export function profileSkillPresentation(profile: Record<string, unknown>): ProfileSkillPresentation {
  const names = (value: unknown): string[] => {
    if (value === undefined || value === null) return []
    if (!Array.isArray(value) || value.some(name => typeof name !== 'string')) throw new Error('Invalid product skill defaults')
    return [...new Set((value as string[]).map(name => name.trim()).filter(Boolean))]
  }
  if (profile.skill_defaults_by_mode === undefined) {
    return { byMode: { profile: names(profile.skills) }, definitionsByMode: {}, modesReported: false }
  }
  const declared = profile.skill_defaults_by_mode
  if (!declared || typeof declared !== 'object' || Array.isArray(declared)) throw new Error('Invalid product skill defaults')
  const rawDefinitions = profile.skill_definitions_by_mode ?? {}
  if (!rawDefinitions || typeof rawDefinitions !== 'object' || Array.isArray(rawDefinitions)) throw new Error('Invalid product skill definitions')
  const definitionsByMode: Record<string, Skill[]> = {}
  for (const [mode, definitions] of Object.entries(rawDefinitions)) {
    if (!Array.isArray(definitions) || definitions.some(skill => !skill || typeof skill.folder_name !== 'string' || typeof skill.content !== 'string' || typeof skill.frontmatter?.name !== 'string')) throw new Error('Invalid product skill definitions')
    definitionsByMode[mode] = definitions
  }
  return { byMode: Object.fromEntries(Object.entries(declared).map(([mode, value]) => [mode, names(value)])), definitionsByMode, modesReported: true }
}

export function defaultSkillLabel(mode: string) {
  return `${mode === 'profile' ? 'Profile' : mode === 'builder' ? 'Builder' : mode === 'run' ? 'Run' : mode} default`
}
