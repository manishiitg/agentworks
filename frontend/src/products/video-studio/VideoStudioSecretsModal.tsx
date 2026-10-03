import { PlugZap, X } from 'lucide-react'
import { SecretSelectionSection } from '../../components/secrets/SecretSelectionSection'
import { updateProductProjectSelections } from '../../platform/chat/productProjects'
import { useChatStore } from '../../stores/useChatStore'
import type { VideoProject } from './videoStudioData'

// Project secrets for a Video Studio project, managed exactly like a
// workflow: project-scoped secrets plus globals. Personal secrets are gone;
// the open chat tab's attachment list is re-synced to the project names so
// tab-level and manifest-level injection agree.
export function VideoStudioSecretsModal({ project, tabId, onProjectChange, onClose }: {
  project: VideoProject
  tabId: string | null
  onProjectChange: (project: VideoProject) => void
  onClose: () => void
}) {
  const persist = async (patch: { selectedSecrets?: string[]; selectedGlobalSecrets?: string[] }) => {
    const updated = await updateProductProjectSelections(project, patch, `Update Video Studio project secrets ${project.title}`)
    onProjectChange({ ...updated, videos: project.videos })
    if (tabId) useChatStore.getState().setTabConfig(tabId, { selectedSecrets: updated.selectedSecrets })
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black bg-opacity-50 p-4" onClick={onClose}>
      <div
        className="flex max-h-[85vh] w-full max-w-3xl flex-col overflow-hidden rounded-lg border border-border bg-card p-6 shadow-xl"
        onClick={event => event.stopPropagation()}
        role="dialog"
        aria-label="Integrations"
      >
        <div className="mb-4 flex shrink-0 items-center justify-between">
          <div className="flex items-center gap-2">
            <PlugZap className="h-5 w-5 text-primary" />
            <h3 className="text-lg font-semibold text-foreground">Integrations · Secrets</h3>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="text-muted-foreground hover:text-foreground"
            aria-label="Close integrations"
          >
            <X className="h-5 w-5" />
          </button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">
          <SecretSelectionSection
            selectedSecrets={project.selectedSecrets}
            onSecretChange={secrets => persist({ selectedSecrets: secrets })}
            selectedGlobalSecrets={project.selectedGlobalSecrets}
            onGlobalSecretChange={secrets => persist({ selectedGlobalSecrets: secrets || [] })}
            persistExplicitGlobalSelection
            workflowPath={project.workspacePath}
            workspaceNoun="project"
            workspaceSecretHeading="Project secrets"
            showGlobalSecrets
            allowGlobalPromotion
          />
        </div>
      </div>
    </div>
  )
}
