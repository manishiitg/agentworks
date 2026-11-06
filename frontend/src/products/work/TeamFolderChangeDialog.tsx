import { Loader2, X } from 'lucide-react'
import { useEffect } from 'react'
import ModalPortal from '../../components/ui/ModalPortal'
import { Button } from '../../components/ui/Button'
import type { TeamFolderChange } from './teamApi'

/** The Folders / Whole Crew confirm box (owner, 6 Nov, approved mockup "Whole Crew asks first"). Every line is the
 * server's (/folder-change); this only lays it out. Nothing changes until the confirm button. */
export function TeamFolderChangeDialog({ change, saving, onCancel, onConfirm }: { change: TeamFolderChange; saving: boolean; onCancel: () => void; onConfirm: () => void }) {
  useEffect(() => {
    const key = (event: KeyboardEvent) => { if (event.key === 'Escape' && !saving) onCancel() }
    document.addEventListener('keydown', key)
    return () => document.removeEventListener('keydown', key)
  }, [saving, onCancel])
  const label = 'mb-1.5 mt-3.5 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground'
  const chip = 'rounded-full border border-border px-2.5 py-0.5 text-xs'
  return <ModalPortal>
    <div className="fixed inset-0 z-[10000] flex items-center justify-center bg-black/50 p-4" onClick={() => { if (!saving) onCancel() }}>
      <div role="dialog" aria-modal="true" aria-label={change.title} onClick={e => e.stopPropagation()} className="w-full max-w-md rounded-xl border border-border bg-card p-5 text-sm shadow-md">
        <div className="flex items-start gap-2">
          <h3 className="flex-1 text-[15px] font-semibold">{change.title}</h3>
          <button type="button" aria-label="Close" disabled={saving} onClick={onCancel} className="rounded p-1 text-muted-foreground hover:bg-muted"><X className="h-4 w-4" /></button>
        </div>
        <p className="mt-1 text-[13px] text-muted-foreground">{change.sub}</p>
        {change.whole && change.people.length > 0 && <>
          <p className={label}>Who gets it</p>
          <div className="flex flex-wrap gap-1.5">{change.people.map(name => <span key={name} className={chip}>{name}</span>)}</div>
        </>}
        {!!change.folders_added?.length && <>
          <p className={label}>Folders added</p>
          <div className="flex flex-wrap gap-1.5">{change.folders_added.map(folder => <span key={folder} className={`${chip} border-amber-500/40 bg-amber-500/10`}>{folder}</span>)}</div>
        </>}
        {change.refusal ? <p role="alert" className="mt-3.5 rounded-lg bg-destructive/10 px-3 py-2 text-[13px] text-destructive">{change.refusal}</p> : <>
          {change.means.length > 0 && <>
            {change.whole && <p className={label}>What it means</p>}
            <ul className={`list-disc space-y-1 pl-5 text-[13px] ${change.whole ? '' : 'mt-3'}`}>{change.means.map(line => <li key={line}>{line}</li>)}</ul>
          </>}
          {change.note && <p className="mt-3.5 rounded-lg bg-muted/60 px-3 py-2 text-xs text-muted-foreground">{change.note}</p>}
        </>}
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" size="sm" disabled={saving} onClick={onCancel}>Cancel</Button>
          {!change.refusal && change.confirm_label && <Button size="sm" disabled={saving} onClick={onConfirm}>
            {saving && <Loader2 aria-label="Saving" className="animate-spin" />}{change.confirm_label}
          </Button>}
        </div>
      </div>
    </div>
  </ModalPortal>
}
