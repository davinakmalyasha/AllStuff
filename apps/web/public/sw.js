/* BizVerse service worker (PRD §5.5.3) — Web Push + install prompt. */
self.addEventListener('install', () => {
  self.skipWaiting()
})

self.addEventListener('activate', (event) => {
  event.waitUntil(self.clients.claim())
})

self.addEventListener('push', (event) => {
  let payload = { title: 'BizVerse', body: 'New notification', data: {} }
  try {
    const parsed = event.data ? JSON.parse(event.data.text()) : {}
    payload = { ...payload, ...parsed }
  } catch {
    /* plain text body */
  }
  event.waitUntil(
    self.registration.showNotification(payload.title, {
      body: payload.body,
      icon: '/icon-192.svg',
      badge: '/icon-192.svg',
      data: payload.data,
    }),
  )
})

self.addEventListener('notificationclick', (event) => {
  event.notification.close()
  const data = event.notification.data || {}
  const url = data.url || '/'
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clients) => {
      for (const client of clients) {
        if (client.url.includes(windowLocation(url))) {
          return client.focus()
        }
      }
      return self.clients.openWindow(url)
    }),
  )
})

function windowLocation(url) {
  try {
    return new URL(url, self.location.origin).origin + new URL(url, self.location.origin).pathname
  } catch {
    return url
  }
}
