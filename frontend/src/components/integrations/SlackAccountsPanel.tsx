import PersonalAccountsPanel from './PersonalAccountsPanel'

export default function SlackAccountsPanel({ workspacePath }: { workspacePath: string }) {
  return <PersonalAccountsPanel provider="slack" workspacePath={workspacePath} />
}
