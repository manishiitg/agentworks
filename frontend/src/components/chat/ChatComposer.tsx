import { forwardRef, type HTMLAttributes, type ReactNode, type FormEventHandler } from 'react'
import { Send } from 'lucide-react'
import { Button, type ButtonProps } from '../ui/Button'
import { Textarea } from '../ui/Textarea'
import { cn } from '../../lib/utils'

/** Shared composer presentation. Crew and Code use the default variant. */
export const PRODUCT_COMPOSER_BAND_CLASS = 'border-t border-border bg-background py-1.5 shadow-[0_-8px_24px_rgba(15,23,42,0.08)]'
export const PRODUCT_COMPOSER_CARD_CLASS = 'space-y-0.5 rounded-2xl border border-border bg-card px-1.5 py-0.5 shadow-sm transition-colors duration-150 focus-within:border-ring'

export function ChatComposerBand({ product = false, className, ...props }: HTMLAttributes<HTMLDivElement> & { product?: boolean }) {
  return <div
    data-product-chat-input={product || undefined}
    className={cn(product ? PRODUCT_COMPOSER_BAND_CLASS : 'space-y-1', className)}
    {...props}
  />
}

export function ChatComposerArea({ product = false, className, ...props }: HTMLAttributes<HTMLDivElement> & { product?: boolean }) {
  return <div
    data-tour="chat-input-area"
    data-testid="tour-chat-input-area"
    className={cn('px-3', product ? 'py-1.5' : 'pt-1 pb-2', className)}
    {...props}
  />
}

export function ChatComposerCard({ product = false, className, ...props }: HTMLAttributes<HTMLDivElement> & { product?: boolean }) {
  return <div
    className={cn(product
      ? PRODUCT_COMPOSER_CARD_CLASS
      : 'space-y-1 rounded-xl border border-slate-700/80 bg-[#101513] p-1.5 shadow-sm transition focus-within:border-slate-500', className)}
    {...props}
  />
}

export const ChatComposerTextarea = forwardRef<HTMLTextAreaElement, React.ComponentProps<'textarea'> & { product?: boolean }>(
  ({ product = false, className, ...props }, ref) => <Textarea
    ref={ref}
    rows={1}
    className={cn(product ? '!min-h-[32px] max-h-[100px] !border-0 !bg-transparent !px-1.5 !py-1 text-sm text-foreground !shadow-none focus-visible:!ring-0 placeholder:text-sm placeholder:text-muted-foreground resize-none overflow-y-auto leading-[1.3]' : '!min-h-[36px] max-h-[100px] !border-0 !bg-transparent !py-1.5 !px-2 text-xs !shadow-none focus-visible:!ring-0 placeholder:text-xs resize-none overflow-y-auto leading-[1.3]', className)}
    {...props}
  />,
)
ChatComposerTextarea.displayName = 'ChatComposerTextarea'

export const ChatComposerSendButton = forwardRef<HTMLButtonElement, ButtonProps & { product?: boolean }>(
  ({ product = false, className, children, ...props }, ref) => <Button
    ref={ref}
    size="icon"
    className={cn('h-7 w-7 p-0', product && 'bg-violet-600 text-white hover:bg-violet-500', className)}
    {...props}
  >{children ?? <Send className="h-3.5 w-3.5" />}</Button>,
)
ChatComposerSendButton.displayName = 'ChatComposerSendButton'

export function ChatComposerControls({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn('flex justify-between items-center', className)} {...props} />
}

/** Keep the default 40px floor and avoid a layout write on single-line typing. */
export function resizeChatComposerTextarea(textarea: HTMLTextAreaElement, product = false): void {
  const floor = product ? 36 : 40
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
