import { expect, test } from '@playwright/test'
import { register, uniqueSuffix } from './fixtures'

/**
 * The "ask for a review" link the owner dashboard copies. It exists to remove
 * one tap: an authenticated customer who has not reviewed yet should land
 * directly in the writing form, not on a page where they must find the button.
 */
test('a review-request link opens the form and posting shows the review', async ({ page }) => {
  const uid = uniqueSuffix()
  await register(page, {
    email: `review${uid}@example.test`,
    username: `rv${uid}`.slice(0, 20),
    password: 'E2ePassword1',
  })
  await page.waitForURL(/\/me$/)

  await page.goto('/b/rumah-kopi-senja?review=1#reviews')
  const box = page.getByPlaceholder(/What was it like/)
  await expect(box).toBeVisible()
  await box.fill('Pelayanan ramah dan kopinya enak, tempatnya nyaman untuk bekerja.')
  await page.getByRole('button', { name: 'Post review' }).click()
  await expect(page.getByText('Pelayanan ramah dan kopinya enak')).toBeVisible()
})
