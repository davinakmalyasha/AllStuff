import { test, expect, type Page } from '@playwright/test'

/**
 * Notifications (PRD §5.7): two users, direct chat → the recipient gets a
 * message_received notification live over WS (bell badge) and in the inbox.
 */
const uid = Date.now().toString(36)

async function register(page: Page, email: string, name: string, username: string) {
  await page.goto('/register')
  await page.locator('input[type=email]').fill(email)
  await page.locator('input[autocomplete=name]').fill(name)
  await page.locator('input[autocomplete=username]').fill(username)
  await page.locator('#field-password').fill('secret1234')
  await page.locator('#field-confirm-password').fill('secret1234')
  await page.locator('button[type=submit]').click()
  await expect(page).toHaveURL(/\/me$/, { timeout: 120_000 })
}

test('direct chat message raises a live notification for the recipient', async ({ browser }) => {
  test.setTimeout(240_000)
  const a = await browser.newContext()
  const b = await browser.newContext()
  const pageA = await a.newPage()
  const pageB = await b.newPage()

  await register(pageA, `na-${uid}@example.com`, 'Notif A', `notifa${uid}`)
  await register(pageB, `nb-${uid}@example.com`, 'Notif B', `notifb${uid}`)

  // B's public profile gives us their user id.
  const profile = await pageA.request.get(`/api/v1/u/notifb${uid}`)
  const { id } = (await profile.json()) as { id: string }

  // CSRF double-submit (PRD §5.9.1): fetch the token cookie, then echo it.
  await pageA.request.get('/api/v1/auth/csrf')
  const csrf = await pageA.evaluate(() => document.cookie.match(/bv_csrf=([^;]+)/)?.[1] ?? '')

  // A opens a direct thread with B and sends a message.
  const threadRes = await pageA.request.post('/api/v1/threads', {
    data: { user_id: id },
    headers: { 'X-CSRF-Token': csrf },
  })
  expect(threadRes.ok()).toBeTruthy()
  const thread = (await threadRes.json()) as { thread: { id: string } }
  const sendRes = await pageA.request.post(`/api/v1/threads/${thread.thread.id}/messages`, {
    data: { body: 'hello from A', client_msg_id: `e2e-${uid}` },
    headers: { 'X-CSRF-Token': csrf },
  })
  expect(sendRes.ok()).toBeTruthy()

  // B's bell badge (PublicLayout header) updates live over WS (no reload).
  await pageB.goto('/')
  await expect(pageB.getByRole('button', { name: /Notifications.*1 unread/ })).toBeVisible({
    timeout: 15_000,
  })

  // The inbox page lists the notification with a deep link.
  await pageB.goto('/me/notifications')
  await expect(pageB.getByText('New message').first()).toBeVisible()

  // Opening the link marks it read (verify via API).
  await pageB.getByText('New message').first().click()
  await pageB.waitForURL(/\/me\/messages\//)

  // Polled, not sampled once.
  //
  // The click handler fires the mark-read POST with `void markRead.mutateAsync(...)`
  // and the router navigates at the same time, so waitForURL resolves as soon as
  // the client-side route changes - which says nothing about whether the POST has
  // landed. Reading /notifications immediately after therefore races the write, and
  // reports unread=1 for a notification that is a moment later correctly read.
  //
  // That is not hypothetical: this assertion failed on the first attempt of a run
  // and passed on the retry, which is the signature of a race rather than a bug.
  // expect.poll states the property that actually matters - the count settles at
  // zero - without asserting how quickly.
  await expect
    .poll(async () => {
      const notifs = await pageB.request.get('/api/v1/notifications?limit=5')
      const { unread } = (await notifs.json()) as { unread: number }
      return unread
    }, { timeout: 15_000, message: 'the notification should be marked read after opening its deep link' })
    .toBe(0)

  await a.close()
  await b.close()
})
