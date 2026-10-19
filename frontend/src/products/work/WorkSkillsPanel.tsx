import { useRef } from 'react'
import SkillsManagerPanel from '../../components/skills/SkillsManagerPanel'
import { useChatStore } from '../../stores/useChatStore'
import { useProjectProduct } from './projectProduct'

/** The existing project skill selection and uninstall path, now under Ops. */
export function WorkSkillsPanel({ workspacePath, projectId, tabId, onSelectedSkillsChange }: {
  workspacePath: string
  projectId: string
  tabId: string
  onSelectedSkillsChange: (skills: string[]) => Promise<unknown>
}) {
  const product = useProjectProduct()
  const selectedSkills = useChatStore(state => state.chatTabs[tabId]?.config.selectedSkills || [])
  const pendingSkillSaves = useRef<Promise<void>>(Promise.resolve())
  const toggleSkill = async (folderName: string) => {
    const next = selectedSkills.includes(folderName)
      ? selectedSkills.filter((name) => name !== folderName)
      : [...selectedSkills, folderName]
    const store = useChatStore.getState()
    const save = onSelectedSkillsChange(next)
    pendingSkillSaves.current = Promise.all([pendingSkillSaves.current, save]).then(() => undefined, () => undefined)
    try {
      await save
    } catch (cause) {
      store.addToast(cause instanceof Error ? cause.message : 'Could not save project skills.', 'error')
      return
    }
    for (const tab of Object.values(store.chatTabs)) {
      if (tab.metadata?.agentProfileId !== product.profileId || tab.metadata?.agentProfileProjectId !== projectId) continue
      store.setTabConfig(tab.tabId, { selectedSkills: next })
      store.setTabMetadata(tab.tabId, { agentProfileRuntimeDirty: true })
    }
  }

  return <SkillsManagerPanel
          compact
          manageOwnScroll={false}
          workspacePath={workspacePath}
          selectedSkills={selectedSkills}
          onBeforeUninstall={() => pendingSkillSaves.current}
          onUninstalled={async name => {
            const currentSelection = useChatStore.getState().chatTabs[tabId]?.config.selectedSkills || []
            await onSelectedSkillsChange(currentSelection.filter(skill => skill !== name))
            const store = useChatStore.getState()
            for (const tab of Object.values(store.chatTabs)) {
              if (tab.metadata?.agentProfileId !== product.profileId || tab.metadata?.agentProfileProjectId !== projectId) continue
              store.setTabConfig(tab.tabId, { selectedSkills: (tab.config.selectedSkills || []).filter(skill => skill !== name) })
              store.setTabMetadata(tab.tabId, { agentProfileRuntimeDirty: true })
            }
          }}
          onToggleSkill={folderName => { void toggleSkill(folderName) }}
          selectionScopeLabel="main chat"
          emptySelectionText={`No skills are installed in this ${product.noun} yet. Ask the agent to add or create one.`}
        />
}
