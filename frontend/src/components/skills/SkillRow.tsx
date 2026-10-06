import { useState } from 'react'
import { Check, ChevronRight, ExternalLink, FileText, MessageCircle, Plus, Trash2, Wrench } from 'lucide-react'
import type { Skill } from '../../types/skills'
import { READ_ONLY_TITLE } from '../../hooks/useCanWriteWorkflow'

interface SkillRowProps {
  skill: Skill
  /** Omitted for skills managed by the platform. */
  onDelete?: () => void
  // When provided (the workflow-panel embedding), the row also gets an
  // add/remove-from-workflow toggle -- kept as a separate control from the
  // expand/collapse click target so the two actions never conflict.
  selected?: boolean
  onToggleSelect?: () => void
  /** When provided, the unselected + button asks chat to add the skill
   * instead of toggling directly -- removal still toggles immediately. */
  onRequestAdd?: () => void
  /** Disables the toggle and delete for a user without write access. */
  readOnly?: boolean
  /** Keeps this shared row's action copy correct outside Workflow. */
  selectionScopeLabel?: string
}

export default function SkillRow({ skill, onDelete, selected, onToggleSelect, onRequestAdd, readOnly = false, selectionScopeLabel = 'main chat' }: SkillRowProps) {
  const [expanded, setExpanded] = useState(false)
  const { frontmatter, folder_name, source_url } = skill
  const actionVerb = selected ? 'Remove' : onRequestAdd ? 'Ask AI to add' : 'Add'

  return (
    <div className="border-b border-gray-200 dark:border-gray-700 last:border-b-0">
      <div className="flex items-center gap-2 px-3 py-2">
        <button
          type="button"
          onClick={() => setExpanded(prev => !prev)}
          aria-expanded={expanded}
          className="flex min-w-0 flex-1 items-center gap-2 text-left"
        >
          <span className="h-2 w-2 shrink-0 rounded-full bg-muted-foreground/40" />
          <span className="shrink-0 text-sm font-medium text-gray-900 dark:text-gray-100">
            {frontmatter.name}
          </span>
          <span className="min-w-0 flex-1 truncate text-xs text-gray-500 dark:text-gray-400">
            {frontmatter.description}
          </span>
          <ChevronRight className={`h-3.5 w-3.5 shrink-0 text-gray-400 transition-transform ${expanded ? 'rotate-90' : ''}`} />
        </button>
        {onToggleSelect && (
          <button
            type="button"
            onClick={onRequestAdd && !selected ? onRequestAdd : onToggleSelect}
            disabled={readOnly}
            className={`flex shrink-0 items-center justify-center rounded-md border p-1 transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
              selected
                ? 'border-primary/40 bg-primary/15 text-primary hover:border-red-500/40 hover:bg-red-500/15 hover:text-red-500'
                : 'border-gray-300 text-gray-500 hover:bg-gray-100 hover:text-gray-900 dark:border-gray-600 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-100'
            }`}
            title={readOnly ? READ_ONLY_TITLE : `${actionVerb} ${frontmatter.name} for this ${selectionScopeLabel}`}
            aria-label={`${actionVerb} ${frontmatter.name} for this ${selectionScopeLabel}`}
          >
            {selected ? <Check className="h-3.5 w-3.5" /> : onRequestAdd ? <MessageCircle className="h-3.5 w-3.5" /> : <Plus className="h-3.5 w-3.5" />}
          </button>
        )}
      </div>

      <div className="flex flex-wrap gap-x-2 gap-y-1 px-3 pb-2 text-xs text-muted-foreground">
        <span className="font-mono">{skill.file_path}</span>
        {skill.used_by?.map((label, index) => <span key={`${label}:${index}`}>{label}</span>)}
        {skill.managed && <span>Managed by platform</span>}
      </div>
      {expanded && (
        <div className="space-y-2.5 border-t border-gray-100 bg-gray-50 px-3 py-3 dark:border-gray-800 dark:bg-gray-900/40">
          <p className="text-xs text-gray-600 dark:text-gray-400">{frontmatter.description}</p>

          {(frontmatter.argument_hint || (frontmatter.allowed_tools && frontmatter.allowed_tools.length > 0) || frontmatter.model) && (
            <div className="flex flex-wrap gap-1.5 text-xs">
              {frontmatter.argument_hint && (
                <span className="flex items-center gap-1 rounded bg-blue-100 px-2 py-1 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300">
                  <FileText className="h-3 w-3" />
                  {frontmatter.argument_hint}
                </span>
              )}
              {frontmatter.allowed_tools && frontmatter.allowed_tools.length > 0 && (
                <span className="flex items-center gap-1 rounded bg-green-100 px-2 py-1 text-green-700 dark:bg-green-900/30 dark:text-green-300">
                  <Wrench className="h-3 w-3" />
                  {frontmatter.allowed_tools.length} tool{frontmatter.allowed_tools.length !== 1 ? 's' : ''}
                </span>
              )}
              {frontmatter.model && (
                <span className="rounded bg-orange-100 px-2 py-1 text-orange-700 dark:bg-orange-900/30 dark:text-orange-300">
                  {frontmatter.model}
                </span>
              )}
            </div>
          )}

          {source_url && (
            <div className="flex items-center gap-2 text-xs">
              <span className="font-medium text-gray-600 dark:text-gray-400">Source:</span>
              <a
                href={source_url}
                target="_blank"
                rel="noopener noreferrer"
                className="flex items-center gap-1 truncate text-blue-600 hover:underline dark:text-blue-400"
              >
                {source_url}
                <ExternalLink className="h-3 w-3 shrink-0" />
              </a>
            </div>
          )}

          <div className="flex items-center justify-between pt-1">
            {folder_name !== frontmatter.name ? (
              <span className="font-mono text-[11px] text-gray-400 dark:text-gray-500">{folder_name}</span>
            ) : <span />}
            {onDelete ? <button
              type="button"
              onClick={onDelete}
              disabled={readOnly}
              title={readOnly ? READ_ONLY_TITLE : undefined}
              className="flex items-center gap-1 rounded-md px-2 py-1 text-xs text-gray-500 transition-colors hover:bg-red-50 hover:text-red-600 disabled:cursor-not-allowed disabled:opacity-50 dark:text-gray-400 dark:hover:bg-red-900/20 dark:hover:text-red-400"
            >
              <Trash2 className="h-3.5 w-3.5" />
              Uninstall
            </button> : null}
          </div>
        </div>
      )}
    </div>
  )
}
