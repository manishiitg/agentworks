import { useState } from 'react'
import { Chip, ChoiceItems, type Choice } from './ComposerModelChips'
import { setTeamAgentProgram, teamAgentPrograms, type TeamTarget } from './teamApi'
import { useChatStore } from '../../stores/useChatStore'

/**
 * The Team agent's model in its chat box (#3084), the same chip every chat has. Only the Team owner changes it (the
 * server says so and checks it; it applies to all the Team agent's chats); everyone else sees the label.
 */
export function TeamAgentModelChip({ target, program, canChange }: { target: TeamTarget; program?: string; canChange?: boolean }) {
  const [choices, setChoices] = useState<Choice[]>()
  const [current, setCurrent] = useState<string>()
  const [label, setLabel] = useState(program || 'Team agent')
  if (!canChange) return <span className="max-w-40 truncate px-1 text-[11px] text-muted-foreground" title="The Team owner chooses the Team agent's model">{label}</span>
  const load = (open: boolean) => {
    if (!open || choices) return
    teamAgentPrograms(target).then(list => {
      const rows: Choice[] = []
      for (const p of list) {
        const models = p.models?.length ? p.models : [{ id: p.model_id || '', label: '' }]
        for (const m of models) {
          const id = `${p.engine}|${m.id}`
          rows.push({ id, label: m.label ? `${p.label} · ${m.label}` : p.label })
          if (program && program.startsWith(p.label) && (!m.label || program.includes(m.label))) setCurrent(id)
        }
      }
      setChoices(rows)
    }).catch(error => useChatStore.getState().addToast(error instanceof Error ? error.message : 'Could not load the providers. Try again.', 'error'))
  }
  const pick = (id: string) => {
    const [engine, model] = id.split('|')
    const next = choices?.find(choice => choice.id === id)
    setTeamAgentProgram(target, engine, model).then(() => { setCurrent(id); if (next) setLabel(next.label) })
      .catch(error => useChatStore.getState().addToast(error instanceof Error ? error.message : 'Could not change the model. Try again.', 'error'))
  }
  return <Chip label={label} ariaLabel="Team agent's model" disabled={false} tag="server" onOpenChange={load} wide>
    {choices ? <ChoiceItems title="Run the Team agent on" choices={choices} value={current} onPick={pick} /> : <p className="px-2 py-1.5 text-xs text-muted-foreground">Loading…</p>}
  </Chip>
}
