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
      await expect(page.getByTestId('tile-clients')).toHaveText(/^1\s*\/ ∞$/, { timeout: 2000 })
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
    // hovering reads the hour off: each network's lines in it
    const plot = page.locator('section[aria-labelledby="traffic-h"] .chart-hover')
    const box = await plot.boundingBox()
    await plot.hover({ position: { x: box.width - 2, y: box.height / 2 } })
    const readout = page.getByTestId('chart-readout')
    await expect(readout).toContainText('–now')
    await expect(readout).toContainText('Dev')
    await page.getByRole('heading', { name: 'Lines per hour, last 24h' }).hover()
    await expect(readout).toHaveCount(0)
  } finally { irc.close() }
})

test('reconnects: which network, how often and why', async ({ page }) => {
  test.setTimeout(150000)
  await addNetwork(u.api, { name: 'Dev', nick: u.name })
  // pointed at a port nothing listens on: every attempt fails
  const bad = await u.api.post('/api/networks', { name: 'Closed', host: 'ircd', port: 6699, tls: false, nick: u.name })
  await u.api.post(`/api/networks/${bad.id}/connect`)
  await login(page, u.name, u.password)
  await page.getByTestId('reconnects-link').click()
  await expect(page).toHaveURL(/\/reconnects$/)
  await expect(page.getByRole('heading', { level: 1, name: 'Reconnects' })).toBeVisible()

  // the failing network, with the reason, before the metrics arrive
  const row = page.getByTestId('reconnects-net').filter({ hasText: 'Closed' })
  await expect(row).toContainText('connection refused', { timeout: 20000 })
  await expect(row).toContainText('nothing listens on that port')
  await expect(page.getByTestId('reconnects-net').filter({ hasText: 'Dev' })).toHaveCount(0)
  // one event per error, not one per retry
  const events = page.getByTestId('reconnects-events')
  await expect(events).toContainText("Closed: couldn't connect: connection refused")
  // counted once metrics are exported (every 15s)
  await expect.poll(async () => (await u.api.get('/api/reconnects')).networks.find((n) => n.id === bad.id)?.failed || 0, { timeout: 120000, intervals: [5000] }).toBeGreaterThan(0)
  await page.reload()
  await expect(row).toContainText(/[1-9]\d* failed/)
  await expect(row.locator('.rc-hours span.on')).not.toHaveCount(0)

  // its events only, then its connection log
  await row.getByRole('button', { name: 'Its events' }).click()
  await expect(page.getByRole('heading', { name: 'Events · Closed' })).toBeVisible()
  await row.getByRole('link', { name: 'Connection log' }).click()
  await expect(page).toHaveURL(new RegExp(`/networks/${bad.id}#log$`))
  await u.api.post(`/api/networks/${bad.id}/disconnect`)
})

test('many networks fit in their tile', async ({ page }) => {
  for (let i = 0; i < 60; i++) await u.api.post('/api/networks', { name: `n${i}`, host: '127.0.0.1', port: 1, nick: u.name })
  const dead = (await u.api.get('/api/networks')).find((n) => n.name === 'n0')
  await u.api.post(`/api/networks/${dead.id}/connect`) // nothing listens there: it keeps retrying
  await login(page, u.name, u.password)
  const tile = page.locator('.tile', { has: page.getByTestId('tile-connected') })
  await expect(tile.locator('.ticks i')).toHaveCount(60)
  await expect(tile).toContainText(/online · 1 reconnecting/, { timeout: 20000 })
  // the ticks, and the Live/Idle chip, stay inside the tile
  const box = await tile.boundingBox()
  for (const el of [tile.locator('.ticks'), tile.locator('.chip')]) {
    const b = await el.boundingBox()
    expect(b.x + b.width).toBeLessThanOrEqual(box.x + box.width + 0.5)
  }
  const next = await page.locator('.tile', { has: page.getByTestId('tile-clients') }).boundingBox()
  expect(box.x + box.width).toBeLessThanOrEqual(next.x)
})
