// The one seam between the SparkQuill UI and whatever serves it.
//
// Every backend call the app makes goes through this interface; the one
// implementation is the AgentWorks platform (platformApi.ts). Turns themselves
// run through the shared ChatArea, so there is no chat transport here.
import type { ApiEngine, Activity, QuickCommand, StoredMsg, TreeNode, VoiceStatus } from '../stores/types'

export type SetupState = {
  engine?: string
  /** @deprecated legacy shared field, no longer written — see parent_model/child_model. */
  model?: string
  parent_model?: string
  child_model?: string
  connection_id?: string
  parent_reasoning_effort?: string
  child_reasoning_effort?: string
  child?: { name?: string; grade?: string; board?: string } | null
  pin_set?: boolean
  setup_complete?: boolean
  next_step?: string
  parent_label?: string
}


export type FileContent = { path?: string; is_text?: boolean; content?: string; size?: number }
export type TreeResponse = TreeNode[] | { nodes?: TreeNode[]; total_size?: number }
export type UploadResult = { name?: string; path?: string; error?: string }


export type ModelInfo = { provider: string; selected: string; default: string; models: { id: string; label: string }[] }
export type FastMode = { enabled: boolean; child_enabled: boolean }

export type WhatsAppVoiceTranscription = { enabled: boolean; installed: boolean; installing: boolean; model_size_mb: number; available: boolean; error?: string }
export type WhatsAppStatus = {
  accounts: { jid: string; connected: boolean }[]
  pairing: { qr_available: boolean; qr_expires_at?: string }
  voice_transcription?: WhatsAppVoiceTranscription
}

export type PulseConfig = {
  enabled: boolean
  cadence_hours: number
  last_run_at?: string
  watch_sites?: string[]
  preferred_hour: number
  preferred_hour_set: boolean
  // The isolated check-in conversation's session id (product.yaml
  // schedules[].isolated) — its own conversation, never the parent's own
  // chat. Absent until the first check-in has actually run once.
  last_session_id?: string
}
export type PulseConfigPatch = Partial<Pick<PulseConfig, 'enabled' | 'cadence_hours' | 'watch_sites' | 'preferred_hour' | 'preferred_hour_set'>>

export type StoredConversation = { messages?: StoredMsg[] }

/** One of the parent's conversations with Quill, as the chats rail lists them. */
export type ParentChat = {
  session_id: string
  title: string
  created_at?: string
  updated_at?: string
  message_count?: number
  current: boolean
}

export interface FamilyApi {
  /** Where the backend lives; shown in the "can't reach" message. */
  readonly baseUrl: string

  // ---- setup ------------------------------------------------------------
  setup(): Promise<SetupState>
  engines(): Promise<ApiEngine[]>
  validateEngine(provider: string): Promise<{ valid: boolean; message?: string }>
  /** role picks whether model is saved as this family's parent_model or child_model; engine itself is shared. */
  selectEngine(role: 'parent' | 'child', engine: string, model?: string, connectionId?: string, reasoningEffort?: string): Promise<void>
  saveChild(child: { name: string; grade: string; board: string }): Promise<void>
  setPin(pin: string): Promise<{ error?: string }>
  verifyPin(pin: string): Promise<{ ok?: boolean }>

  // ---- the parent's chats with Quill ----------------------------------------
  /** The live chat first, then earlier ones newest first. */
  listParentChats(): Promise<ParentChat[]>
  /** Forgets an earlier chat and deletes its transcript; the live one is refused. */
  deleteParentChat(sessionId: string): Promise<void>

  // ---- child conversation --------------------------------------------------
  childActivity(): Promise<Activity | null>
  handoff(dir: string, resume: boolean): Promise<{ new_session?: boolean; dir?: string; goal?: string }>
  /** Forgets the child's conversation for an activity, so the next turn starts a new one ("Start fresh"). */
  resetChildConversation(activityDir: string): Promise<void>
  loadChildConversation(activityDir: string): Promise<StoredConversation | null>

  // ---- workspace -----------------------------------------------------------
  tree(): Promise<TreeResponse>
  readFile(path: string): Promise<FileContent>
  /** URL a browser element can load the file's bytes from. */
  rawUrl(path: string, opts?: { download?: boolean; print?: boolean }): string
  /** URL of a static asset the server ships (jsxgraph, ...). */
  assetUrl(relPath: string): string
  upload(file: File, scope: 'parent' | 'child'): Promise<UploadResult>
  saveState(key: string, data: unknown): Promise<void>
  loadState(key: string): Promise<unknown>
  activities(): Promise<Activity[]>
  /** Permanently removes these activities (folder, files, attempts). No undo. */
  deleteActivities(dirs: string[]): Promise<void>

  /** Make sure this app is logged in (platform); a no-op on the standalone server. */
  ensureSession(): Promise<void>

  /** The composer's quick menus for both modes (the product's `commands:`). */
  commands(): Promise<{ parent: QuickCommand[]; child: QuickCommand[] }>

  // ---- settings ------------------------------------------------------------
  models(): Promise<ModelInfo | null>
  saveModel(modelId: string): Promise<void>
  fastMode(): Promise<FastMode>
  saveFastMode(patch: { enabled: boolean; child_enabled?: boolean }): Promise<void>
  secrets(): Promise<string[]>
  saveSecret(name: string, value: string): Promise<string[]>
  deleteSecret(name: string): Promise<string[]>
  voiceStatus(): Promise<VoiceStatus>
  browserStatus(): Promise<{ cli_installed: boolean }>

  // ---- connectors & pulse --------------------------------------------------
  whatsappStatus(): Promise<WhatsAppStatus>
  whatsappPairImageUrl(nonce: number): string
  whatsappUnpair(jid: string): Promise<void>
  whatsappVoice(enabled: boolean): Promise<WhatsAppVoiceTranscription>
  pulseConfig(): Promise<PulseConfig>
  savePulseConfig(patch: PulseConfigPatch): Promise<PulseConfig>
  /** Clears the isolated check-in conversation; the next check-in opens a fresh one. */
  resetCheckinHistory(): Promise<void>
  runPulse(): Promise<{ ok: boolean; error?: string }>
}
