import { Cpu } from 'lucide-react'
import { useLLMStore } from '../../stores'
import { useAppStore } from '../../stores/useAppStore'
import { Tooltip, TooltipContent, TooltipTrigger } from '../ui/tooltip'

export default function ProvidersControl() {
  const isOpen = useLLMStore(state => state.showLLMModal)
  const open = useLLMStore(state => state.setShowLLMModal)

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          onClick={() => { useAppStore.getState().setAdminPage(null); open(true) }}
          data-tour="global-providers"
          aria-label="Providers"
          aria-pressed={isOpen}
          className={`rounded-md p-1 transition-colors ${
            isOpen
              ? 'bg-violet-50 text-violet-700 dark:bg-violet-500/20 dark:text-violet-200'
              : 'text-gray-500 hover:bg-gray-100 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-gray-200'
          }`}
        >
          <Cpu className="h-4 w-4" />
        </button>
      </TooltipTrigger>
      <TooltipContent side="bottom">Providers</TooltipContent>
    </Tooltip>
  )
}
