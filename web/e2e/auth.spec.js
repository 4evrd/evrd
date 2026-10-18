import { test, expect } from '@playwright/test'
import { USER, login, freshUser } from './helpers.js'

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
  await expect(page.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Sign out' }).first().click()
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
})

test('users don’t see administration', async ({ page }) => {
  await login(page)
  await expect(page.getByRole('navigation', { name: 'Administration' })).toHaveCount(0)
  await expect(page.getByRole('link', { name: 'Users', exact: true })).toHaveCount(0)
  await page.goto('/admin')
  await expect(page.getByText('Admins only.')).toBeVisible()
  await page.goto('/admin/users')
  await expect(page.getByText('Admins only.')).toBeVisible()
})

test('a suspended account can’t sign in', async ({ page }) => {
  const u = await freshUser('sp')
  try {
    await u.admin.patch(`/api/admin/users/${u.id}`, { suspended: true })
    await page.goto('/')
    await page.getByRole('textbox', { name: 'Username' }).fill(u.name)
    await page.getByLabel('Password', { exact: true }).fill(u.password)
    await page.getByRole('button', { name: 'Sign in' }).click()
    await expect(page.getByText(/suspended/i)).toBeVisible()
  } finally { await u.cleanup() }
})

test('when the daemon is unreachable the app says so and can retry', async ({ page }) => {
  await login(page)
  await page.route('**/api/me', (r) => r.abort())
  await page.reload()
  await expect(page.getByText('Couldn’t reach the bouncer.')).toBeVisible()
  await page.unroute('**/api/me')
  await page.getByRole('button', { name: 'Try again' }).click()
  await expect(page.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible()
})
