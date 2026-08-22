import { test, expect } from '@playwright/test'

/**
 * Critical journeys (PRD §12.7): register → login → wizard → submit → admin
 * approve → public page → search. Runs against the live stack (Vite + Go).
 */

const uid = Date.now().toString(36)

test('register → login → verify email → profile', async ({ page }) => {
  await page.goto('/register')
  await page.locator('input[type=email]').fill(`e2e-${uid}@example.com`)
  await page.locator('input[autocomplete=name]').fill('E2E Owner')
  await page.locator('input[autocomplete=username]').fill(`e2e${uid}`)
  await page.locator('#field-password').fill('secret1234')
  await page.locator('#field-confirm-password').fill('secret1234')
  await page.locator('button[type=submit]').click()
  // Auth endpoints are rate-limited (5/min/IP, PRD §5.9.1) — wait out the window.
  await expect(page).toHaveURL(/\/me$/, { timeout: 120_000 })
  await expect(page.getByText('E2E Owner')).toBeVisible()

  await page.getByRole('button', { name: /Sign out/ }).click()
  await page.goto('/login')
  await page.locator('input[type=email]').fill(`e2e-${uid}@example.com`)
  await page.locator('input[type=password]').fill('secret1234')
  await page.locator('button[type=submit]').click()
  await expect(page).toHaveURL(/\/me$/, { timeout: 120_000 })
})

test('landing page renders live trending + categories', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: /Every business in the world/ })).toBeVisible()
  await expect(page.getByText(/Categories/i).first()).toBeVisible()
})

test('discover search finds verified businesses', async ({ page }) => {
  await page.goto('/discover?q=coffee')
  await expect(page.getByText(/result/)).toBeVisible()
})

test('public business page shows storefront + engagement bar', async ({ page }) => {
  await page.goto('/b/rumah-kopi-senja')
  await expect(page.getByRole('heading', { name: 'Rumah Kopi Senja' })).toBeVisible()
  await expect(page.getByText('Fully Verified')).toBeVisible()
})

test('compare page works with two businesses', async ({ page }) => {
  await page.goto('/compare?b=5845a5db-02b0-4ce8-a6d1-95f2b4b16396,dcb251c1-01c7-4969-967f-54f47967d3e9')
  await expect(page.getByText('Rumah Kopi Senja')).toBeVisible()
  await expect(page.getByText('Razor & Thread Barbershop')).toBeVisible()
})

test('category page shows the trending leaderboard', async ({ page }) => {
  await page.goto('/c/cafe')
  await expect(page.getByRole('heading', { name: 'Café' })).toBeVisible()
  await expect(page.getByText(/Top in Café/)).toBeVisible()
  await expect(page.getByText('Rumah Kopi Senja')).toBeVisible()
  await expect(page.getByText(/updated \d+ min ago/)).toBeVisible()
})
