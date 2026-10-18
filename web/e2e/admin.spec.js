import { test, expect } from '@playwright/test'
import { ADMIN, ADMIN_PASS, login } from './helpers.js'

test('an admin creates, suspends and deletes a user', async ({ page }) => {
  const name = `tmp${Date.now() % 100000}`
  await login(page, ADMIN, ADMIN_PASS)
  await page.getByRole('link', { name: 'Users' }).click()

  await page.getByRole('textbox', { name: 'Username' }).fill(name)
  await page.getByRole('textbox', { name: 'Password' }).fill('temporary-pass')
  await page.getByRole('button', { name: 'Create user' }).click()
  const row = page.getByTestId('user-row').filter({ hasText: name })
  await expect(row).toBeVisible()

  await row.click()
  await expect(page.getByRole('heading', { name })).toBeVisible()
  await page.getByLabel('Suspension reason').fill('e2e')
  await page.getByRole('button', { name: 'Suspend' }).click()
  await page.locator('dialog.modal[open] #dlg-ok').click()
  await expect(page.getByText('Suspended:')).toBeVisible()

  await page.getByRole('button', { name: 'Delete user' }).click()
  await page.locator('dialog.modal[open]').getByRole('textbox').fill(name)
  await page.locator('dialog.modal[open] #dlg-ok').click()
  await expect(page.getByRole('heading', { name: 'Users' })).toBeVisible()
  await expect(page.getByTestId('user-row').filter({ hasText: name })).toHaveCount(0)
})
