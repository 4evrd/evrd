import { test, expect } from '@playwright/test'
import { login, freshUser, addNetwork, uniq } from './helpers.js'
import { IRC } from './irc.js'

// Logging in to services on the dev ergo server, which has a real NickServ
// and SASL: an account registered there, then networks set up through the
// web page with each login method. ergo reserves an account's nick, so evrd
// signs in on the alternate nick and IDENTIFY names the account; ergo then
// gives it the account's nick.

let u
const clients = []
test.beforeEach(async ({ page }) => {
  u = await freshUser('ns')
  await login(page, u.name, u.password)
})
test.afterEach(async () => {
  watch = null
  while (clients.length) clients.pop().close()
  await u.cleanup()
})

const status = async (id) => u.api.get(`/api/networks/${id}`)
async function attach(network) {
  const c = await IRC.connect({ user: u.name, network, password: u.password })
  clients.push(c)
  await c.waitFor(/ 376 /)
  return c
}

// watcher is a plain network's client for asking ergo about others.
let watch
async function watcher() {
  if (!watch) {
    await addNetwork(u.api, { name: 'Watch', nick: uniq('w') })
    watch = await attach('Watch')
  }
  return watch
}

// account is who ergo says nick is logged in as ("" for nobody); gone waits
// for nick to leave.
async function account(nick) {
  const w = await watcher()
  const seen = w.lines.length
  w.send(`WHOIS ${nick}`)
  await w.next(new RegExp(` 318 \\S+ ${nick} `, 'i'))
  const m = w.lines.slice(seen).map((l) => l.match(new RegExp(` 330 \\S+ ${nick} (\\S+) `, 'i'))).find(Boolean)
  return m ? m[1] : ''
}
async function gone(nick) {
  const w = await watcher()
  await expect.poll(async () => {
    const seen = w.lines.length
    w.send(`WHOIS ${nick}`)
    await w.next(new RegExp(` 318 \\S+ ${nick} `, 'i'))
    return w.lines.slice(seen).some((l) => / 401 /.test(l))
  }, { timeout: 15000 }).toBe(true)
}

// register makes an ergo account (from a network of its own, removed again
// so the nick is free) and returns its name and password.
async function register() {
  const acct = uniq('acct')
  const pw = `${acct}-pw`
  const reg = await addNetwork(u.api, { name: 'Reg', nick: acct })
  const c = await attach('Reg')
  c.send(`PRIVMSG NickServ :REGISTER ${pw}`)
  await c.waitFor(/NickServ.*Account created/i, 15000)
  c.close()
  await u.api.del(`/api/networks/${reg.id}`)
  await gone(acct)
  return { acct, pw }
}

// regOnly makes a channel only logged-in users may join (ergo's +R), kept
// open by a network of its own. Returns the channel and that client.
async function regOnly() {
  const chan = '#' + uniq('reg')
  await addNetwork(u.api, { name: 'Hold', nick: uniq('hold') })
  const h = await attach('Hold')
  h.send(`JOIN ${chan}`)
  await h.waitFor(new RegExp(`JOIN :?${chan}`, 'i'))
  h.send(`MODE ${chan} +R`)
  await h.waitFor(new RegExp(`MODE ${chan} \\+R`, 'i'))
  return { chan, h }
}

// addViaPage fills the new-network form: the dev ircd, the nick, the
// channels, and the Authentication section (fill gets the page to set it up).
async function addViaPage(page, { name, nick, channels = [], fill }) {
  await page.goto('/networks/new')
  await page.getByRole('textbox', { name: 'Name', exact: true }).fill(name)
  await page.locator('#sv-host-0').fill('ircd')
  const tls = page.locator('.server-row').first().getByRole('switch', { name: 'TLS' })
  if ((await tls.getAttribute('aria-checked')) === 'true') await tls.click()
  await page.locator('#sv-port-0').fill('6667')
  await page.locator('#nick').fill(nick)
  if (channels.length) {
    await page.locator('#newChan').fill(channels.join(' '))
    await page.locator('#newChan').press('Enter')
  }
  await fill()
  await page.getByRole('button', { name: 'Add network' }).last().click()
  await expect(page).toHaveURL(/\/networks\/\d+$/)
  const id = +page.url().split('/').pop()
  await expect.poll(async () => (await status(id)).status, { timeout: 20000 }).toBe('connected')
  return id
}

const log = (page) => page.getByTestId('conn-log')

test('NickServ (Atheme/Anope preset): identifies, then joins a registered-only channel', async ({ page }) => {
  const { acct, pw } = await register()
  const { chan, h } = await regOnly()
  const id = await addViaPage(page, {
    name: 'NS', nick: acct, channels: [chan],
    fill: async () => {
      await page.locator('#authPreset').selectOption('atheme')
      await expect(page.locator('#authMethod')).toHaveValue('nickserv')
      await expect(page.locator('#identifyArgs')).toHaveValue('account')
      await page.locator('#saslUser').fill(acct)
      await page.locator('#saslPass').fill(pw)
      await page.getByRole('switch', { name: 'Join channels after identifying' }).click()
    },
  })
  await expect.poll(async () => (await status(id)).auth, { timeout: 20000 }).toBe('identified')
  // the +R channel let it in: it joined once logged in
  await h.waitFor(new RegExp(`:${acct}!\\S+ JOIN :?${chan}`, 'i'), 15000)
  await expect(log(page)).toContainText(`Identifying to NickServ as ${acct}`, { timeout: 20000 })
  await expect(log(page)).toContainText('Joining channels once services confirm the login')
  await expect(log(page)).toContainText(`Identified to services: You're now logged in as ${acct}`)
  await expect(log(page)).not.toContainText(pw)
  await expect(page.getByTestId('auth-state')).toHaveText('Logged in')
  // settings kept; the password isn't sent back
  const n = await status(id)
  expect(n).toMatchObject({ authMethod: 'nickserv', identifyArgs: 'account', joinAfterAuth: true, hasPassword: true })
  expect(n.password).toBeUndefined()
  await page.reload()
  await expect(page.locator('#authPreset')).toHaveValue('atheme')
  await expect(page.getByRole('switch', { name: 'Join channels after identifying' })).toHaveAttribute('aria-checked', 'true')
})

test('a wrong password: refused, explained, and the registered-only channel stays shut', async ({ page }) => {
  const { acct } = await register()
  const { chan } = await regOnly()
  const id = await addViaPage(page, {
    name: 'Bad', nick: acct, channels: [chan],
    fill: async () => {
      await page.locator('#authMethod').selectOption('nickserv')
      await page.locator('#saslUser').fill(acct)
      await page.locator('#saslPass').fill('not-the-password')
      await page.getByRole('switch', { name: 'Join channels after identifying' }).click()
    },
  })
  await expect.poll(async () => (await status(id)).auth, { timeout: 20000 }).toBe('failed')
  await expect(log(page)).toContainText('Services refused the login: Authentication failed', { timeout: 20000 })
  await expect(log(page)).toContainText('The password was refused. Check the account and password under Authentication')
  await expect(log(page)).toContainText('only lets in people logged in to an account')
  await expect(log(page)).not.toContainText('not-the-password')
  await expect(page.getByTestId('auth-state')).toHaveText('Login refused')
})

test('SASL only, automatic, and a command of my own each log in', async ({ page }) => {
  const { acct, pw } = await register()
  const methods = [
    { name: 'Sasl', set: async () => page.locator('#authPreset').selectOption('sasl'), says: `Logged in with SASL as ${acct}` },
    { name: 'Auto', set: async () => expect(page.locator('#authPreset')).toHaveValue('auto'), says: `Logged in with SASL as ${acct}` },
    {
      name: 'Cmd',
      set: async () => {
        await page.locator('#authMethod').selectOption('command')
        await expect(page.locator('#authPreset')).toHaveValue('custom')
        await page.locator('#authCommand').fill('/msg NickServ IDENTIFY %account% %password%')
      },
      says: `Identified to services: You're now logged in as ${acct}`,
    },
  ]
  for (const m of methods) {
    const id = await addViaPage(page, {
      name: m.name, nick: acct,
      fill: async () => {
        await m.set()
        await page.locator('#saslUser').fill(acct)
        await page.locator('#saslPass').fill(pw)
      },
    })
    await expect.poll(async () => (await status(id)).auth, { timeout: 20000 }).toBe('identified')
    await expect(log(page)).toContainText(m.says, { timeout: 20000 })
    // ergo agrees: WHOIS names the account
    expect((await account((await status(id)).currentNick)).toLowerCase()).toBe(acct.toLowerCase())
    // gone again, so the next one gets the account's nick
    await u.api.del(`/api/networks/${id}`)
    await gone(acct)
  }
})
