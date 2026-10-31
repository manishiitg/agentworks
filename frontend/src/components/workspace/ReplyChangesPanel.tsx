import { ReplyFileChanges } from '../chat/ReplyFileChanges'
import { WorkspaceViewHeader } from '../workflow/WorkspaceViewHeader'
import type { ReplyFileChangesRef } from '../../utils/replyFileChanges'
import { openChatFiles } from './chatFileNavigation'

/** Exact saved reply, with its existing server-authorized PR, command and Undo actions. */
export function ReplyChangesPanel({ reference }: { reference: ReplyFileChangesRef }) {
  return <section className="flex h-full min-h-0 flex-col" aria-label="Changes in this reply">
    <WorkspaceViewHeader title="Files" subtitle="This reply" actions={<button type="button" className="text-xs underline" onClick={() => openChatFiles(reference.sessionId)}>All chat changes</button>} />
    <div className="min-h-0 flex-1 overflow-y-auto"><ReplyFileChanges reference={reference} panel /></div>
  </section>
}
