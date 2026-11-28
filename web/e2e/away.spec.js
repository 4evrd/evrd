import { test, expect } from '@playwright/test'
import { login, freshUser, addNetwork, choose } from './helpers.js'
import { IRC } from './irc.js'

// The account's own auto-away message (ZNC simple_away's reason): set in
// Account → Profile and from *status, it's what the network shows others
// (WHOIS's 301) while no client is attached.

let me, them
const clients = []
test.beforeEach(async () => {
  me = await freshUser('aw')
  them = await freshUser('awt')
})
test.afterEach(async () => {
  while (clients.length) clients.pop().close()
  await me.cleanup()
  await them.cleanup()
})

async function attach(user, network, opts = {}) {
  const c = await IRC.connect({ user: user.name, network, password: user.password, ...opts })
  clients.push(c)
  await c.waitFor(/ 376 /)
  return c
}

// away is what WHOIS says about nick's away, asked from c ('' when not away).
async function away(c, nick) {
  const seen = c.lines.length
  c.send(`WHOIS ${nick}`)
  await c.next(new RegExp(` 318 \\S+ ${nick} `, 'i'))
  const hit = c.lines.slice(seen).find((l) => new RegExp(` 301 \\S+ ${nick} :`, 'i').test(l))
  return hit ? hit.replace(/^.* 301 \S+ \S+ :/, '') : ''
}

test('the away message set in the web app is what the network shows, and *status changes it', async ({ page }) => {
  await login(page, me.name, me.password)
  await page.goto('/account')
  await page.getByRole('switch', { name: /Mark me away/ }).click()
  await choose(page, page.getByTestId('away-delay'), 'As soon as the last client goes')
  const msg = page.getByLabel('Away message')
  await expect(msg).toHaveAttribute('placeholder', 'Away - evrd is keeping my seat')
  await msg.fill('Gone fishing since %awaytime%')
  await page.getByRole('button', { name: 'Save profile' }).click()
  await expect(page.getByText('Profile saved')).toBeVisible()
  await page.reload()
  await expect(page.getByLabel('Away message')).toHaveValue('Gone fishing since %awaytime%')
  expect(await me.api.get('/api/me')).toMatchObject({ autoAway: true, autoAwayDelay: 0, awayMessage: 'Gone fishing since %awaytime%' })
  await page.goto('about:blank')

  // a network with nothing attached: away with the message, the time filled in
  await addNetwork(me.api, { name: 'Dev', nick: me.name })
  await addNetwork(them.api, { name: 'Dev', nick: them.name })
  const peer = await attach(them, 'Dev')
  await expect.poll(() => away(peer, me.name), { timeout: 20000 }).toMatch(/^Gone fishing since \w{3} \w{3} [ \d]\d \d\d:\d\d:\d\d \d{4}$/)

  // *status shows it and changes it; it's used the next time the account is away
  const c = await attach(me, 'Dev')
  const reply = (re) => c.next(new RegExp(`^:\\*status!\\S+ PRIVMSG \\S+ :${re}`))
  let r = reply('Your auto-away message: Gone fishing since %awaytime%')
  c.send('PRIVMSG *status :awaymessage')
  await r
  r = reply('Auto-away message set: Back after lunch\\.')
  c.send('PRIVMSG *status :awaymessage Back after lunch')
  await r
  await expect.poll(() => away(peer, me.name), { timeout: 10000 }).toBe('') // a client is attached
  c.close()
  await c.closed()
  await expect.poll(() => away(peer, me.name), { timeout: 20000 }).toBe('Back after lunch')
  expect((await me.api.get('/api/me')).awayMessage).toBe('Back after lunch')
})
