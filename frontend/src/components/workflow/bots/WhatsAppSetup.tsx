import { AlertCircle, AlertTriangle, CheckCircle2, List, MessageCircle, Send, Loader2, RotateCcw, Trash2 } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Button } from '../../ui/Button'
import { Card } from '../../ui/Card'
import type { WorkflowBots } from './useWorkflowBots'
import { StatusBanner } from './StatusBanner'

// ── Drill-in: WhatsApp setup ──────────────────────────────────────────────

type WhatsAppSetupBots = Pick<WorkflowBots,
  | 'canManageOwnWhatsAppDevices'
  | 'waStatus' | 'waError'
  | 'qrImageURL' | 'qrLoading' | 'qrError'
  | 'waUnpairConfirmSlot' | 'waUnpairingSlot' | 'handleUnpairWhatsAppDevice'
  | 'refreshWaQR'
>

function formatQRExpiry(expiresAt: string | undefined, nowMs: number): string | null {
  if (!expiresAt) return null
  const expiresMs = new Date(expiresAt).getTime()
  if (!Number.isFinite(expiresMs)) return null
  const remainingSeconds = Math.max(0, Math.ceil((expiresMs - nowMs) / 1000))
  if (remainingSeconds <= 0) return 'QR expired. Refreshing...'
  return `QR expires in ${remainingSeconds}s.`
}

export function WhatsAppSetup({ bots }: { bots: WhatsAppSetupBots }) {
  const {
    canManageOwnWhatsAppDevices,
    waStatus, waError, qrImageURL, qrLoading, qrError,
    waUnpairConfirmSlot, waUnpairingSlot, handleUnpairWhatsAppDevice,
    refreshWaQR,
  } = bots
  const [nowMs, setNowMs] = useState(() => Date.now())

  // One person, one WhatsApp: the account links a single phone.
  const phone = waStatus
    ? (waStatus.devices?.[0] ?? { slot: '', paired: waStatus.paired, connected: waStatus.connected, own_jid: waStatus.own_jid, qr_available: waStatus.qr_available })
    : null
  const hasLinkedDevice = !!phone?.paired
  const hasConnectedDevice = hasLinkedDevice && !!phone?.connected
  const showPairCard = !!waStatus?.enabled && !hasLinkedDevice
  const qrAvailable = !!waStatus?.qr_available
  const qrExpiresAt = waStatus?.qr_expires_at
  const qrExpiryLabel = formatQRExpiry(qrExpiresAt, nowMs)

  useEffect(() => {
    if (!qrExpiresAt) return
    setNowMs(Date.now())
    const timer = window.setInterval(() => setNowMs(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [qrExpiresAt])

  return (
    <div className="space-y-4">
      {waError && <StatusBanner tone="error">{waError}</StatusBanner>}

      {/* Connector disabled at server startup */}
      {waStatus && !waStatus.enabled && (
        <Card className="p-4">
          <div className="flex items-start gap-3">
            <AlertTriangle className="w-5 h-5 text-amber-500 flex-shrink-0 mt-0.5" />
            <div className="space-y-1">
              <h3 className="text-sm font-medium text-foreground">WhatsApp connector is disabled</h3>
              <p className="text-xs text-muted-foreground">
                Remove <code className="px-1 py-0.5 bg-muted rounded">WHATSAPP_ENABLED=false</code> from
                the server's <code className="px-1 py-0.5 bg-muted rounded">.env</code> and restart the
                agent. The connector is enabled by default, and the per-user session directory can be
                overridden via <code className="px-1 py-0.5 bg-muted rounded">WHATSAPP_SESSION_DIR</code>.
              </p>
            </div>
          </div>
        </Card>
      )}

      {/* Status card */}
      {waStatus && waStatus.enabled && (
        <Card className="p-4">
          <div className="flex items-start justify-between gap-4">
            <div>
              <h3 className="text-sm font-medium text-foreground">Connection</h3>
              <p className="text-xs text-muted-foreground mt-0.5">Account-wide pairing shared by all projects and workflows.</p>
              {(waStatus.owner_email || waStatus.owner_username || waStatus.owner_user_id) && (
                <p className="mt-1 text-[11px] text-muted-foreground">
                  bound to <span className="text-foreground">{waStatus.owner_email || waStatus.owner_username || waStatus.owner_user_id}</span>
                </p>
              )}
            </div>
            <div className="flex flex-col items-end gap-1 text-xs">
              <span className="inline-flex items-center gap-2">
                <span className={`h-2 w-2 rounded-full ${hasConnectedDevice ? 'bg-green-500' : hasLinkedDevice ? 'bg-amber-500' : 'bg-gray-400'}`} />
                <span className="text-foreground">
                  {hasConnectedDevice ? 'Connected' : hasLinkedDevice ? 'Paired, offline' : 'Not paired'}
                </span>
              </span>
              <span className="text-[11px] text-muted-foreground">
                {waStatus.enabled ? 'Enabled' : 'Disabled'} · {hasLinkedDevice ? 'Linked' : 'Unpaired'}
              </span>
            </div>
          </div>
          {!hasLinkedDevice && (
            <p className="mt-3 text-xs text-muted-foreground">
              Pair once via QR: <strong>Android</strong> ⋮ menu → Linked Devices → Link a device · <strong>iPhone</strong> Settings → Linked Devices → Link Device.
            </p>
          )}
        </Card>
      )}

      {/* The linked number */}
      {waStatus && waStatus.enabled && phone && hasLinkedDevice && (() => {
        const statusDot = phone.connected ? 'bg-green-500' : 'bg-amber-500'
        const statusText = phone.connected ? 'Connected' : 'Paired, offline'
        const confirming = waUnpairConfirmSlot === phone.slot
        const busy = waUnpairingSlot === phone.slot
        return (
          <Card className="p-4">
            <div>
              <h3 className="text-sm font-medium text-foreground">Linked number</h3>
              <p className="mt-0.5 text-xs text-muted-foreground">
                One WhatsApp per account. To use another number, unpair this one first.
              </p>
            </div>
            <div className="mt-3 flex items-center justify-between gap-3 rounded-md border border-border bg-muted/20 px-3 py-2">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className={`h-2 w-2 rounded-full ${statusDot}`} />
                  <div className="text-[11px] text-muted-foreground">{statusText}</div>
                </div>
                {phone.own_jid && (
                  <div className="mt-1 text-[11px] text-muted-foreground font-mono truncate">{phone.own_jid}</div>
                )}
              </div>
              <Button
                onClick={() => handleUnpairWhatsAppDevice(phone.slot)}
                disabled={!canManageOwnWhatsAppDevices || !!waUnpairingSlot}
                variant={confirming ? 'destructive' : 'outline'}
                size="sm"
                className="flex-shrink-0 whitespace-nowrap"
              >
                {busy ? (
                  <><Loader2 className="w-3.5 h-3.5 animate-spin mr-1.5" /> Unpairing…</>
                ) : confirming ? (
                  <><Trash2 className="w-3.5 h-3.5 mr-1.5" /> Confirm</>
                ) : (
                  'Unpair'
                )}
              </Button>
            </div>
          </Card>
        )
      })()}

      {/* QR pairing card — shown while the account's phone is unpaired */}
      {waStatus && waStatus.enabled && showPairCard && (
        <Card className="p-4">
          <div className="flex flex-col items-center gap-3">
            <div className="flex w-full items-center justify-between gap-3">
              <div>
                <h3 className="text-sm font-medium text-foreground">Pair device</h3>
                <p className="mt-0.5 text-xs text-muted-foreground">Scan this QR from WhatsApp → Linked Devices.</p>
              </div>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={refreshWaQR}
                disabled={qrLoading}
                aria-label="Refresh WhatsApp QR"
              >
                <RotateCcw className="h-3.5 w-3.5" />
                Refresh
              </Button>
            </div>
            {qrAvailable ? (
              <>
                {qrImageURL ? (
                  <img
                    src={qrImageURL}
                    alt="WhatsApp pairing QR"
                    width={256}
                    height={256}
                    className="rounded border border-border bg-white p-2"
                  />
                ) : (
                  <div className="flex h-64 w-64 items-center justify-center rounded border border-border bg-muted/30 p-4 text-center">
                    {qrLoading ? (
                      <div className="flex items-center gap-2 text-sm text-muted-foreground">
                        <Loader2 className="w-4 h-4 animate-spin" />
                        Loading QR…
                      </div>
                    ) : qrError ? (
                      <div className="flex flex-col items-center gap-2 text-sm text-red-700 dark:text-red-300">
                        <AlertCircle className="w-5 h-5" />
                        <span>{qrError}</span>
                      </div>
                    ) : (
                      <span className="text-sm text-muted-foreground">QR not available yet.</span>
                    )}
                  </div>
                )}
                {qrExpiryLabel && (
                  <p className="text-[11px] text-muted-foreground/80 text-center">{qrExpiryLabel}</p>
                )}
                {(waStatus.pairing_error || waStatus.pairing_message) && (
                  <div className="w-full space-y-2">
                    {waStatus.pairing_error && <StatusBanner tone="error">{waStatus.pairing_error}</StatusBanner>}
                    {waStatus.pairing_message && (
                      <div className="rounded-md border border-border bg-muted/30 p-3 text-[11px] text-muted-foreground">
                        {waStatus.pairing_message}
                        {waStatus.pairing_last_at && (
                          <div className="mt-1.5 text-muted-foreground/80">
                            Last attempt: {new Date(waStatus.pairing_last_at).toLocaleString()}
                          </div>
                        )}
                      </div>
                    )}
                  </div>
                )}
              </>
            ) : (
              <div className="flex items-center gap-2 text-sm text-muted-foreground py-8">
                <Loader2 className="w-4 h-4 animate-spin" />
                Waiting for the server to generate a QR…
              </div>
            )}
          </div>
        </Card>
      )}

      {waStatus?.enabled && waStatus.paired && (
        <Card className="p-4">
          <h3 className="flex items-center gap-2 text-sm font-medium text-foreground"><CheckCircle2 className="h-4 w-4 text-emerald-500" aria-hidden="true" />Ready to chat</h3>
          <ol className="mt-4 grid gap-3 sm:grid-cols-3">
            {[
              { icon: MessageCircle, title: '1. Message yourself', text: 'Open your own chat in WhatsApp on the linked number.' },
              { icon: List, title: '2. Choose an agent', text: 'Send @list and choose an agent from the options shown.' },
              { icon: Send, title: '3. Start chatting', text: 'Send your message. Your choice is remembered for the next message.' },
            ].map(step => <li key={step.title} className="rounded-lg border border-border bg-muted/30 p-3">
              <step.icon className="mb-3 h-5 w-5 text-primary" aria-hidden="true" />
              <p className="text-xs font-medium text-foreground">{step.title}</p>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">{step.text}</p>
            </li>)}
          </ol>
          <p className="mt-3 text-xs leading-5 text-muted-foreground">Already chatting? Keep going. Send @list whenever you want to choose another agent. No DM link code or custom route is needed for your own chat.</p>
          <p className="mt-2 text-xs leading-5 text-muted-foreground">AgentWorks connects as a linked device. WhatsApp may show a generic device name such as “Other device”. Unpair above to disconnect it.</p>
        </Card>
      )}

      {/* Loading placeholder */}
      {!waStatus && !waError && (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="w-8 h-8 animate-spin text-primary" />
        </div>
      )}
    </div>
  )
}
