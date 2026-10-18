import { test, expect } from '@playwright/test'
import { login, resetNetworks, confirm } from './helpers.js'

test.beforeEach(async ({ request }) => {
  await resetNetworks(request)
})

async function addNetwork(page, name, nick) {
  await page.goto('/networks')
  await page.getByLabel('Network name').fill(name)
  await page.getByLabel('Server').fill('ircd')
  await page.getByRole('checkbox', { name: 'TLS' }).uncheck()
  await page.getByLabel('Port').fill('6667')
  if (nick) await page.getByLabel('Nick').fill(nick)
  await page.getByLabel('Channels').fill('#e2e')
  await page.getByRole('button', { name: 'Add network' }).last().click()
}

test('add a network, connect it, open it and send a message', async ({ page }) => {
  await login(page)
  await addNetwork(page, 'E2E', 'e2euser')

  const row = page.getByTestId('network-row').filter({ hasText: 'E2E' })
  await expect(row).toBeVisible()
  await row.getByRole('button', { name: 'Connect', exact: true }).click()
  await expect(row.getByText('Connected', { exact: true })).toBeVisible({ timeout: 20000 })

  await row.getByRole('link', { name: 'Open' }).click()
  const input = page.getByPlaceholder(/Message/)
  await input.fill('hello from e2e')
  await input.press('Enter')
  await expect(page.getByText('hello from e2e')).toBeVisible({ timeout: 10000 })
})

test('removing a network clears the list', async ({ page }) => {
  await login(page)
  await addNetwork(page, 'Temp')
  await expect(page.getByTestId('network-row')).toHaveCount(1)
  await page.getByRole('button', { name: 'Remove Temp' }).click()
  await confirm(page)
  await expect(page.getByText('No networks yet')).toBeVisible()
})

test('edit a network', async ({ page }) => {
  await login(page)
  await addNetwork(page, 'Edit', 'before')
  await page.getByRole('button', { name: 'Edit Edit' }).click()
  const form = page.getByRole('form', { name: 'Edit Edit' })
  await form.getByLabel('Nick').fill('after')
  await form.getByLabel('Channels').fill('#e2e #more')
  await form.getByRole('button', { name: 'Save changes' }).click()
  const row = page.getByTestId('network-row').filter({ hasText: 'Edit' })
  await expect(row.getByText(/after · ircd:6667.*#e2e #more/)).toBeVisible()
})

test('chat commands: /join, /me, /nick, /part and events', async ({ page }) => {
  await login(page)
  await addNetwork(page, 'Cmds', 'cmduser')
  const row = page.getByTestId('network-row').filter({ hasText: 'Cmds' })
  await row.getByRole('button', { name: 'Connect', exact: true }).click()
  await expect(row.getByText('Connected', { exact: true })).toBeVisible({ timeout: 20000 })
  await row.getByRole('link', { name: 'Open' }).click()
  await expect(page.getByText('Live', { exact: true })).toBeVisible({ timeout: 10000 })

  const input = page.getByRole('textbox', { name: 'Message' })
  await input.fill('/join #cmds')
  await input.press('Enter')
  await expect(page.getByRole('heading', { name: '#cmds' })).toBeVisible()
  await expect(page.locator('.log').getByText('cmduser joined')).toBeVisible({ timeout: 10000 })

  await input.fill('/me waves')
  await input.press('Enter')
  await expect(page.locator('.log').getByText('* cmduser waves')).toBeVisible()

  await input.fill('/nick cmdnew')
  await input.press('Enter')
  await expect(page.locator('.log').getByText('cmduser is now known as cmdnew')).toBeVisible({ timeout: 10000 })
  await expect(page.locator('.conv-head .who')).toHaveText('as cmdnew')

  await input.fill('/help')
  await input.press('Enter')
  await expect(page.locator('.log').getByText(/\/join #channel \[key\]/)).toBeVisible()

  await input.fill('/bogus')
  await input.press('Enter')
  await expect(page.locator('.log').getByText(/\/bogus isn’t supported/)).toBeVisible()

  await input.fill('/part #cmds bye')
  await input.press('Enter')
  await expect(page.locator('.buf', { hasText: 'cmds' })).toHaveCount(0, { timeout: 10000 })
})

test('*status works from the web chat', async ({ page }) => {
  await login(page)
  await addNetwork(page, 'Stat', 'statuser')
  const row = page.getByTestId('network-row').filter({ hasText: 'Stat' })
  await row.getByRole('button', { name: 'Connect', exact: true }).click()
  await expect(row.getByText('Connected', { exact: true })).toBeVisible({ timeout: 20000 })
  await row.getByRole('link', { name: 'Open' }).click()
  await expect(page.getByText('Live', { exact: true })).toBeVisible({ timeout: 10000 })

  const input = page.getByRole('textbox', { name: 'Message' })
  await input.fill('/msg *status help')
  await input.press('Enter')
  await expect(page.getByRole('heading', { name: '*status' })).toBeVisible()
  await expect(page.locator('.log').getByText(/evrd commands/)).toBeVisible({ timeout: 5000 })

  // Typing in the *status buffer runs commands too.
  await input.fill('networks')
  await input.press('Enter')
  await expect(page.locator('.log').getByText(/\* Stat\s+connected/)).toBeVisible({ timeout: 5000 })
})
