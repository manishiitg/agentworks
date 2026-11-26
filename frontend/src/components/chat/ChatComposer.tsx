import { forwardRef, useEffect, useState, type HTMLAttributes, type ReactNode, type FormEventHandler } from 'react'
import { FileText, Paperclip, Send, X } from 'lucide-react'
import { LoadingIndicator } from '../ui/LoadingIndicator'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '../ui/tooltip'
import { Button, type ButtonProps } from '../ui/Button'
import { Textarea } from '../ui/Textarea'
import { cn } from '../../lib/utils'

/** Shared composer presentation. Crew and Code use the default variant. */
export const PRODUCT_COMPOSER_BAND_CLASS = 'border-t border-border bg-background py-1.5 shadow-[0_-8px_24px_rgba(15,23,42,0.08)]'
export const PRODUCT_COMPOSER_CARD_CLASS = 'space-y-0.5 rounded-2xl border border-border bg-surface-composer px-1.5 py-0.5 shadow-sm transition-colors duration-150 focus-within:border-ring'

export function ChatComposerBand({ product = false, className, ...props }: HTMLAttributes<HTMLDivElement> & { product?: boolean }) {
  return <div
    data-theme-surface="composer-band"
    data-product-chat-input={product || undefined}
    className={cn(product ? PRODUCT_COMPOSER_BAND_CLASS : 'space-y-1', className)}
    {...props}
  />
}

export function ChatComposerArea({ product = false, className, ...props }: HTMLAttributes<HTMLDivElement> & { product?: boolean }) {
  return <div
    data-tour="chat-input-area"
    data-testid="tour-chat-input-area"
    className={cn('min-w-0 px-2 sm:px-3 pb-[env(safe-area-inset-bottom)]', product ? 'py-1.5' : 'pt-1 pb-2', 'max-md:pb-[calc(env(safe-area-inset-bottom)+0.5rem)]', className)}
    {...props}
  />
}

export function ChatComposerCard({ product = false, className, ...props }: HTMLAttributes<HTMLDivElement> & { product?: boolean }) {
  return <div
    data-theme-surface="composer"
    data-chat-composer-card
    className={cn(product
      ? PRODUCT_COMPOSER_CARD_CLASS
      : 'space-y-1 rounded-xl border border-border bg-surface-composer p-1.5 shadow-sm transition focus-within:border-ring', className)}
    {...props}
  />
}

export const ChatComposerTextarea = forwardRef<HTMLTextAreaElement, React.ComponentProps<'textarea'> & { product?: boolean }>(
  ({ product = false, className, ...props }, ref) => <Textarea
    ref={ref}
    data-theme-control="composer-input"
    rows={1}
    // A message box, not a form field: no AutoFill bar (keys, cards, places) above the phone keyboard.
    autoComplete="off"
    autoCorrect="on"
    enterKeyHint="send"
    className={cn('!text-base placeholder:!text-base md:!text-sm md:placeholder:!text-sm', product ? '!min-h-[32px] max-h-[100px] !border-0 !bg-transparent !px-1.5 !py-1 text-sm text-foreground !shadow-none focus-visible:!ring-0 placeholder:text-sm placeholder:text-muted-foreground resize-none overflow-y-auto leading-[1.3]' : '!min-h-[36px] max-h-[100px] !border-0 !bg-transparent !py-1.5 !px-2 text-xs !shadow-none focus-visible:!ring-0 placeholder:text-xs resize-none overflow-y-auto leading-[1.3]', className)}
    {...props}
  />,
)
ChatComposerTextarea.displayName = 'ChatComposerTextarea'

export const ChatComposerSendButton = forwardRef<HTMLButtonElement, ButtonProps & { product?: boolean }>(
  ({ product = false, className, children, ...props }, ref) => <Button
    ref={ref}
    data-theme-action="send"
    size="icon"
    className={cn('h-7 w-7 p-0', product && 'bg-violet-600 text-white hover:bg-violet-500', className)}
    {...props}
  >{children ?? <Send className="h-3.5 w-3.5" />}</Button>,
)
ChatComposerSendButton.displayName = 'ChatComposerSendButton'

export function ChatComposerControls({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div data-theme-composer-controls="" className={cn('flex min-w-0 flex-wrap justify-between items-center gap-y-1 [&_button]:min-h-7 [&_button]:min-w-7 [&_button>svg]:!h-3 [&_button>svg]:!w-3', className)} {...props} />
}

/** Keep the default 36px floor and avoid a layout write on single-line typing. */
export function resizeChatComposerTextarea(textarea: HTMLTextAreaElement, product = false): void {
  // One line of text plus the textarea's own padding: no spare blank line under an empty box.
  const floor = product ? 32 : 36
  if (textarea.style.height === `${floor}px` && textarea.scrollHeight <= textarea.clientHeight) return
  textarea.style.height = 'auto'
  const next = `${Math.min(Math.max(textarea.scrollHeight, floor), 100)}px`
  if (textarea.style.height !== next) textarea.style.height = next
}

/** One form/card/textarea layout shared by the full ChatInput and API adapters. */
export function ChatComposerForm({ product = false, className, onSubmit, aboveCard, children }: {
  product?: boolean
  className?: string
  onSubmit: FormEventHandler<HTMLFormElement>
  aboveCard?: ReactNode
  children: ReactNode
}) {
  return <form onSubmit={onSubmit} className={cn(product ? 'relative' : 'relative space-y-1', className)}>
    {aboveCard}
    <ChatComposerCard product={product}>{children}</ChatComposerCard>
  </form>
}

/** The message box's paperclip, shared by the chat input and the Ask box under a teammate's agent. */
export function ChatComposerAttachButton({ busy = false, disabled = false, onClick, tip }: { busy?: boolean; disabled?: boolean; onClick: () => void; tip: string }) {
  return <TooltipProvider><Tooltip>
    <TooltipTrigger asChild>
      <Button type="button" variant="outline" size="icon" disabled={disabled || busy} onClick={onClick} className="h-7 w-7 p-0"
        data-theme-action="attach" data-testid="chat-upload-button" aria-label="Attach files">
        {busy ? <LoadingIndicator className="w-3.5 h-3.5" /> : <Paperclip className="w-3.5 h-3.5" />}
      </Button>
    </TooltipTrigger>
    <TooltipContent><p>{tip}</p></TooltipContent>
  </Tooltip></TooltipProvider>
}

/** Local previews of files in the message box: an image thumbnail or a file icon, with the name; ✕ removes one. */
export function ChatComposerAttachmentTiles({ files, onRemove, dim = false }: { files: File[]; onRemove?: (index: number) => void; dim?: boolean }) {
  const [previews, setPreviews] = useState<(string | undefined)[]>([])
  useEffect(() => {
    const urls = files.map(file => file.type.startsWith('image/') ? URL.createObjectURL(file) : undefined)
    setPreviews(urls)
    return () => { urls.forEach(url => { if (url) URL.revokeObjectURL(url) }) }
  }, [files])
  return <div className="flex gap-2 overflow-x-auto pt-1">
    {files.map((file, index) => <div key={`${file.name}-${index}`} title={file.name}
      className="relative flex h-24 w-24 shrink-0 items-center justify-center overflow-hidden rounded-xl border border-border bg-muted">
      {previews[index] ? <img src={previews[index]} alt={file.name} className={cn('h-full w-full object-cover', dim && 'opacity-60')} /> : <FileText aria-hidden="true" className="h-6 w-6 text-muted-foreground" />}
      <span className="absolute inset-x-0 bottom-0 truncate bg-background/95 px-1.5 py-1 text-[10px] text-foreground">{file.name}</span>
      {onRemove && <button type="button" aria-label={`Remove ${file.name}`} onClick={() => onRemove(index)}
        className="absolute right-1 top-1 grid h-5 w-5 place-items-center rounded-full border border-border bg-background/95 text-foreground hover:bg-muted"><X className="h-3 w-3" aria-hidden="true" /></button>}
    </div>)}
  </div>
}
