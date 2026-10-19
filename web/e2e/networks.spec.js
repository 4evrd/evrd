import { test, expect } from '@playwright/test'
import { login, confirm, freshUser, addNetwork, session, ADMIN, ADMIN_PASS, choose } from './helpers.js'

let u
test.beforeEach(async ({ page }) => {
  u = await freshUser('net')
  await login(page, u.name, u.password)
})
test.afterEach(async () => { await u.cleanup() })

const form = (page) => page.locator('form.sections')
const card = (page, name) => page.getByTestId('network-row').filter({ has: page.getByRole('heading', { name, exact: true }) })
const save = (page) => page.getByRole('button', { name: /^(Save changes|Add network)$/ }).last().click()

// fillBasics fills a new network's name and first server (plain, on the dev ircd).
async function fillBasics(page, name, { host = 'ircd', port = '6667' } = {}) {
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill(name)
  await page.locator('#sv-host-0').fill(host)
  const tls = page.locator('.server-row').first().getByRole('switch', { name: 'TLS' })
  if ((await tls.getAttribute('aria-checked')) === 'true') await tls.click()
  await page.locator('#sv-port-0').fill(port)
}

test('add a network on its own page: it connects and shows on the list', async ({ page }) => {
  await page.goto('/networks')
  await page.getByRole('link', { name: 'Add network' }).first().click()
  await expect(page).toHaveURL(/\/networks\/new$/)
  await expect(page.getByRole('heading', { name: 'Add a network' })).toBeVisible()
  await expect(page.locator('#nick')).toHaveValue(u.name) // from the account default
  await fillBasics(page, 'Dev')
  await page.locator('#newChan').fill('#e2e, #more')
  await page.locator('#newChan').press('Enter')
  await expect(page.getByTestId('chan-row')).toHaveCount(2)
  await save(page)
  await expect(page.getByText('Added Dev — connecting…')).toBeVisible()
  await expect(page).toHaveURL(/\/networks\/\d+$/)
  await expect(page.getByRole('heading', { level: 1, name: 'Dev' })).toBeVisible()
  await expect(page.locator('.page-head').getByText('Connected', { exact: true })).toBeVisible({ timeout: 20000 })

  await page.getByRole('link', { name: 'Networks' }).first().click()
  const row = card(page, 'Dev')
  await expect(row.getByText('Connected', { exact: true })).toBeVisible()
  await expect(page.getByTestId('net-count')).toHaveText('1 of ∞ used')
})

test('the form explains what is wrong before saving', async ({ page }) => {
  await page.goto('/networks/new')
  await page.locator('#nick').fill('')
  await save(page)
  await expect(page.getByText('Give it a name so you can tell it apart.')).toBeVisible()
  await expect(page.getByText(/Enter the server address/)).toBeVisible()
  await expect(page.getByText('Pick a nickname.')).toBeVisible()
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill('has space')
  await page.locator('#sv-host-0').fill('not a host!')
  await page.locator('#sv-port-0').fill('99999')
  await save(page)
  await expect(page.getByText(/No spaces, \/ or :/)).toBeVisible()
  await expect(page.getByText(/doesn’t look like a hostname/)).toBeVisible()
  await expect(page.getByText(/Ports are whole numbers/)).toBeVisible()
  await page.locator('#newChan').fill('nohash')
  await page.getByRole('button', { name: 'Add', exact: true }).last().click()
  await expect(page.getByText(/isn’t a channel/)).toBeVisible()
  await expect(page).toHaveURL(/\/networks\/new$/)
})

test('several servers: add, reorder, remove — saved in order', async ({ page }) => {
  const n = await addNetwork(u.api, { name: 'Multi', nick: u.name })
  await page.goto(`/networks/${n.id}`)
  await page.getByRole('button', { name: 'Add a server' }).click()
  await page.locator('#sv-host-1').fill('irc.example.org')
  // a new server starts like the first one: plain, port 6667
  await expect(page.locator('#sv-port-1')).toHaveValue('6667')
  await page.locator('#sv-pass-1').fill('server-secret')
  await page.getByRole('button', { name: 'Add a server' }).click()
  await page.locator('#sv-host-2').fill('irc3.example.org')
  await page.getByRole('button', { name: 'Move server 3 up' }).click()
  await expect(page.locator('#sv-host-1')).toHaveValue('irc3.example.org')
  await save(page)
  await expect(page.getByText(/Saved Multi/)).toBeVisible()
  const saved = await u.api.get(`/api/networks/${n.id}`)
  expect(saved.servers.map((s) => s.host)).toEqual(['ircd', 'irc3.example.org', 'irc.example.org'])
  expect(saved.servers[2]).toMatchObject({ port: 6667, tls: false, hasPassword: true })

  await page.reload()
  await expect(page.locator('#sv-pass-2')).toHaveAttribute('placeholder', /unchanged/)
  await page.getByRole('button', { name: 'Remove server 2' }).click()
  await save(page)
  await expect.poll(async () => (await u.api.get(`/api/networks/${n.id}`)).servers.length).toBe(2)
  expect((await u.api.get(`/api/networks/${n.id}`)).servers[1]).toMatchObject({ host: 'irc.example.org', hasPassword: true })
})

test('identity, SASL, certificates and advanced settings are saved', async ({ page }) => {
  const n = await addNetwork(u.api, { name: 'Full', nick: u.name })
  await page.goto(`/networks/${n.id}`)
  await page.locator('#altNick').fill(u.name + '_alt')
  await page.locator('#username').fill('myident')
  await page.locator('#realname').fill('E2E Person')
  await page.locator('#quitMsg').fill('off to bed')
  await page.locator('#saslUser').fill('myaccount')
  await page.locator('#saslPass').fill('sasl-secret')
  await page.getByRole('switch', { name: 'Verify with certificate authorities' }).click()
  const fp = 'AB:'.repeat(31) + 'CD'
  await page.locator('#newFp').fill('not-a-fingerprint')
  await page.getByRole('button', { name: 'Add', exact: true }).first().click()
  await expect(page.getByText(/64 hex digits/).first()).toBeVisible()
  await page.locator('#newFp').fill(fp)
  await page.locator('#newFp').press('Enter')
  await expect(page.getByText(fp)).toBeVisible()
  await choose(page, page.getByLabel('Encoding'), 'windows-1252')
  await page.getByLabel('Lines per second').fill('1.5')
  await page.getByLabel('Burst').fill('5')
  await page.getByLabel('Join delay').fill('3')
  await save(page)
  await expect(page.getByText(/Saved Full/)).toBeVisible()

  const s = await u.api.get(`/api/networks/${n.id}`)
  expect(s).toMatchObject({
    altNick: u.name + '_alt', username: 'myident', realname: 'E2E Person', quitMsg: 'off to bed',
    saslUser: 'myaccount', hasPassword: true, trustPKI: false, encoding: '^windows-1252',
    floodRate: 1.5, floodBurst: 5, joinDelay: 3,
  })
  expect(s.fingerprints).toEqual(['ab'.repeat(31) + 'cd'])

  // the trust-everything switch warns
  await page.getByRole('switch', { name: 'Trust every certificate' }).click()
  await expect(page.getByText(/Certificates aren’t checked for this network/)).toBeVisible()
  // the services password can be cleared
  await page.getByRole('button', { name: 'Clear', exact: true }).last().click()
  await expect(page.getByText('Will be removed: evrd won’t log in.')).toBeVisible()
})

test('channels: keys, detached and join-on-connect', async ({ page }) => {
  const n = await addNetwork(u.api, { name: 'Chans', nick: u.name, channels: ['#one', '#two'] })
  await page.goto(`/networks/${n.id}`)
  await page.getByLabel('Key for #one').fill('sekrit')
  await page.getByRole('switch', { name: 'Detach #two' }).click()
  await page.getByRole('switch', { name: 'Join #one on connect' }).click()
  await page.getByRole('button', { name: 'Remove #two' }).click()
  await page.locator('#newChan').fill('#three')
  await page.locator('#newChan').press('Enter')
  await save(page)
  await expect(page.getByText(/Saved Chans/)).toBeVisible()
  const s = await u.api.get(`/api/networks/${n.id}`)
  expect(s.channels).toEqual(['#one', '#three'])
  expect(s.channelSettings).toEqual({ '#one': { key: 'sekrit', disabled: true } })
})

test('switch a network off and on from the list', async ({ page }) => {
  await addNetwork(u.api, { name: 'Flip', nick: u.name })
  await page.goto('/networks')
  const row = card(page, 'Flip')
  await row.getByRole('switch', { name: 'Disconnect Flip' }).click()
  await expect(page.getByText('Disconnected from Flip. Settings kept.')).toBeVisible()
  await expect(row.getByText('Disconnected', { exact: true })).toBeVisible({ timeout: 10000 })
  await row.getByRole('switch', { name: 'Connect Flip' }).click()
  await expect(row.getByText('Connected', { exact: true })).toBeVisible({ timeout: 20000 })
})

test('remove a network from its page', async ({ page }) => {
  const n = await addNetwork(u.api, { name: 'Temp', nick: u.name })
  await page.goto(`/networks/${n.id}`)
  await page.getByRole('button', { name: 'Remove Temp' }).click()
  await confirm(page, 'Temp')
  await expect(page.getByText('Removed Temp')).toBeVisible()
  await expect(page).toHaveURL(/\/networks$/)
  await expect(page.getByTestId('network-row')).toHaveCount(0)
})

test('the server refuses a duplicate name', async ({ page }) => {
  await addNetwork(u.api, { name: 'Dup', nick: u.name })
  await page.goto('/networks/new')
  await fillBasics(page, 'Dup')
  await save(page)
  await expect(page.locator('#form-err')).toContainText('already have a network called Dup')
})

test('search and filter the list', async ({ page }) => {
  await addNetwork(u.api, { name: 'Alpha', nick: u.name })
  const b = await addNetwork(u.api, { name: 'Bravo', nick: u.name + 'b' })
  await u.api.post(`/api/networks/${b.id}/disconnect`)
  await page.goto('/networks')
  await expect(page.getByTestId('network-row')).toHaveCount(2)
  await page.locator('#page-search').fill('alp')
  await expect(page.getByTestId('network-row')).toHaveCount(1)
  await page.locator('#page-search').fill('zzz')
  await expect(page.getByText('No networks match')).toBeVisible()
  await page.getByRole('button', { name: 'Clear filters' }).click()
  const chips = page.getByRole('group', { name: 'Filter by status' })
  await chips.getByRole('button', { name: /^Off/ }).click()
  await expect(page.getByTestId('network-row')).toHaveCount(1)
  await expect(card(page, 'Bravo')).toBeVisible()
  await page.goto('/networks?filter=connected')
  await expect(page.getByTestId('network-row')).toHaveCount(1)
  await expect(card(page, 'Alpha')).toBeVisible()
})

test('old deep links and the chat settings button lead to the page', async ({ page }) => {
  const n = await addNetwork(u.api, { name: 'Deep', nick: u.name })
  await page.goto(`/networks#edit-${n.id}`)
  await expect(page).toHaveURL(new RegExp(`/networks/${n.id}$`))
  await page.goto('/networks#add')
  await expect(page).toHaveURL(/\/networks\/new$/)
  await page.goto(`/chat?net=${n.id}`)
  await page.getByRole('link', { name: 'Network settings' }).click()
  await expect(page.getByRole('heading', { level: 1, name: 'Deep' })).toBeVisible()
})

test('a network that can’t connect says why, and the connection log shows what happened', async ({ page }) => {
  {
    // one that works, one pointed at a port nothing listens on
    const ok = await addNetwork(u.api, { name: 'Dev', nick: u.name })
    const bad = await u.api.post('/api/networks', { name: 'Closed', host: 'ircd', port: 6699, tls: false, nick: u.name })
    await u.api.post(`/api/networks/${bad.id}/connect`)

    await page.goto(`/networks/${bad.id}`)
    const problem = page.getByTestId('conn-problem')
    await expect(problem).toContainText('connection refused', { timeout: 20000 })
    await expect(problem).toContainText('nothing listens on that port')
    const log = page.getByTestId('conn-log')
    await expect(log).toContainText('Switched on')
    await expect(log).toContainText('Connecting to ircd port 6699 without TLS')
    await expect(log.locator('li.bad', { hasText: "Couldn't connect: connection refused" })).not.toHaveCount(0)
    await expect(log).toContainText('Trying again in')

    // the networks list says why too, and links to the log
    await page.goto('/networks')
    const closed = card(page, 'Closed')
    await expect(closed).toContainText('nothing listens on that port')
    await closed.getByRole('link', { name: 'Connection log' }).click()
    await expect(page).toHaveURL(new RegExp(`/networks/${bad.id}#log$`))

    // a working network's log: the sign-in, the server's side, no passwords
    await page.goto(`/networks/${ok.id}`)
    const okLog = page.getByTestId('conn-log')
    await expect(okLog).toContainText('Signed in as', { timeout: 20000 })
    await expect(okLog.locator('li.server')).not.toHaveCount(0)
    await expect(okLog).not.toContainText('PASS ')
    await expect(page.getByTestId('conn-problem')).toHaveCount(0)
  }
})

test('modules: switch perform on, give it a command, and it runs at sign-in', async ({ page }) => {
  const chan = '#perf' + Date.now().toString(36).slice(-5)
  const n = await u.api.post('/api/networks', { name: 'Mods', host: 'ircd', port: 6667, tls: false, nick: u.name })
  await page.goto(`/networks/${n.id}`)
  const perform = page.getByTestId('module-perform')
  await expect(perform).toContainText('ZNC perform')
  await perform.getByRole('switch').click()
  await perform.getByLabel('Commands').fill(`/join ${chan}\n/mode %nick% +i`)
  await save(page)
  await expect(page.getByText('Saved Mods')).toBeVisible()
  const saved = await u.api.get(`/api/networks/${n.id}`)
  expect(saved.modules.perform).toEqual({ enabled: true, settings: { commands: `/join ${chan}\n/mode %nick% +i` } })

  // it runs when the network signs in
  await u.api.post(`/api/networks/${n.id}/connect`)
  await expect.poll(async () => (await u.api.get(`/api/networks/${n.id}`)).channels, { timeout: 20000 }).toContain(chan)
  await page.reload()
  await expect(page.getByTestId('conn-log')).toContainText('perform: sent 2 commands', { timeout: 20000 })
  await expect(page.getByTestId('module-perform').getByRole('switch')).toBeChecked()
})

test('modules an admin switches off are unavailable', async ({ page }) => {
  const admin = await session(ADMIN, ADMIN_PASS)
  try {
    await admin.patch('/api/admin/settings', { set: { modules_off: 'autovoice' } })
    const n = await u.api.post('/api/networks', { name: 'Off', host: 'ircd', port: 6667, tls: false, nick: u.name })
    // requests are spread over the stack's two nodes, and the one the admin
    // didn't use picks the change up at its next settings poll (5s)
    await expect(async () => {
      await page.goto(`/networks/${n.id}`)
      await expect(page.getByTestId('module-autovoice')).toContainText('Switched off by an admin', { timeout: 1000 })
    }).toPass({ timeout: 20000 })
    await expect(page.getByTestId('module-autovoice').getByRole('switch')).toHaveCount(0)
    await expect(page.getByTestId('module-autoop').getByRole('switch')).toHaveCount(1)
  } finally {
    await admin.patch('/api/admin/settings', { reset: ['modules_off'] })
    await admin.dispose()
  }
})
