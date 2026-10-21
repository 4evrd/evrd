import { test, expect } from '@playwright/test'
import { login, freshUser, addNetwork, uniq, ADMIN, ADMIN_PASS } from './helpers.js'
import { IRC } from './irc.js'

let u
test.beforeEach(async () => { u = await freshUser('hlth') })
test.afterEach(async () => { await u.cleanup() })

test('a network’s health: live state, tiles, ranges and channels', async ({ page }) => {
  test.setTimeout(150000)
  const chan = '#' + uniq('h')
  const net = await addNetwork(u.api, { name: 'Dev', nick: u.name, channels: [chan] })
  const irc = await IRC.connect({ user: u.name, network: 'Dev', password: u.password })
  try {
    await irc.waitFor(/ 366 /)
    for (let i = 0; i < 5; i++) irc.send(`PRIVMSG ${chan} :line ${i}`)
    await login(page, u.name, u.password)

    // from the network's card
    await page.goto('/networks')
    await page.getByRole('link', { name: 'Health and stats for Dev' }).click()
    await expect(page).toHaveURL(new RegExp(`/networks/${net.id}/health$`))
    await expect(page.getByTestId('health-title')).toHaveText('Dev')
    await expect(page.getByTestId('tile-status')).toContainText('Connected')
    for (const id of ['tile-uptime', 'tile-lag', 'tile-messages', 'tile-disconnects']) await expect(page.getByTestId(id)).toBeVisible()
    const info = page.getByTestId('health-info')
    await expect(info).toContainText('ircd:6667')
    await expect(info).toContainText(u.name)
    await expect(page.getByTestId('health-channels')).toContainText(chan)
    await expect(page.getByTestId('health-log-link')).toHaveAttribute('href', `/networks/${net.id}#log`)

    // the dev stack has a metrics store: the charts fill in once metrics are
    // exported (every 15s)
    await expect(page.getByTestId('health-no-metrics')).toHaveCount(0)
    await expect.poll(async () => (await u.api.get(`/api/networks/${net.id}/health`)).summary?.lines || 0, { timeout: 120000, intervals: [5000] }).toBeGreaterThan(0)
    await page.reload()
    await expect(page.getByTestId('tile-messages')).not.toContainText('—')
    await expect(page.getByTestId('uptime-band')).toBeVisible()

    // the range is a row of buttons, kept in the URL
    await page.getByRole('button', { name: '7 days' }).click()
    await expect(page).toHaveURL(/\?range=7d$/)
    await expect(page.getByRole('button', { name: '7 days' })).toHaveAttribute('aria-pressed', 'true')
    await expect(page.getByTestId('tile-uptime')).toContainText('Last 7 days')
    await page.getByRole('button', { name: '30 days' }).click()
    await expect(page).toHaveURL(/\?range=30d$/)
    await expect(page.getByTestId('tile-uptime')).toContainText('Last 30 days')
    await page.reload()
    await expect(page.getByRole('button', { name: '30 days' })).toHaveAttribute('aria-pressed', 'true')

    // the table alternative to the charts
    await page.getByRole('button', { name: 'Show table' }).click()
    await expect(page.locator('#health-table tbody tr')).toHaveCount(30)
  } finally { irc.close() }
})

test('without a metrics store the page still shows the live state', async ({ page }) => {
  const net = await addNetwork(u.api, { name: 'Dev', nick: u.name })
  // the API as it answers when no metrics store is configured
  await page.route(`**/api/networks/${net.id}/health**`, async (route) => {
    const body = await (await route.fetch()).json()
    delete body.series
    delete body.summary
    route.fulfill({ json: { ...body, metrics: false, reason: "metrics aren't configured" } })
  })
  await login(page, u.name, u.password)
  await page.goto(`/networks/${net.id}/health`)
  await expect(page.getByTestId('health-no-metrics')).toContainText('Metrics aren’t available.')
  await expect(page.getByTestId('tile-status')).toContainText('Connected')
  await expect(page.getByTestId('tile-messages')).toContainText('Needs a metrics store')
  await expect(page.getByRole('heading', { name: 'No charts without metrics' })).toBeVisible()
  await expect(page.getByTestId('health-info')).toContainText('ircd:6667')
})

test('the settings page and the overview link to it', async ({ page }) => {
  const net = await addNetwork(u.api, { name: 'Dev', nick: u.name })
  await login(page, u.name, u.password)
  await page.locator('.net-node', { hasText: 'Dev' }).click()
  await expect(page).toHaveURL(new RegExp(`/networks/${net.id}/health$`))
  await page.getByRole('link', { name: 'Settings' }).click()
  await expect(page).toHaveURL(new RegExp(`/networks/${net.id}$`))
  await page.getByRole('link', { name: 'Health', exact: true }).click()
  await expect(page.getByTestId('health-title')).toHaveText('Dev')
})

test('admins see any user’s network health', async ({ page }) => {
  const net = await addNetwork(u.api, { name: 'Dev', nick: u.name })
  await login(page, ADMIN, ADMIN_PASS)
  await page.goto(`/admin/users/${u.id}`)
  await page.getByRole('link', { name: 'Health of Dev' }).click()
  await expect(page).toHaveURL(new RegExp(`/admin/users/${u.id}/networks/${net.id}/health$`))
  await expect(page.getByTestId('health-title')).toHaveText('Dev')
  await expect(page.getByTestId('tile-status')).toContainText('Connected')
  await page.getByRole('button', { name: '7 days' }).click()
  await expect(page).toHaveURL(/\?range=7d$/)
  await expect(page.getByTestId('health-log-link')).toHaveAttribute('href', `/admin/users/${u.id}#net-${net.id}`)
})
