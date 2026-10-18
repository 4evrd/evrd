import { test, expect } from '@playwright/test'
import { login, freshUser, addNetwork, uniq } from './helpers.js'
import { IRC } from './irc.js'

let u
test.beforeEach(async () => { u = await freshUser('ovw') })
test.afterEach(async () => { await u.cleanup() })

test('a new account: empty states point the way', async ({ page }) => {
  await login(page, u.name, u.password)
  await expect(page.getByText(`${u.name}. 0 of 0 networks are connected`)).toBeVisible()
  await expect(page.getByTestId('tile-networks')).toHaveText(/^0\s*\/ ∞$/)
  await expect(page.getByRole('heading', { name: 'No networks yet' })).toBeVisible()
  await expect(page.getByText('No clients attached.')).toBeVisible()
  await page.getByRole('link', { name: 'Connect a client' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'IRC clients' })).toBeVisible()
})

test('networks, channels, clients and events', async ({ page }) => {
  const chan = '#' + uniq('o')
  const net = await addNetwork(u.api, { name: 'Dev', nick: u.name, channels: [chan] })
  const irc = await IRC.connect({ user: u.name, network: 'Dev', password: u.password, version: 'WeeChat 4.4.2' })
  try {
    await irc.waitFor(/ 366 /)
    await login(page, u.name, u.password)
    await expect(page.getByTestId('tile-connected')).toHaveText(/^1\s*\/ 1$/)
    // topology: the network and its channel, linked to the chat
    await expect(page.locator('.net-node', { hasText: 'Dev' })).toContainText('Connected')
    const node = page.locator('.chan-node', { hasText: chan })
    await expect(node).toContainText('Dev · 1 users')
    // attached clients, named from CTCP VERSION
    await expect(async () => {
      await page.reload()
      await expect(page.getByTestId('tile-clients')).toHaveText(/^1\s*\/ 5$/, { timeout: 2000 })
    }).toPass({ timeout: 20000 })
    await expect(page.locator('section[aria-labelledby="att-h"]')).toContainText('WeeChat')
    await expect(page.locator('section[aria-labelledby="ev-h"]')).toContainText('Connected to Dev')
    // the attach event lands a few seconds after the client connects
    await expect(async () => {
      await page.reload()
      await expect(page.locator('section[aria-labelledby="ev-h"]')).toContainText('WeeChat attached', { timeout: 2000 })
    }).toPass({ timeout: 20000 })
    await node.click()
    await expect(page).toHaveURL(new RegExp(`/chat\\?net=${net.id}&buf=`))
    await expect(page.locator('#buf-title')).toHaveText(chan)
  } finally { irc.close() }
})

test('traffic is charted from VictoriaMetrics', async ({ page }) => {
  test.setTimeout(150000)
  const chan = '#' + uniq('t')
  const net = await addNetwork(u.api, { name: 'Dev', nick: u.name, channels: [chan] })
  const irc = await IRC.connect({ user: u.name, network: 'Dev', password: u.password })
  try {
    await irc.waitFor(/ 366 /)
    for (let i = 0; i < 20; i++) irc.send(`PRIVMSG ${chan} :line ${i}`)
    // metrics are exported every 15s; the overview reads them back
    await expect.poll(async () => (await u.api.get('/api/overview')).traffic.total24h, { timeout: 120000, intervals: [5000] }).toBeGreaterThan(0)
    await login(page, u.name, u.password)
    await expect(page.getByTestId('tile-messages')).not.toHaveText('—')
    await expect(page.locator('section[aria-labelledby="traffic-h"] .legend')).toContainText('Dev')
    await expect(page.getByText('Traffic charts need a metrics store.')).toHaveCount(0)
    expect((await u.api.get('/api/overview')).traffic.series.map((s) => s.network)).toContain(net.id)
  } finally { irc.close() }
})
