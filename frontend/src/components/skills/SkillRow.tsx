import { useState } from 'react'
import { Check, ChevronRight, ExternalLink, FileText, MessageCircle, Plus, Trash2, Wrench } from 'lucide-react'
import type { Skill } from '../../types/skills'
import { READ_ONLY_TITLE } from '../../hooks/useCanWriteWorkflow'
import { MarkdownRenderer } from '../ui/MarkdownRenderer'

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
  readable?: boolean
  onOpenFile?: () => void
  inclusionLabels?: string[]
  platformStatus?: string
  onRead?: () => void
  reader?: boolean
  defaultDefinitions?: { mode: string; skill: Skill }[]
  /** Installed-skill inventory: management happens through chat. */
  inventoryOnly?: boolean
}

export default function SkillRow({ skill, onDelete, selected, onToggleSelect, onRequestAdd, readOnly = false, selectionScopeLabel = 'main chat', readable = false, onOpenFile, inclusionLabels = [], platformStatus, onRead, reader = false, defaultDefinitions = [], inventoryOnly = false }: SkillRowProps) {
  const { frontmatter, folder_name, source_url } = skill
  const actionVerb = selected ? 'Remove' : onRequestAdd ? 'Ask chat to add' : 'Add'
  const actionLabel = readable ? `${selected ? 'Stop using' : onRequestAdd ? 'Ask chat to use' : 'Use'} ${frontmatter.name} in this ${selectionScopeLabel}` : `${actionVerb} ${frontmatter.name} for this ${selectionScopeLabel}`

  return (
    <div className="border-b border-gray-200 dark:border-gray-700 last:border-b-0">
      <div className="flex items-center gap-2 px-3 py-2">
        <button
          type="button"
          onClick={onRead}
          disabled={reader}
          aria-label={`Read skill ${frontmatter.name}`}
          className="flex min-w-0 flex-1 items-center gap-2 text-left"
        >
          {!readable && <span className="h-2 w-2 shrink-0 rounded-full bg-muted-foreground/40" />}
          <span className={readable ? 'min-w-0 flex-1' : 'contents'}>
          <span className={`${readable ? 'block break-words' : 'shrink-0'} text-sm font-medium text-gray-900 dark:text-gray-100`}>
            {frontmatter.name}
          </span>
          {!reader && <span className={`${readable ? 'mt-1 line-clamp-2 leading-5' : 'min-w-0 flex-1 truncate'} text-xs text-gray-500 dark:text-gray-400`}>
            {frontmatter.description}
          </span>}
          </span>
          {!reader && <ChevronRight className="h-3.5 w-3.5 shrink-0 text-gray-400" />}
        </button>
        {!inventoryOnly && onToggleSelect && !inclusionLabels.length && (
          <button
            type="button"
            onClick={onRequestAdd && !selected ? onRequestAdd : onToggleSelect}
            disabled={readOnly}
            className={`flex shrink-0 items-center justify-center gap-1 rounded-md border ${readable ? 'min-h-7 px-2 py-1 text-[11px]' : 'p-1'} transition-colors disabled:cursor-not-allowed disabled:opacity-50 ${
              selected
                ? 'border-primary/40 bg-primary/15 text-primary hover:border-red-500/40 hover:bg-red-500/15 hover:text-red-500'
                : 'border-gray-300 text-gray-500 hover:bg-gray-100 hover:text-gray-900 dark:border-gray-600 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-100'
            }`}
            title={readOnly ? READ_ONLY_TITLE : actionLabel}
            aria-label={actionLabel}
            aria-pressed={selected}
          >
            {readable ? <>{selected && <Check className="h-3 w-3" />}<span>{selected ? 'Selected' : onRequestAdd ? 'Ask chat' : 'Use here'}</span></> : selected ? <Check className="h-3.5 w-3.5" /> : onRequestAdd ? <MessageCircle className="h-3.5 w-3.5" /> : <Plus className="h-3.5 w-3.5" />}
          </button>
        )}
      </div>

      <div className="flex flex-wrap gap-x-2 gap-y-1 px-3 pb-2 text-xs text-muted-foreground">
        {inventoryOnly && !reader && <span>{skill.brain_source ? `From Brain: ${skill.brain_source}` : skill.source ? `From ${skill.source}` : 'Source not recorded'}</span>}
        {inclusionLabels.map(label => <span key={label}>{label}</span>)}
        {platformStatus && <span>{platformStatus}</span>}
        {!inventoryOnly && !inclusionLabels.length && !skill.used_by?.length && <span>Available · Not selected</span>}
        {skill.validation_error && <span className="text-destructive">File needs attention</span>}
        {!readable && <span className="font-mono">{skill.file_path}</span>}
        {!inventoryOnly && skill.used_by?.map((label, index) => <span key={`${label}:${index}`}>{label}</span>)}
        {!readable && skill.managed && <span>Managed by platform</span>}
      </div>
      {reader && (
        <div className="space-y-2.5 border-t border-gray-100 bg-gray-50 px-3 py-3 dark:border-gray-800 dark:bg-gray-900/40">
          <p className="text-xs text-gray-600 dark:text-gray-400">{frontmatter.description}</p>
          {readable && <p className="break-all font-mono text-[11px] text-muted-foreground">{skill.file_path}</p>}
          {!skill.file_path && <p className="text-xs text-muted-foreground">Declared by the product; no project file is installed.</p>}
          {skill.validation_error && <p className="text-xs text-destructive">{skill.validation_error}</p>}
          <p className="break-words text-xs text-muted-foreground">{skill.brain_source ? `From Brain: ${skill.brain_source}${skill.brain_edited ? ' · Edited locally' : ''}` : skill.source ? `Recorded source: ${skill.source}${skill.source_type ? ` (${skill.source_type})` : ''}` : skill.file_managed ? 'Source: platform-managed file' : skill.builtin && skill.file_path === 'Platform' ? 'Source: platform registry' : inclusionLabels.length && !skill.file_path ? 'Source: product declaration' : 'Original file source not recorded'}</p>
          {skill.builtin && skill.file_path !== 'Platform' && <p className="text-xs text-muted-foreground">AgentWorks supplies the runtime instructions for this name. This project also has the saved file shown here.</p>}
          {(skill.content || defaultDefinitions.length > 0) && <SkillInstructions skill={skill} definitions={defaultDefinitions} />}
          {onOpenFile && <button type="button" onClick={onOpenFile} className="inline-flex min-h-8 items-center gap-1.5 rounded-md border border-border px-2.5 py-1.5 text-xs hover:bg-muted"><FileText className="h-3.5 w-3.5" />Open in Files</button>}

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
            {onDelete && !inclusionLabels.length ? <button
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
function SkillInstructions({ skill, definitions }: { skill: Skill; definitions: { mode: string; skill: Skill }[] }) {
  const hasSavedFile = skill.file_path && skill.file_path !== 'Platform'
  const documents = [
    ...(hasSavedFile || !definitions.length ? [{ id: 'file', label: hasSavedFile ? 'Saved file' : 'Instructions', skill }] : []),
    ...definitions.map(definition => ({ id: definition.mode, label: `${definition.mode === 'builder' ? 'Builder' : definition.mode === 'run' ? 'Run' : definition.mode} default`, skill: definition.skill })),
  ]
  const [documentId, setDocumentId] = useState(documents[0]?.id)
  const current = documents.find(document => document.id === documentId) || documents[0]
  return <div className="min-w-0 space-y-3 rounded-lg border border-border bg-background p-3">
    {documents.length > 1 && <div role="group" aria-label="Instruction source" className="flex flex-wrap gap-2">{documents.map(document => <button type="button" key={document.id} aria-pressed={current?.id === document.id} onClick={() => setDocumentId(document.id)} className={`rounded-md border px-2 py-1 text-xs ${current?.id === document.id ? 'border-primary bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:bg-muted'}`}>{document.label}</button>)}</div>}
    {documents.length === 1 && definitions.length > 0 && <p className="text-xs text-muted-foreground">{current?.label}</p>}
    {current && <MarkdownRenderer content={current.skill.content} basePath={current.skill.file_path === 'Platform' ? undefined : current.skill.file_path} />}
  </div>
}
