import { test, expect } from '@playwright/test'
import { login, freshUser, addNetwork, uniq } from './helpers.js'

// At phone width the pages fit: nothing makes them scroll sideways.

let me, net
test.beforeEach(async ({ page }) => {
  me = await freshUser('mob')
  net = await addNetwork(me.api, { name: 'Libera', nick: me.name, channels: ['#' + uniq('a-rather-long-channel-name'), '#go-nuts'] })
  await me.api.post('/api/me/client-passwords', { name: 'A laptop with a fairly long name' })
  await page.setViewportSize({ width: 390, height: 844 })
  await login(page, me.name, me.password)
})
test.afterEach(async () => { await me.cleanup() })

const fits = async (page) => {
  await page.waitForTimeout(300)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
}

test('the IRC clients page fits a phone, on every client’s tab', async ({ page }) => {
  await page.goto('/clients')
  await expect(page.getByRole('heading', { name: 'Set up your client' })).toBeVisible()
  await expect(page.getByText('A laptop with a fairly long name')).toBeVisible()
  for (const tab of ['HexChat', 'irssi', 'WeeChat', 'Other']) {
    const t = page.getByRole('tab', { name: tab })
    await t.scrollIntoViewIfNeeded() // below the sign-in switch on a phone
    await expect(t).toBeInViewport({ ratio: 1 })
    await t.click()
    await fits(page)
  }
})

test('a network’s page fits a phone', async ({ page }) => {
  await page.goto(`/networks/${net.id}`)
  await expect(page.getByTestId('playback')).toBeVisible()
  await fits(page)
})
