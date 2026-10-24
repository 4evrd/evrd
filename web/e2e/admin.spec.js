import { test, expect } from '@playwright/test'
import { ADMIN, ADMIN_PASS, login, confirm, freshUser, session, addNetwork, uniq, BASE, choose } from './helpers.js'
import { IRC } from './irc.js'

test.beforeEach(async ({ page }) => { await login(page, ADMIN, ADMIN_PASS) })

// a user's admin page
const userPage = async (page, u) => { await page.goto(`/admin/users/${u.id}`); await expect(page.getByTestId('user-title')).toHaveText(u.name ?? u.username) }
const title = (page) => page.getByTestId('user-title')
const row = (page, name) => page.getByTestId('user-row').filter({ hasText: name })

test('create a user: validation, duplicates, then they must choose a password', async ({ page, browser }) => {
  const name = uniq('new')
  await page.getByRole('link', { name: 'Users', exact: true }).click()
  await page.getByRole('button', { name: 'New user' }).click()
  const nd = page.locator('#new-drawer')
  await page.getByRole('button', { name: 'Create user' }).click()
  await expect(nd.getByText(/Use 2–32 lowercase letters/)).toBeVisible()
  await expect(nd.getByText('Use at least 10 characters.')).toBeVisible()

  await nd.getByLabel('Username').fill(ADMIN)
  await nd.getByLabel('Temporary password').fill('temporary-pass')
  await page.getByRole('button', { name: 'Create user' }).click()
  await expect(nd.getByText('That username is taken — pick another.')).toBeVisible()

  await nd.getByLabel('Username').fill(name)
  await page.getByRole('button', { name: 'Create user' }).click()
  await expect(page.getByText(`Created ${name}`)).toBeVisible()
  await expect(row(page, name)).toContainText('User')
  await expect(row(page, name)).toContainText('Offline')

  // the new user signs in and has to pick their own password first
  const ctx = await browser.newContext({ baseURL: BASE })
  const p = await ctx.newPage()
  await p.goto('/')
  await p.getByRole('textbox', { name: 'Username' }).fill(name)
  await p.getByLabel('Password', { exact: true }).fill('temporary-pass')
  await p.getByRole('button', { name: 'Sign in' }).click()
  await expect(p.getByRole('heading', { name: 'Choose your own password' })).toBeVisible()
  await p.locator('#s-cur').fill('temporary-pass')
  await p.locator('#s-new').fill('their-own-password')
  await p.locator('#s-rep').fill('their-own-password')
  await p.getByRole('button', { name: 'Change password' }).click()
  await expect(p.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible()
  await ctx.close()

  await row(page, name).click()
  await expect(title(page)).toHaveText(name)
  await page.getByRole('button', { name: 'Delete' }).click()
  await confirm(page, name)
  await expect(page.getByText(`Deleted ${name}`)).toBeVisible()
  await expect(page).toHaveURL(/\/admin\/users$/)
  await expect(row(page, name)).toHaveCount(0)
})

test('reset a password: a one-time password the user must change', async ({ page, browser }) => {
  const u = await freshUser('rst')
  try {
    await userPage(page, u)
    await page.getByRole('button', { name: 'Reset' }).click()
    await confirm(page)
    const temp = await page.getByLabel('Temporary password').inputValue()
    expect(temp.length).toBeGreaterThanOrEqual(10)
    // the old password stops working
    await expect(session(u.name, u.password)).rejects.toThrow(/401/)
    const ctx = await browser.newContext({ baseURL: BASE })
    const p = await ctx.newPage()
    await p.goto('/')
    await p.getByRole('textbox', { name: 'Username' }).fill(u.name)
    await p.getByLabel('Password', { exact: true }).fill(temp)
    await p.getByRole('button', { name: 'Sign in' }).click()
    await expect(p.getByRole('heading', { name: 'Choose your own password' })).toBeVisible()
    await ctx.close()
  } finally { await u.cleanup() }
})

test('suspend and restore a user', async ({ page }) => {
  const u = await freshUser('sus')
  try {
    await addNetwork(u.api, { name: 'Dev', nick: u.name })
    await userPage(page, u)
    await expect(page.locator('.net-card', { hasText: 'Dev' })).toContainText('Connected')
    await page.getByRole('button', { name: 'Suspend' }).click()
    await expect(page.locator('dialog.confirm[open]')).toContainText(`Suspend ${u.name}?`)
    await confirm(page)
    await expect(page.getByText(`Suspended ${u.name}`)).toBeVisible()
    await expect(page.locator('.page-head')).toContainText('Suspended')
    // they're signed out and can't sign back in
    expect((await u.api.ctx.get('/api/me')).status()).toBe(401)
    await expect(session(u.name, u.password)).rejects.toThrow()
    // the overview flags them
    await page.goto('/admin')
    await expect(page.locator('.attn-row', { hasText: `${u.name} is suspended` })).toBeVisible()
    await page.locator('.attn-row', { hasText: `${u.name} is suspended` }).click()
    await expect(title(page)).toHaveText(u.name)
    await page.getByRole('button', { name: 'Restore' }).click()
    await expect(page.getByText(`Restored ${u.name}`)).toBeVisible()
    await expect(page.locator('.page-head')).not.toContainText('Suspended')
    await session(u.name, u.password).then((s) => s.dispose())
  } finally { await u.cleanup() }
})

test('per-user limits override the server defaults', async ({ page }) => {
  const u = await freshUser('lim')
  try {
    await userPage(page, u)
    const d = page.locator('#manage')
    await d.getByLabel('Max networks').fill('x')
    await page.getByRole('button', { name: 'Save limits' }).click()
    await expect(d.getByText(/Enter a whole number/)).toBeVisible()
    await d.getByLabel('Max networks').fill('1')
    await d.getByLabel('Max clients').fill('2')
    await d.getByLabel('Backlog lines').fill('500')
    await page.getByRole('button', { name: 'Save limits' }).click()
    await expect(page.getByText(`Saved limits for ${u.name}`)).toBeVisible()
    await page.goto('/admin/users')
    await expect(row(page, u.name)).toContainText('0 / 1')
    await expect(row(page, u.name)).toContainText('0 / 2')
    expect((await u.api.get('/api/me')).limits).toMatchObject({ maxNetworks: 1, maxClients: 2, backlogLines: 500 })

    // the limit bites: a second network is refused
    await addNetwork(u.api, { name: 'One', nick: u.name })
    await expect(u.api.post('/api/networks', { name: 'Two', host: 'ircd', port: 6667, tls: false, nick: u.name })).rejects.toThrow()

    // empty goes back to the default
    await row(page, u.name).click()
    await expect(title(page)).toHaveText(u.name)
    await expect(d.getByLabel('Max networks')).toHaveValue('1')
    await d.getByLabel('Max networks').fill('')
    await d.getByLabel('Max clients').fill('')
    await d.getByLabel('Backlog lines').fill('')
    await page.getByRole('button', { name: 'Save limits' }).click()
    await expect(page.getByText(`Saved limits for ${u.name}`)).toBeVisible()
    await page.goto('/admin/users')
    await expect(row(page, u.name)).toContainText('1 / ∞')
  } finally { await u.cleanup() }
})

test('make someone an admin and back', async ({ page }) => {
  const u = await freshUser('mk')
  try {
    await userPage(page, u)
    const sw = page.getByRole('switch', { name: 'Administrator' })
    await sw.click()
    await expect(page.getByText(`${u.name} is now an admin`)).toBeVisible()
    await expect(page.locator('.page-head')).toContainText('Admin')
    expect((await u.api.get('/api/me')).role).toBe('admin')
    await sw.click()
    await expect(page.getByText(`${u.name} is no longer an admin`)).toBeVisible()
    expect((await u.api.get('/api/me')).role).toBe('user')
  } finally { await u.cleanup() }
})

test('you can’t suspend or delete yourself here', async ({ page }) => {
  await page.goto(`/admin/users?user=${ADMIN}`)
  await expect(title(page)).toHaveText(ADMIN)
  await expect(page).toHaveURL(/\/admin\/users\/\d+$/)
  await expect(page.getByRole('button', { name: 'Suspend' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Delete' })).toHaveCount(0)
})

test('search, filter and deep links on the users list', async ({ page }) => {
  const a = await freshUser('flt', { admin: true })
  const b = await freshUser('flt')
  try {
    await page.goto('/admin/users')
    await page.locator('#page-search').fill(a.name)
    await expect(page.getByTestId('user-row')).toHaveCount(1)
    await page.locator('#page-search').fill('flt')
    await expect(page.getByTestId('user-row')).toHaveCount(2)
    await page.getByRole('button', { name: 'Admins', exact: true }).click()
    await expect(page.getByTestId('user-row')).toHaveCount(1)
    await expect(row(page, a.name)).toBeVisible()
    await page.getByRole('button', { name: 'Suspended', exact: true }).click()
    await expect(page.getByText('No users match')).toBeVisible()
    await page.getByRole('button', { name: 'All', exact: true }).click()

    // keyboard: Enter opens a row
    await row(page, b.name).focus()
    await page.keyboard.press('Enter')
    await expect(title(page)).toHaveText(b.name)

    // old drawer links land on the user's page
    await page.goto(`/admin/users?id=${b.id}`)
    await expect(title(page)).toHaveText(b.name)
    await expect(page).toHaveURL(new RegExp(`/admin/users/${b.id}$`))
    await page.goto('/admin/users#new')
    await expect(page.locator('#new-drawer').getByLabel('Username')).toBeVisible()
  } finally { await a.cleanup(); await b.cleanup() }
})

test('bouncer overview: tiles, placement, upstreams and storage', async ({ page }) => {
  const u = await freshUser('ov')
  try {
    await addNetwork(u.api, { name: 'Dev', nick: u.name })
    await page.getByRole('link', { name: 'Overview' }).nth(1).click()
    await expect(page.getByRole('heading', { name: 'Bouncer overview' })).toBeVisible()
    const all = (await (await session(ADMIN, ADMIN_PASS)).get('/api/admin/users')).length
    await expect(page.getByTestId('ov-users')).toHaveText(String(all))
    await expect(page.getByText(/nodes? holding/)).toBeVisible()
    // every placed account hangs off a node; the new one is there, linked
    const node = page.locator('.user-node', { hasText: u.name })
    await expect(node).toContainText('1/1 links')
    await page.getByRole('tab', { name: /Online/ }).click()
    await expect(page.locator('.user-node', { hasText: u.name })).toHaveCount(0)
    await page.getByRole('tab', { name: /All/ }).click()
    await expect(page.locator('.up-row', { hasText: 'ircd' })).toContainText('All up')
    await expect(page.locator('.kv', { hasText: 'Accounts' }).last()).toContainText('PostgreSQL')
    await expect(page.locator('.kv', { hasText: 'Chat history' })).toContainText('ScyllaDB')
    await expect(page.locator('.kv', { hasText: 'Networks per user' })).toContainText('∞')
    // the software: evrd's version, and no holder on the clustered nodes
    const { version } = await (await fetch(BASE + '/api/config')).json()
    await expect(page.getByTestId('sw-evrd')).toHaveText(version)
    await expect(page.getByTestId('sw-holder')).toHaveText('Not set up (links_socket)')
    await expect(page.getByTestId('evrd-version')).toHaveText(`evrd ${version}`)
    await node.click()
    await expect(title(page)).toHaveText(u.name)
  } finally { await u.cleanup() }
})

test('a user’s page: their overview, networks and channels, clients and sign-ins', async ({ page }) => {
  const u = await freshUser('upg')
  const chan = '#' + uniq('upg')
  let c
  try {
    await addNetwork(u.api, { name: 'Dev', nick: u.name, channels: [chan] })
    await expect.poll(async () => (await u.api.get('/api/networks'))[0].status, { timeout: 20000 }).toBe('connected')
    c = await IRC.connect({ user: u.name, network: 'Dev', password: u.password, version: 'pageclient 2.0' })
    await c.waitFor(/ 001 /)

    await page.goto('/admin/users')
    await row(page, u.name).click()
    await expect(page).toHaveURL(new RegExp(`/admin/users/${u.id}$`))
    await expect(title(page)).toHaveText(u.name)
    await expect(page.locator('.page-head')).toContainText('Online')

    // the same overview they see: tiles and networks → channels
    await expect(page.getByTestId('tile-networks')).toContainText('1')
    await expect(page.getByTestId('tile-connected')).toContainText('1')
    await expect(page.locator('.net-node', { hasText: 'Dev' })).toBeVisible()
    await expect(page.locator('.chan-node', { hasText: chan })).toBeVisible()
    // an admin looking isn't sent into the user's chat
    expect(await page.locator('.chan-node', { hasText: chan }).evaluate((e) => e.tagName)).toBe('DIV')

    // networks with their channels
    const net = page.locator('#networks .net-card', { hasText: 'Dev' })
    await expect(net).toContainText('Connected')
    await expect(net).toContainText(u.name)
    await expect(net.locator('.chan-chip', { hasText: chan.slice(1) })).toBeVisible()

    // attached clients and web sign-ins
    const clients = page.locator('#clients')
    await expect(clients).toContainText('pageclient', { timeout: 20000 })
    await expect(clients).toContainText('Dev')
    await expect(clients.getByText('Web sign-ins')).toBeVisible()
    await expect(clients.locator('.row', { hasText: /password/ })).not.toHaveCount(0)

    // the network's anchor from the overview lands on its card
    await page.locator('.net-node', { hasText: 'Dev' }).click()
    await expect(page).toHaveURL(/#net-\d+$/)

    // signing them out of the web app
    await clients.getByRole('button', { name: 'Sign out everywhere' }).click()
    await confirm(page)
    await expect(page.getByText(`Signed ${u.name} out of the web app`)).toBeVisible()
    expect((await u.api.ctx.get('/api/me')).status()).toBe(401)
    await expect(clients).toContainText('pageclient') // IRC stays attached
  } finally { c?.close(); await u.cleanup() }
})

test('a user’s overview and metrics aren’t open to non-admins', async () => {
  const u = await freshUser('nadm')
  try {
    await expect(u.api.get(`/api/admin/users/${u.id}/overview`)).rejects.toThrow(/403/)
    await expect(u.api.get(`/api/admin/users/${u.id}/reconnects`)).rejects.toThrow(/403/)
    await expect(u.api.get('/api/admin/metrics')).rejects.toThrow(/403/)
  } finally { await u.cleanup() }
})

test('metrics: every chart, ranges and the busiest networks', async ({ page }) => {
  await page.getByRole('link', { name: 'Metrics' }).click()
  await expect(page.getByRole('heading', { name: 'Metrics', exact: true })).toBeVisible()
  for (const id of ['messages', 'links', 'clients', 'lag', 'connects', 'logins', 'cluster']) {
    await expect(page.getByTestId(`metric-${id}`)).toBeVisible()
  }
  // every node reports its users from the start (links only once some
  // test has added a network): there's something to draw once the first
  // export (every 15s) has landed
  await expect(async () => {
    await page.reload()
    await expect(page.getByTestId('metric-cluster').locator('svg path.line')).not.toHaveCount(0, { timeout: 3000 })
  }).toPass({ timeout: 60000 })
  await expect(page.getByTestId('metric-cluster').locator('.legend')).toContainText('node-')
  await page.getByRole('tab', { name: '7 days' }).click()
  await expect(page.getByRole('tab', { name: '7 days' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByTestId('metric-messages')).toContainText('per hour')
  await page.getByRole('tab', { name: '6 hours' }).click()
  await expect(page.getByTestId('metric-messages')).toContainText('per 5 min')
  await expect(page.getByRole('heading', { name: 'Busiest networks' })).toBeVisible()
})

test('a user’s page lists their latest web sign-ins, the rest behind a button', async ({ page }) => {
  const u = await freshUser('ses')
  const extra = []
  try {
    for (let i = 0; i < 6; i++) extra.push(await session(u.name, u.password))
    await page.goto(`/admin/users/${u.id}`)
    const signins = page.locator('#clients .row', { hasText: /· password/ })
    await expect(signins).toHaveCount(5)
    await page.getByRole('button', { name: /Show all \d+/ }).click()
    await expect(signins).not.toHaveCount(5)
    expect(await signins.count()).toBeGreaterThanOrEqual(7)
    await page.getByRole('button', { name: 'Show fewer' }).click()
    await expect(signins).toHaveCount(5)
  } finally { for (const s of extra) await s.dispose(); await u.cleanup() }
})

test('admins see why a user’s network can’t connect, and its connection log', async ({ page }) => {
  const u = await freshUser('alog')
  try {
    const bad = await u.api.post('/api/networks', { name: 'Closed', host: 'ircd', port: 6699, tls: false, nick: u.name })
    await u.api.post(`/api/networks/${bad.id}/connect`)
    await page.goto(`/admin/users/${u.id}`)
    const net = page.locator('#networks .net-card', { hasText: 'Closed' })
    await expect(net.getByTestId('net-problem')).toContainText('connection refused', { timeout: 30000 })
    await expect(net.getByTestId('net-problem')).toContainText('nothing listens on that port')
    await net.getByRole('button', { name: 'Connection log' }).click()
    const log = net.getByTestId('conn-log')
    await expect(log).toContainText('Connecting to ircd port 6699 without TLS')
    await expect(log.locator('li.bad', { hasText: "Couldn't connect: connection refused" })).not.toHaveCount(0)
    await net.getByRole('button', { name: 'Connection log' }).click()
    await expect(log).toHaveCount(0)

    // the user's reconnects, as they see them, linked to the network's card
    const rc = page.locator('#reconnects').getByTestId('reconnects-net').filter({ hasText: 'Closed' })
    await expect(async () => {
      await page.reload()
      await expect(rc).toContainText('connection refused', { timeout: 2000 })
    }).toPass({ timeout: 20000 })
    await expect(page.locator('#reconnects').getByTestId('reconnects-events')).toContainText("Closed: couldn't connect")
    await rc.getByRole('link', { name: 'Connection log' }).click()
    await expect(page).toHaveURL(new RegExp(`#net-${bad.id}$`))
  } finally { await u.cleanup() }
})

test('IRC client drops across every account: why, how long, and which devices', async ({ page }) => {
  const u = await freshUser('drop')
  try {
    await addNetwork(u.api, { name: 'Dev', nick: u.name, channels: ['#' + uniq('d')] })
    const c = await IRC.connect({ user: u.name, network: 'Dev', password: u.password })
    await c.next(/ 376 /)
    c.send('QUIT :bye')
    await c.closed()

    await page.goto('/admin')
    await page.getByRole('link', { name: 'Drops' }).click()
    await expect(page.getByRole('heading', { level: 1, name: 'IRC client drops' })).toBeVisible()
    const mine = () => page.getByTestId('drops-row').filter({ hasText: u.name })
    await expect(async () => {
      await page.reload()
      await expect(mine()).toBeVisible({ timeout: 2000 })
    }).toPass({ timeout: 20000 })
    await expect(page.getByTestId('reason-quit')).toContainText('Left on purpose: the user or the client said QUIT.')
    await expect(page.getByTestId('drops-gaps')).toContainText('not back')
    // over time: a column per hour, stacked by reason
    await expect(page.getByTestId('drops-chart')).toBeVisible()
    await expect(page.getByTestId('drops-time')).toContainText('quit')
    // every reason is listed, even ones that didn't happen
    await expect(page.locator('[data-testid^="reason-"]')).toHaveCount(9)

    // by a reason, then over 30 days, then on to the user
    await choose(page, page.getByTestId('drops-sort').getByRole('combobox'), 'quit')
    await expect(mine()).toContainText('1 / 1')
    await page.getByRole('tab', { name: '30 days' }).click()
    await expect(page.getByRole('tab', { name: '30 days' })).toHaveAttribute('aria-selected', 'true')
    await expect(mine()).toBeVisible()
    // one user: their name narrows the page to their devices
    await mine().getByRole('link', { name: u.name }).click()
    await expect(page).toHaveURL(/\/admin\/drops\?user=\d+/)
    await expect(page.getByTestId('drops-only')).toContainText(`Only ${u.name}’s devices.`)
    await expect(page.getByTestId('drops-row')).toHaveCount(1)
    // and on to their account, whose page lists their drops too
    await page.getByTestId('drops-only').getByRole('link', { name: 'Open their account' }).click()
    await expect(page.getByTestId('user-title')).toHaveText(u.name)
    await expect(page.getByTestId('user-drops')).toContainText('quit')
    await page.getByTestId('user-drops').getByRole('link', { name: 'See all their drops' }).click()
    await expect(page.getByTestId('drops-only')).toBeVisible()
  } finally { await u.cleanup() }
})

test('users can’t see every account’s drops', async ({ page }) => {
  const u = await freshUser('nodrop')
  try {
    expect(await u.api.get('/api/admin/client-drops').then(() => 'ok', (e) => e.message)).toMatch(/403/)
    await page.context().clearCookies() // signed in as the admin before each test
    await login(page, u.name, u.password)
    await page.goto('/admin/drops')
    await expect(page.getByText('Only admins can see every account’s drops.')).toBeVisible()
  } finally { await u.cleanup() }
})
