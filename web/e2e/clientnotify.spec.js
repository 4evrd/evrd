import { test, expect } from '@playwright/test'
import { login, freshUser, addNetwork } from './helpers.js'
import { IRC } from './irc.js'

// clientnotify (ZNC's): turned on in Account → Bouncer, the account's IRC
// clients hear from *status when another one attaches, and goes; "new
// addresses only" keeps quiet for an address seen before.

let me
const clients = []
test.beforeEach(async () => {
  me = await freshUser('cn')
  await addNetwork(me.api, { name: 'Dev', nick: me.name })
})
test.afterEach(async () => {
  while (clients.length) clients.pop().close()
  await me.cleanup()
})

async function attach(version) {
  const c = await IRC.connect({ user: me.name, network: 'Dev', password: me.password, version })
  clients.push(c)
  await c.waitFor(/ 376 /)
  return c
}

const notice = (text) => new RegExp(`^:\\*status!\\S+ PRIVMSG \\S+ :${text}`)
// quiet reports whether no line matching re came to c within ms.
const quiet = (c, re, ms) => c.next(re, ms).then(() => false, () => true)

test('IRC clients hear when another attaches and leaves; new addresses only stays quiet', async ({ page }) => {
  await login(page, me.name, me.password)
  await page.goto('/account#bouncer')
  const b = page.getByRole('form', { name: 'Bouncer settings' })
  const on = b.getByRole('switch', { name: 'Tell my IRC clients when another client connects' })
  await expect(on).toHaveAttribute('aria-checked', 'false')
  await expect(b.getByRole('switch', { name: 'Only from new addresses' })).toHaveCount(0) // only once it's on
  await on.click()
  await b.getByRole('switch', { name: 'When a client disconnects too' }).click()
  await b.getByRole('button', { name: 'Save bouncer settings' }).click()
  await expect(page.getByText('Bouncer settings saved')).toBeVisible()
  expect((await me.api.get('/api/me')).settings).toMatchObject({ clientNotify: true, clientNotifyNewOnly: false, clientNotifyOnDisconnect: true })

  const first = await attach('e2e-client 1.0')
  const second = await attach('e2e-second 2.0')
  // once the newcomer said what it is (its VERSION)
  await first.waitFor(notice('Another client attached to your account: e2e-second from \\S+\\. Clients attached now: 2'), 15000)
  expect(await quiet(second, notice('Another client attached'), 1000)).toBe(true) // not about itself

  const left = first.next(notice('A client left your account: e2e-second from \\S+\\. Clients attached now: 1\\.'), 15000)
  second.close()
  await left

  // *status shows and changes it: new addresses only, and this one's known now
  const reply = first.next(notice('Client notices: on - new addresses only: on - on disconnect: on\\.'))
  first.send('PRIVMSG *status :clientnotify newonly on')
  await reply
  await page.reload()
  await expect(b.getByRole('switch', { name: 'Only from new addresses' })).toHaveAttribute('aria-checked', 'true')
  await attach('e2e-third 3.0')
  expect(await quiet(first, notice('Another client attached'), 6000)).toBe(true)

  // off: nothing at all
  await on.click()
  await b.getByRole('button', { name: 'Save bouncer settings' }).click()
  await expect(page.getByText('Bouncer settings saved')).toBeVisible()
  const fourth = await attach('e2e-fourth 4.0')
  fourth.close()
  expect(await quiet(first, notice('(Another client attached|A client left)'), 6000)).toBe(true)
})
