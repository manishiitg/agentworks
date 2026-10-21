import { Archive, BookOpen, Plug, ShieldCheck, UsersRound } from 'lucide-react'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextSection as Section } from '../../components/workspace/WorkspaceContextCard'
import type { KnowledgeBootstrap } from '../../services/knowledgebaseApi'
import type { KnowledgebaseView } from './KnowledgebaseWorkspacePane'

export function KnowledgebaseContextPanel({ bootstrap, folder, view, onOpen, onConnect }: {
  bootstrap: KnowledgeBootstrap
  folder: string
  view: KnowledgebaseView
  onOpen: (view: KnowledgebaseView) => void
  onConnect: () => void
}) {
  const canWrite = bootstrap.can_write !== false
  return <WorkspaceContextCard title="Brain" summary="Shared knowledge for your agents." subtle>
    <Section title="Knowledge"><Row icon={BookOpen} active={view === 'library'} onClick={() => onOpen('library')}>Library</Row></Section>
    {canWrite ? <>
      <Section title="Manage"><Row icon={ShieldCheck} active={view === 'access'} hint={folder || 'Organization root'} onClick={() => onOpen('access')}>Folder access</Row></Section>
      <Section title="Use your knowledge"><Row icon={UsersRound} active={view === 'crew'} onClick={() => onOpen('crew')}>Use in a Crew</Row><Row icon={Plug} onClick={onConnect}>Connect AI tools</Row></Section>
      <div className="border-t border-border pt-3">
        <Row icon={Archive} active={view === 'backup'} attention={!bootstrap.backup_configured && bootstrap.is_admin} detail={bootstrap.backup_configured ? 'Configured' : 'Not set up'} onClick={() => onOpen('backup')}>Backup</Row>
      </div>
    </> : <p className="px-1 text-xs leading-5 text-muted-foreground">You can open files shared with you.</p>}
  </WorkspaceContextCard>
}
