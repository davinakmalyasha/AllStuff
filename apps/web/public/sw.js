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
  // Same-origin only: push payloads are VAPID-signed, but if the signing key
  // ever leaks a crafted {url} must not open attacker-controlled origins.
  const raw = data.url || '/'
  let url
  try {
    const u = new URL(raw, self.location.origin)
    if (u.origin !== self.location.origin) return
    url = u.pathname + u.search + u.hash
  } catch {
    url = '/'
  }
  event.waitUntil(
    self.clients.matchAll({ type: 'window', includeUncontrolled: true }).then((clients) => {
      for (const client of clients) {
        if (client.url === self.location.origin + url) {
          return client.focus()
        }
      }
      return self.clients.openWindow(url)
    }),
  )
})
