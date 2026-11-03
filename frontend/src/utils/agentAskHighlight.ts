import { activateTab } from './activateTab'

/** Opens a tab at the last message from this sender (marked with their name in the chat) and briefly highlights it:
 * the owner sees what someone else asked their shared agent (owner, 3 Nov). */
export function openAgentAsk(tabId: string, senderLabel: string) {
  activateTab(tabId)
  const started = Date.now()
  const find = () => {
    const marked = [...document.querySelectorAll<HTMLElement>('[data-sender-label]')].filter(node => node.dataset.senderLabel === senderLabel && node.offsetParent !== null)
    const target = marked[marked.length - 1]
    if (!target) {
      if (Date.now() - started < 4000) window.setTimeout(find, 200)
      return
    }
    target.scrollIntoView({ block: 'center', behavior: 'smooth' })
    target.classList.add('ring-2', 'ring-amber-500/60', 'rounded-2xl', 'transition-shadow')
    window.setTimeout(() => target.classList.remove('ring-2', 'ring-amber-500/60'), 2500)
  }
  window.setTimeout(find, 150)
}
