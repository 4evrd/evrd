import { test, expect } from '@playwright/test'
import { USER, login } from './helpers.js'

test('rejects invalid credentials', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('textbox', { name: 'Username' }).fill(USER)
  await page.getByLabel('Password', { exact: true }).fill('definitely-wrong')
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByText(/wrong username or password/i)).toBeVisible()
})

test('signs in, survives a reload, and signs out', async ({ page }) => {
  await login(page)
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Overview' })).toBeVisible()
  await page.getByRole('button', { name: 'Sign out' }).first().click()
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
})

test('users don’t see administration', async ({ page }) => {
  await login(page)
  await expect(page.getByRole('link', { name: 'Users' })).toHaveCount(0)
  await page.goto('/admin')
  await expect(page.getByText('Admins only.')).toBeVisible()
})
