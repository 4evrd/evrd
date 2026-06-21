import { test, expect } from '@playwright/test'
import { login, resetNetworks } from './helpers.js'

test.beforeEach(async ({ request }) => {
  await resetNetworks(request)
})

test('add a network, connect it, open the console and send a message', async ({ page }) => {
  await login(page)

  // Add a network pointing at the dev upstream (ircd:6667, no TLS).
  await page.getByRole('button', { name: '+ Add network' }).click()
  await page.getByLabel('Network name').fill('E2E')
  await page.getByLabel('Server', { exact: true }).fill('ircd')
  await page.getByLabel('Port').fill('6667')
  await page.getByRole('checkbox', { name: 'TLS' }).uncheck()
  await page.getByLabel('Nick').fill('e2euser')
  await page.getByLabel('Channels').fill('#e2e')
  await page.getByRole('button', { name: 'Add network' }).click()

  // It shows up and connects.
  const row = page.getByTestId('network-row').filter({ hasText: 'E2E' })
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: 'Connect', exact: true }).click()
  await expect(row.getByText('connected', { exact: true })).toBeVisible({ timeout: 20000 })

  // Open the console and send a message; it should echo into the log.
  await row.getByRole('button', { name: 'Open' }).click()
  const input = page.getByPlaceholder(/Message/)
  await input.fill('hello from e2e')
  await input.press('Enter')
  await expect(page.getByText('hello from e2e')).toBeVisible({ timeout: 10000 })
})

test('removing a network clears the list', async ({ page }) => {
  await login(page)

  await page.getByRole('button', { name: '+ Add network' }).click()
  await page.getByLabel('Network name').fill('Temp')
  await page.getByLabel('Server', { exact: true }).fill('ircd')
  await page.getByLabel('Port').fill('6667')
  await page.getByRole('checkbox', { name: 'TLS' }).uncheck()
  await page.getByLabel('Nick').fill('tempnick')
  await page.getByRole('button', { name: 'Add network' }).click()

  await expect(page.getByText('Temp', { exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Remove' }).click()
  await expect(page.getByText('No networks yet.')).toBeVisible()
})
