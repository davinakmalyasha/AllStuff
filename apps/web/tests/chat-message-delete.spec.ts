import { test, expect } from '@playwright/test'
import { register, uniqueSuffix } from './fixtures'

/**
 * Deleting a message for one participant.
 *
 * This covers the handler path that crashed in production, and it lives here rather
 * than in internal/httpapi for a reason: that package's tests build the middleware
 * chain with probe routes and NO database, so they cannot reach a real Chat service.
 * Reaching one needs repos, which needs a database - and the E2E suite already has a
 * real PostgreSQL instance and real HTTP. The crash was on the HTTP path, so this is
 * where it belongs.
 *
 * What went wrong: migration 0048 moved the per-participant scope out of
 * chat_messages.deleted_for and into chat_message_hides, so a private delete no longer
 * mutates the shared row. The service was correctly changed to return NO message -
 * echoing it would re-insert into the actor's view the message just hidden - but the
 * handler still dereferenced that nil to build the `message.deleted` broadcast. Every
 * "delete for me" panicked.
 *
 * It shipped green because the repository tests covered the hide itself, no test
 * covered the handler's use of the result, and the database-backed suites could not
 * run locally.
 *
 * The second assertion matters as much as the first. A private delete that hides the
 * message from EVERYONE is the original 0048 defect, so a test that only checks the
 * actor's view would pass against the bug this whole change set exists to fix.
 */

const PASSWORD = 'e2e-password-1234'

type MessageDTO = { id: number; body?: string }

test('a private delete hides the message from the actor and not the other party', async ({ browser }) => {
  const uid = uniqueSuffix()
  const ctxA = await browser.newContext()
  const ctxB = await browser.newContext()
  const pageA = await ctxA.newPage()
  const pageB = await ctxB.newPage()

  const usernameB = `delb${uid}`
  await register(pageA, { email: `da-${uid}@example.com`, username: `dela${uid}`, password: PASSWORD })
  await register(pageB, { email: `db-${uid}@example.com`, username: usernameB, password: PASSWORD })

  // B's user id, from their public profile.
  const profile = await pageA.request.get(`/api/v1/u/${usernameB}`)
  expect(profile.ok(), `profile lookup returned ${profile.status()}`).toBeTruthy()
  const { id: userBId } = (await profile.json()) as { id: string }

  // CSRF double-submit: fetch the cookie, then echo it on mutating requests.
  await pageA.request.get('/api/v1/auth/csrf')
  const csrf = await pageA.evaluate(() => document.cookie.match(/bv_csrf=([^;]+)/)?.[1] ?? '')
  expect(csrf, 'no CSRF cookie was issued').not.toBe('')
  const mutating = { 'X-CSRF-Token': decodeURIComponent(csrf) }

  // A opens a direct thread with B and sends a message.
  const threadRes = await pageA.request.post('/api/v1/threads', {
    data: { user_id: userBId },
    headers: mutating,
  })
  expect(threadRes.ok(), `thread create returned ${threadRes.status()}`).toBeTruthy()
  const { thread } = (await threadRes.json()) as { thread: { id: string } }

  const body = `delete-me-${uid}`
  const sendRes = await pageA.request.post(`/api/v1/threads/${thread.id}/messages`, {
    data: { body, client_msg_id: `e2e-${uid}` },
    headers: mutating,
  })
  expect(sendRes.ok(), `send returned ${sendRes.status()}`).toBeTruthy()
  const { message } = (await sendRes.json()) as { message: MessageDTO }
  expect(message.id, 'no message id in the send response').toBeTruthy()

  // THE REGRESSION. Before the fix this returned 500: the handler dereferenced a nil
  // message to build a broadcast tombstone for a delete that has no tombstone.
  const del = await pageA.request.delete(`/api/v1/messages/${message.id}?scope=me`, { headers: mutating })
  expect(
    del.status(),
    'a private delete must succeed with 204; anything else means the handler is using the service result it does not return',
  ).toBe(204)

  // A's view: gone.
  const aView = await pageA.request.get(`/api/v1/threads/${thread.id}`)
  expect(aView.ok()).toBeTruthy()
  const aBody = (await aView.json()) as { messages: MessageDTO[] }
  expect(
    aBody.messages.some((m) => m.id === message.id),
    'the message the actor just hid is still in the actor\'s own list',
  ).toBe(false)

  // B's view: still there. This is the whole point of the per-participant scope.
  const bView = await pageB.request.get(`/api/v1/threads/${thread.id}`)
  expect(bView.ok()).toBeTruthy()
  const bBody = (await bView.json()) as { messages: MessageDTO[] }
  expect(
    bBody.messages.some((m) => m.id === message.id),
    '"delete for me" removed the message for the other participant - it deleted for everyone',
  ).toBe(true)

  // Repeating the delete must stay a no-op rather than raising on the unique
  // (message_id, user_id) key.
  const again = await pageA.request.delete(`/api/v1/messages/${message.id}?scope=me`, { headers: mutating })
  expect(again.status(), 'deleting the same message twice must not fail').toBe(204)

  await ctxA.close()
  await ctxB.close()
})

/**
 * Deleting for EVERYONE is the other branch, and it must still broadcast and echo the
 * message. A fix that made both scopes behave like "me" would satisfy the test above
 * and break this one.
 */
test('deleting for everyone removes it from both participants', async ({ browser }) => {
  const uid = uniqueSuffix()
  const ctxA = await browser.newContext()
  const ctxB = await browser.newContext()
  const pageA = await ctxA.newPage()
  const pageB = await ctxB.newPage()

  const usernameB = `everb${uid}`
  await register(pageA, { email: `ea-${uid}@example.com`, username: `evera${uid}`, password: PASSWORD })
  await register(pageB, { email: `eb-${uid}@example.com`, username: usernameB, password: PASSWORD })

  const profile = await pageA.request.get(`/api/v1/u/${usernameB}`)
  const { id: userBId } = (await profile.json()) as { id: string }

  await pageA.request.get('/api/v1/auth/csrf')
  const csrf = await pageA.evaluate(() => document.cookie.match(/bv_csrf=([^;]+)/)?.[1] ?? '')
  const mutating = { 'X-CSRF-Token': decodeURIComponent(csrf) }

  const threadRes = await pageA.request.post('/api/v1/threads', {
    data: { user_id: userBId },
    headers: mutating,
  })
  const { thread } = (await threadRes.json()) as { thread: { id: string } }

  const sendRes = await pageA.request.post(`/api/v1/threads/${thread.id}/messages`, {
    data: { body: `remove-for-all-${uid}`, client_msg_id: `e2e-all-${uid}` },
    headers: mutating,
  })
  const { message } = (await sendRes.json()) as { message: MessageDTO }

  const del = await pageA.request.delete(`/api/v1/messages/${message.id}?scope=everyone`, { headers: mutating })
  expect(del.status(), 'delete-for-everyone must succeed').toBe(200)

  for (const [who, page] of [['actor', pageA], ['other party', pageB]] as const) {
    const view = await page.request.get(`/api/v1/threads/${thread.id}`)
    const viewBody = (await view.json()) as { messages: MessageDTO[] }
    expect(
      viewBody.messages.some((m) => m.id === message.id),
      `the message is still visible to the ${who} after delete-for-everyone`,
    ).toBe(false)
  }

  await ctxA.close()
  await ctxB.close()
})
