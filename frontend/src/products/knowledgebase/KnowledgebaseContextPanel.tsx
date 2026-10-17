import { Archive, BookOpen, BrainCircuit, CalendarClock, KeyRound, MessageCircle, Plug, ShieldCheck } from 'lucide-react'
import { WorkspaceContextCard, WorkspaceContextRow as Row, WorkspaceContextSection as Section } from '../../components/workspace/WorkspaceContextCard'
import type { KnowledgeBootstrap } from '../../services/knowledgebaseApi'
import type { KnowledgebaseView } from './KnowledgebaseWorkspacePane'

export function KnowledgebaseContextPanel({ bootstrap, folder, view, onOpen, onChat, onConnect }: {
  bootstrap: KnowledgeBootstrap
  folder: string
  view: KnowledgebaseView
  onOpen: (view: KnowledgebaseView) => void
  onChat: () => void
  onConnect: () => void
}) {
  const canWrite = bootstrap.can_write !== false
  return <WorkspaceContextCard title="Brain" summary="Shared knowledge for your agents.">
    <Section title="Knowledge"><Row icon={BookOpen} active={view === 'library'} onClick={() => onOpen('library')}>Library</Row></Section>
    {canWrite ? <>
      <Section title="Access & connections">
        <Row icon={ShieldCheck} active={view === 'access'} hint={folder || 'Organization root'} onClick={() => onOpen('access')}>Folder access</Row>
        <Row icon={MessageCircle} hint="Open the existing assistant without sending a message" onClick={onChat}>Access chat</Row>
        <Row icon={Plug} onClick={onConnect}>Connect AI tools</Row>
      </Section>
      <Section title="Manage">
        <Row icon={CalendarClock} active={view === 'schedules'} onClick={() => onOpen('schedules')}>Schedules</Row>
        <Row icon={KeyRound} active={view === 'secrets'} onClick={() => onOpen('secrets')}>Secrets</Row>
        <Row icon={BrainCircuit} active={view === 'models'} onClick={() => onOpen('models')}>Models</Row>
      </Section>
      <div className="border-t border-border pt-3">
        {bootstrap.is_admin ? <Row icon={Archive} detail={bootstrap.backup_configured ? 'Configured' : undefined} onClick={onChat}>Backup</Row>
          : bootstrap.backup_configured ? <p className="px-1 text-[11px] leading-5 text-muted-foreground">Backup configured.</p> : null}
      </div>
    </> : <p className="px-1 text-xs leading-5 text-muted-foreground">You can open files shared with you.</p>}
  </WorkspaceContextCard>
}
