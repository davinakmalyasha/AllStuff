import { expect, test } from '@playwright/test'
import { register, seededBusinessBySlug, uniqueSuffix } from './fixtures'

/**
 * The customer booking journey: find a service, pick a time, book it, take it
 * with you, and cancel.
 *
 * It runs against the seeded café, whose "Kopi Cupping Session" is the one
 * product with a duration. If that product disappears from scripts/seed.sql this
 * test fails at the panel, with a message naming what it expected - not by
 * silently passing on a page with nothing to book.
 */
const PASSWORD = 'E2ePassword1'

test('book a service, add it to the calendar, then cancel', async ({ page, request }) => {
  const biz = await seededBusinessBySlug(request, 'rumah-kopi-senja')
  const uid = uniqueSuffix()
  await register(page, {
    email: `book${uid}@example.test`,
    username: `bk${uid}`.slice(0, 20),
    password: PASSWORD,
  })
  await page.waitForURL(/\/me$/)

  await page.goto(`/b/${biz.slug}`)

  // The panel only appears for a bookable service, so its presence is also the
  // assertion that the seed carries duration_minutes.
  await expect(
    page.getByRole('heading', { name: /^Book Kopi Cupping Session$/ }),
  ).toBeVisible()

  // Walk forward until a day actually has a free slot. A fixed day is a hidden
  // dependency on the clock: today may be over, and a day near a boundary can
  // legitimately hold nothing once the business's timezone is applied.
  const days = page.getByRole('group', { name: 'Choose a day' }).getByRole('button')
  const times = page.getByRole('group', { name: 'Choose a time' }).getByRole('button')
  let found = false
  for (let i = 0; i < 6 && !found; i++) {
    await days.nth(i).click()
    try {
      await expect(times.first()).toBeVisible({ timeout: 4_000 })
      found = true
    } catch {
      // No times left on that day; try the next one.
    }
  }
  expect(found, 'no bookable slot in the next six days').toBe(true)
  await times.first().click()

  // The confirmation replaces the picker, so a second booking needs a deliberate
  // "book another time".
  await expect(page.getByText(/^Booked for /)).toBeVisible()

  await page.goto('/me/bookings')
  await expect(page.getByRole('heading', { name: 'My bookings' })).toBeVisible()
  await expect(page.getByText('Kopi Cupping Session')).toBeVisible()
  await expect(page.getByText('Confirmed')).toBeVisible()

  // Add-to-calendar downloads a real .ics.
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Add to calendar' }).click()
  expect((await download).suggestedFilename()).toBe('booking.ics')

  // Rescheduling offers exactly the derived availability for the same service,
  // so picking a time there must succeed; the card then shows the new time.
  await page.getByRole('button', { name: 'Reschedule' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  const moveDays = dialog.getByRole('group', { name: 'Choose a day' }).getByRole('button')
  const moveTimes = dialog.getByRole('group', { name: 'Choose a time' }).getByRole('button')
  let moved = false
  for (let i = 0; i < 6 && !moved; i++) {
    await moveDays.nth(i).click()
    try {
      await expect(moveTimes.first()).toBeVisible({ timeout: 4_000 })
      moved = true
    } catch {
      // No times left on that day; try the next one.
    }
  }
  expect(moved, 'no reschedule slot in the next six days').toBe(true)
  // Pick the last offered time rather than the first, so the booking visibly
  // changes even when the original slot is still on the same day.
  await moveTimes.last().click()
  await expect(page.getByText('Booking moved')).toBeVisible()
  await expect(dialog).toBeHidden()

  // Cancelling asks first. The booking does not vanish - a cancellation is part
  // of the customer's history - it moves to Past and its status says Cancelled,
  // which is also why the Upcoming section disappears.
  await page.getByRole('button', { name: 'Cancel' }).click()
  await page.getByRole('button', { name: 'Cancel booking' }).click()
  await expect(page.getByText('Cancelled', { exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Upcoming' })).toBeHidden()
  await expect(page.getByRole('heading', { name: 'Past' })).toBeVisible()
})
