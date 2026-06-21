import { test, expect } from '@playwright/test'
import { USER, login } from './helpers.js'

test('rejects invalid credentials', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('textbox', { name: 'Username' }).fill(USER)
  await page.getByRole('textbox', { name: 'Password' }).fill('definitely-wrong')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByText(/invalid credentials/i)).toBeVisible()
})

test('signs in and back out', async ({ page }) => {
  await login(page)
  await expect(page.getByRole('heading', { name: 'Networks' })).toBeVisible()
  await page.getByRole('button', { name: 'sign out' }).click()
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
})
