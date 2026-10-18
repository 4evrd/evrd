import { test, expect } from '@playwright/test'
import { login, freshUser, addNetwork, uniq } from './helpers.js'
import { IRC } from './irc.js'

// Real connections to the dev ergo server: SASL against an account
// registered there, its self-signed TLS listener on 6697, and failover.

let u
const clients = []
test.beforeEach(async () => { u = await freshUser('cx') })
test.afterEach(async () => {
  while (clients.length) clients.pop().close()
  await u.cleanup()
})

const status = async (id) => (await u.api.get(`/api/networks/${id}`))
async function attach(network) {
  const c = await IRC.connect({ user: u.name, network, password: u.password })
  clients.push(c)
  await c.waitFor(/ 376 /)
  return c
}

test('SASL logs in to the account on connect', async () => {
  // Register an account on ergo (NickServ) from a plain connection.
  const acct = uniq('acct')
  const reg = await addNetwork(u.api, { name: 'Reg', nick: acct })
  const c = await attach('Reg')
  c.send(`PRIVMSG NickServ :REGISTER ${acct}-pw`)
  await c.waitFor(/NickServ.*Account created/i, 15000)
  c.close()
  await u.api.post(`/api/networks/${reg.id}/disconnect`)

  // A second network logs in with SASL under another nick.
  const nick = acct + 'x'
  const n = await u.api.post('/api/networks', { name: 'Sasl', host: 'ircd', port: 6667, tls: false, nick, saslUser: acct, password: `${acct}-pw` })
  await u.api.post(`/api/networks/${n.id}/connect`)
  await expect.poll(async () => (await status(n.id)).status, { timeout: 20000 }).toBe('connected')
  // (ergo may rename us to the account's nick once logged in)
  const cur = (await status(n.id)).currentNick
  const s = await attach('Sasl')
  s.send(`WHOIS ${cur}`)
  await s.waitFor(new RegExp(` 330 \\S+ ${cur} ${acct} `, 'i'))

  // A wrong password connects unauthenticated, not logged in.
  await u.api.patch(`/api/networks/${n.id}`, { password: 'wrong-password' })
  await expect.poll(async () => (await status(n.id)).status, { timeout: 20000 }).toBe('connected')
  const w = await attach('Sasl')
  w.send(`WHOIS ${(await status(n.id)).currentNick}`)
  const end = await w.waitFor(/ 318 /)
  expect(w.lines.some((l) => / 330 /.test(l))).toBe(false)
  expect(end).toBeTruthy()
})

test('a self-signed server: refused, then trusted by fingerprint from the page', async ({ page }) => {
  const n = await u.api.post('/api/networks', { name: 'Tls', host: 'ircd', port: 6697, tls: true, nick: u.name })
  await u.api.post(`/api/networks/${n.id}/connect`)
  // TrustPKI (the default) refuses ergo's self-signed certificate.
  await expect.poll(async () => (await status(n.id)).untrustedFingerprint || '', { timeout: 20000 }).toMatch(/^[0-9a-f]{64}$/)
  const fp = (await status(n.id)).untrustedFingerprint
  expect((await status(n.id)).lastError).toMatch(/untrusted certificate/)

  await login(page, u.name, u.password)
  await page.goto(`/networks/${n.id}`)
  await expect(page.getByText(/evrd refused .* certificate/)).toBeVisible()
  await expect(page.locator('.fp').first()).toHaveText(fp.toUpperCase().match(/.{2}/g).join(':'))
  await page.getByRole('button', { name: 'Trust this certificate' }).click()
  await expect(page.getByText('Trusted that certificate')).toBeVisible()
  await expect.poll(async () => (await status(n.id)).status, { timeout: 30000 }).toBe('connected')
  expect((await status(n.id)).fingerprints).toEqual([fp])
  await page.reload()
  await expect(page.locator('.page-head').getByText('Connected', { exact: true })).toBeVisible()
  await expect(page.getByText(/evrd refused/)).toHaveCount(0)
})

test('trust-all accepts any certificate', async () => {
  const n = await u.api.post('/api/networks', { name: 'Any', host: 'ircd', port: 6697, tls: true, nick: u.name, trustAllCerts: true })
  await u.api.post(`/api/networks/${n.id}/connect`)
  await expect.poll(async () => (await status(n.id)).status, { timeout: 20000 }).toBe('connected')
  expect((await status(n.id)).server).toBe('ircd:6697')
})

test('failover: a dead first server, then the next one', async ({ page }) => {
  const n = await u.api.post('/api/networks', {
    name: 'Fail', nick: u.name,
    servers: [{ host: 'ircd', port: 1, tls: false }, { host: 'ircd', port: 6667, tls: false }],
  })
  await u.api.post(`/api/networks/${n.id}/connect`)
  await expect.poll(async () => (await status(n.id)).status, { timeout: 30000 }).toBe('connected')
  expect((await status(n.id)).server).toBe('ircd:6667')
  await login(page, u.name, u.password)
  await page.goto(`/networks/${n.id}`)
  await expect(page.locator('.page-head')).toContainText('on ircd:6667')
})

test('detach from an IRC client hides the channel; the web marks it', async () => {
  const chan = '#' + uniq('d')
  const n = await addNetwork(u.api, { name: 'Det', nick: u.name, channels: [chan] })
  const c = await attach('Det')
  await c.waitFor(new RegExp(`JOIN ${chan}`, 'i'))
  c.send(`DETACH ${chan}`)
  await c.waitFor(/PART .*Detached/)
  await expect.poll(async () => (await status(n.id)).channelSettings[chan.toLowerCase()]?.detached).toBe(true)
  // a new client isn't put in it
  const c2 = await attach('Det')
  c2.send('PRIVMSG *status :version')
  await c2.waitFor(/evrd /)
  expect(c2.lines.some((l) => new RegExp(`JOIN ${chan}`, 'i').test(l))).toBe(false)
  c2.send(`JOIN ${chan}`)
  await c2.waitFor(new RegExp(`JOIN ${chan}`, 'i'))
  await expect.poll(async () => (await status(n.id)).channelSettings[chan.toLowerCase()]?.detached ?? false).toBe(false)
})
