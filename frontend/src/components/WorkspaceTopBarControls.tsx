import { TooltipProvider } from './ui/tooltip'
import { useProductNavigationSidebar } from './workspace/ProductTopBar'
import AccountControl from './topbar/AccountControl'

interface WorkspaceTopBarControlsProps {
  onOpenWalkthrough?: () => void
  onOpenShortcuts?: () => void
}

export default function WorkspaceTopBarControls(props: WorkspaceTopBarControlsProps) {
  const sidebar = useProductNavigationSidebar()
  return (
    <TooltipProvider delayDuration={400}>
      <div className={sidebar ? 'flex flex-col items-stretch gap-2' : 'flex items-center gap-1.5'}>
        <AccountControl {...props} />
      </div>
    </TooltipProvider>
  )
}
