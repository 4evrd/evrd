import { test, expect } from '@playwright/test'
import { login, confirm, freshUser, addNetwork, uniq } from './helpers.js'
import { IRC } from './irc.js'

let u, net
const clients = []
test.beforeEach(async ({ page }) => {
  u = await freshUser('cli')
  net = await addNetwork(u.api, { name: 'Dev', nick: u.name, channels: ['#' + uniq('k')] })
  await login(page, u.name, u.password)
})
test.afterEach(async () => {
  while (clients.length) clients.pop().close()
  await u.cleanup()
})
async function attach(opts) {
  const c = await IRC.connect({ user: u.name, network: 'Dev', password: u.password, ...opts })
  clients.push(c)
  return c
}
const attached = (page) => page.locator('section[aria-labelledby="live-h"]')

test('connection settings and setup guides', async ({ page }) => {
  await page.goto('/clients')
  await expect(page.getByTestId('irc-host')).toHaveText('localhost')
  await expect(page.getByText('6697 (TLS) · 6667 (plain)')).toBeVisible()
  await expect(page.locator('.kv', { hasText: 'Server password' })).toContainText(`${u.name}/network:password`)
  await expect(page.getByText('TLS available')).toBeVisible()

  const snippet = page.locator('pre.snippet')
  await expect(snippet).toContainText(`localhost/6697`)
  await expect(snippet).toContainText(`${u.name}/Dev:PASSWORD`)
  await page.getByRole('tab', { name: 'irssi' }).click()
  await expect(snippet).toContainText(`/server add -tls -network evrd-Dev -password ${u.name}/Dev:PASSWORD localhost 6697`)
  await page.getByRole('tab', { name: 'WeeChat' }).click()
  await expect(snippet).toContainText(`/set irc.server.evrd.password "${u.name}/Dev:PASSWORD"`)
  await page.getByRole('tab', { name: 'Other' }).click()
  await expect(page.getByText('Any client that can send a server password')).toBeVisible()

  // sign-in by link is on (public_url is set): an account with a password
  // starts on the password, and can switch the guide to the link
  await expect(page.getByText('Or leave the password out: *status sends a link')).toBeVisible()
  const method = page.getByTestId('signin-method')
  await expect(method.getByRole('tab', { name: 'A password' })).toHaveAttribute('aria-selected', 'true')
  await method.getByRole('tab', { name: 'A link (no password)' }).click()
  await expect(page.getByTestId('pass-format')).toHaveText(`${u.name}/network`)
  await expect(page.getByTestId('link-steps')).toContainText('open the link *status sends you')
  await page.getByRole('tab', { name: 'WeeChat' }).click()
  await expect(snippet).toContainText(`/set irc.server.evrd.password "${u.name}/Dev"`)
})

test('an SSO account is told it can connect without a password', async ({ page }) => {
  await page.route('**/api/me', async (route) => {
    if (route.request().method() !== 'GET') return route.continue()
    const res = await route.fetch()
    await route.fulfill({ response: res, json: { ...(await res.json()), hasPassword: false } })
  })
  await page.route('**/api/config', async (route) => {
    const res = await route.fetch()
    await route.fulfill({ response: res, json: { ...(await res.json()), sso: true, ssoLabel: 'Authentik', ircLinkSignIn: true } })
  })
  await page.goto('/clients')
  const notice = page.getByTestId('link-notice')
  await expect(notice).toContainText('You can connect without a password.')
  await expect(notice).toContainText(`${u.name}/Dev`)
  await expect(page.getByTestId('sso-notice')).toHaveCount(0)
  // the guide starts on the link: no password in the snippets
  await expect(page.getByTestId('signin-method').getByRole('tab', { name: 'A link (no password)' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.locator('pre.snippet')).toContainText(`Password        ${u.name}/Dev`)
  await expect(page.locator('pre.snippet')).not.toContainText('PASSWORD')
})

test('client passwords: shown once, used to attach, revoked', async ({ page }) => {
  await page.goto('/clients')
  await expect(page.getByText('No client passwords yet')).toBeVisible()
  await page.getByRole('button', { name: 'New client password' }).first().click()
  // validation
  await page.getByRole('button', { name: 'Create password' }).click()
  await expect(page.getByText('Give it a name so you can tell it apart.')).toBeVisible()
  await page.getByLabel('Device name').fill('Laptop')
  await page.getByRole('button', { name: 'Create password' }).click()
  await expect(page.getByTestId('new-secret')).toBeVisible()
  const secret = await page.locator('#pw-value').inputValue()
  expect(secret.length).toBeGreaterThanOrEqual(16)
  // the guide fills in the real password while it's on screen
  await expect(page.locator('pre.snippet')).toContainText(`${u.name}/Dev:${secret}`)
  await page.getByRole('button', { name: 'Done' }).click()
  await expect(page.getByText(/Created .* · used never/)).toBeVisible()

  // attach with it: the client shows up, named by its CTCP VERSION reply
  const c = await attach({ password: secret, version: 'HexChat 2.16.2 / Linux' })
  await c.waitFor(/ 001 /)
  await expect(async () => {
    await page.reload()
    await expect(attached(page).getByText('HexChat · Laptop')).toBeVisible({ timeout: 2000 })
  }).toPass({ timeout: 20000 })
  await expect(page.locator('.row', { hasText: 'Laptop' }).getByText(/used just now|used \d+ (second|minute)/)).toBeVisible()

  await page.getByRole('button', { name: 'Revoke Laptop' }).click()
  await confirm(page)
  await expect(page.getByText('Revoked Laptop')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Revoke Laptop' })).toHaveCount(0)
  // a new attach with the revoked password doesn't get in: it's a long
  // secret evrd doesn't know, so it's offered a sign-in link (only the
  // owner, signed in on the website, could allow it)
  const refused = await attach({ password: secret })
  await refused.waitFor(/Sign in on the website to connect: /)
  await expect.poll(() => refused.lines.some((l) => / JOIN /.test(l))).toBe(false)
})

test('a native client gets backlog and stays in sync with the web', async ({ page }) => {
  const them = await freshUser('peer')
  try {
    const chan = net.channels?.[0] || (await u.api.get('/api/networks'))[0].channels[0]
    await addNetwork(them.api, { name: 'Dev', nick: them.name, channels: [chan] })
    const peer = await IRC.connect({ user: them.name, network: 'Dev', password: them.password })
    clients.push(peer)
    await peer.waitFor(/ 366 /)
    peer.send(`PRIVMSG ${chan} :said while you were away`)
    await expect.poll(async () => (await u.api.get(`/api/networks/${net.id}/messages?target=${encodeURIComponent(chan)}`))?.length ?? 0, { timeout: 10000 }).toBeGreaterThan(0)

    // attaching plays back what was missed
    const c = await attach()
    await c.waitFor(/PRIVMSG \S+ :(\[[^\]]*\] )?said while you were away/)

    // what the native client says shows in the web chat, and vice versa
    await page.goto(`/chat?net=${net.id}&buf=${encodeURIComponent(chan)}`)
    await expect(page.locator('.live.on')).toBeVisible({ timeout: 10000 })
    c.send(`PRIVMSG ${chan} :typed in irssi`)
    await expect(page.locator('.log').getByText('typed in irssi')).toBeVisible({ timeout: 10000 })
    const input = page.getByRole('textbox', { name: 'Message' })
    await input.fill('typed on the web')
    await input.press('Enter')
    await c.waitFor(/PRIVMSG \S+ :typed on the web/)
    await peer.waitFor(/PRIVMSG \S+ :typed on the web/)
  } finally { await them.cleanup() }
})

test('attach errors', async () => {
  const bad = await attach({ password: 'wrong-password' })
  await bad.waitFor(/ 464 .*Invalid credentials/)
  const unknown = await attach({ network: 'Nope' })
  await unknown.waitFor(/ 464 .*Unknown network Nope/)
})

test('the client limit is enforced', async ({ page }) => {
  await u.admin.patch(`/api/admin/users/${u.id}`, { limits: { maxNetworks: null, maxClients: 1, backlogLines: null } })
  const first = await attach()
  await first.waitFor(/ 001 /)
  const second = await attach()
  await second.waitFor(/ 464 .*Too many clients attached \(limit 1\)/)
  await page.goto('/clients')
  await expect(page.locator('#live-h + .chip')).toHaveText('1 of 1')
})

test('an SSO account is told its SSO password won’t work in IRC clients', async ({ page }) => {
  // e2e has no SSO provider: answer as an account that signs in with one
  await page.route('**/api/me', async (route) => {
    if (route.request().method() !== 'GET') return route.continue()
    const res = await route.fetch()
    await route.fulfill({ response: res, json: { ...(await res.json()), hasPassword: false } })
  })
  await page.route('**/api/config', async (route) => {
    const res = await route.fetch()
    await route.fulfill({ response: res, json: { ...(await res.json()), sso: true, ssoLabel: 'Authentik', ircLinkSignIn: false } })
  })
  await page.goto('/clients')
  const notice = page.getByTestId('sso-notice')
  await expect(notice).toContainText('Your Authentik password won’t work in an IRC client.')
  await expect(notice).toContainText(`${u.name}/Dev:client-password`)
  await notice.getByRole('button', { name: 'Create one' }).click()
  await expect(page.getByRole('dialog', { name: 'New client password' })).toBeVisible()

  // with a password of its own there's nothing to warn about
  await page.unrouteAll({ behavior: 'ignoreErrors' })
  await page.goto('/clients')
  await expect(page.getByRole('heading', { name: 'Point your client here' })).toBeVisible()
  await expect(page.getByTestId('sso-notice')).toHaveCount(0)
})
