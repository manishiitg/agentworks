// Notifications for this browser (owner, 5 Nov). The server decides what is sent; this only shows it. Tapping opens
// the chat. An app tab that is open but not in front plays the short sound when this device has it on.
self.addEventListener('push', event => {
  let msg = {}
  try { msg = event.data ? event.data.json() : {} } catch { msg = {} }
  const title = msg.title || 'AgentWorks'
  event.waitUntil((async () => {
    const tabs = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
    if (msg.sound) for (const tab of tabs) tab.postMessage({ type: 'agentworks-notify-sound' })
    await self.registration.showNotification(title, {
      body: msg.body || '',
      icon: '/logo.svg',
      tag: msg.session || undefined,
      data: { route: typeof msg.route === 'string' && msg.route.startsWith('/') ? msg.route : '/' },
    })
  })())
})

self.addEventListener('notificationclick', event => {
  event.notification.close()
  const route = event.notification.data?.route || '/'
  event.waitUntil((async () => {
    const target = new URL(route, self.location.origin).href
    const tabs = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
    for (const tab of tabs) {
      if (new URL(tab.url).origin === self.location.origin && 'focus' in tab) {
        await tab.focus()
        if ('navigate' in tab) await tab.navigate(target)
        return
      }
    }
    await self.clients.openWindow(target)
  })())
})
