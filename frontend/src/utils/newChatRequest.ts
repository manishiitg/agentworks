/**
 * "New chat" from outside the composer (the Context section, the model menu): the chat area that shows this
 * session runs its own New chat, the same one as the send menu. The tab starts a new conversation and the
 * current one moves to its earlier chats; nothing is emptied or lost.
 */
export const newChatRequestEvent = 'agentworks:new-chat-request'

export function requestNewChat(sessionId: string) {
  window.dispatchEvent(new CustomEvent(newChatRequestEvent, { detail: { sessionId } }))
}
