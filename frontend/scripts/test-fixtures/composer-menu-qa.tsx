import { createRoot } from 'react-dom/client'
import '../../src/index.css'
import '../../src/App.css'
import { ComposerActionsMenu } from '../../src/components/chat/ComposerActionsMenu'
import { useAuthStore } from '../../src/stores/useAuthStore'

// Only menu layout: no chat, model turn, account connection or server mutation.
useAuthStore.setState({ user: { id: 'example-person' } as never })
const noop = () => {}
createRoot(document.getElementById('root')!).render(<div className="fixed bottom-4 left-4">
  <ComposerActionsMenu uploading={false} onAttach={noop} onOpenCommands={noop} onOpenLiveView={noop} showAgents
    sendOptions={{ mode: 'steer', busy: true, onModeChange: noop, onNewChat: noop, onStop: noop }} />
</div>)
