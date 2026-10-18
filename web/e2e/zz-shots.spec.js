import { test, expect } from '@playwright/test'
import { login, addNetwork, session, ADMIN, ADMIN_PASS } from './helpers.js'
import { IRC } from './irc.js'

// Not a test: makes a demo account with some chat and takes the README
// screenshots. Run on purpose only: SHOTS=1 npx playwright test zz-shots
test.skip(!process.env.SHOTS, 'screenshots only on request')
test.use({ viewport: { width: 1440, height: 900 }, colorScheme: 'dark' })
test.setTimeout(240000)

// demoUser makes (or remakes) an account with a fixed, readable name.
async function demoUser(name) {
  const a = await session(ADMIN, ADMIN_PASS)
  const old = (await a.get('/api/admin/users')).find((u) => u.username === name)
  if (old) await a.del(`/api/admin/users/${old.id}`)
  const password = `${name}-demo-password`
  const u = await a.post('/api/admin/users', { username: name, password: 'temporary-pw-1' })
  const s = await session(name, 'temporary-pw-1')
  await s.post('/api/me/password', { current: 'temporary-pw-1', next: password })
  await s.dispose()
  const api = await session(name, password)
  return { name, password, api, cleanup: async () => { await api.dispose(); await a.del(`/api/admin/users/${u.id}`).catch(() => {}); await a.dispose() } }
}

test('README screenshots', async ({ page }) => {
  const out = '../docs/images'
  const me = await demoUser('mira')
  const people = [await demoUser('tobias'), await demoUser('anke'), await demoUser('ravi')]
  const clients = []
  try {
    const libera = await addNetwork(me.api, { name: 'libera', nick: me.name, channels: ['#bouncer', '#go-nuts', '#svelte'] })
    await addNetwork(me.api, { name: 'oftc', nick: me.name, channels: ['#debian-next', '#tor-dev'] })
    for (const p of people) {
      await addNetwork(p.api, { name: 'libera', nick: p.name, channels: ['#bouncer', '#go-nuts', '#svelte'] })
      const c = await IRC.connect({ user: p.name, network: 'libera', password: p.password })
      await c.waitFor(/ 366 .*#svelte/i)
      clients.push(c)
    }
    const say = async (i, chan, text) => { clients[i].send(`PRIVMSG ${chan} :${text}`); await new Promise((r) => setTimeout(r, 350)) }
    await say(0, '#bouncer', 'morning! anyone tried the new network editor yet?')
    await say(1, '#bouncer', 'yes — failover across servers is great, it hopped to the second one during the split')
    await say(2, '#bouncer', 'trusted my self-signed server by fingerprint, took two clicks')
    await say(0, '#bouncer', `${me.name}: did your irssi pick up the backlog after the move?`)
    await say(1, '#go-nuts', 'anyone else on 1.26 yet?')
    await say(2, '#go-nuts', 'the new iterator helpers are lovely')
    await say(0, '#svelte', 'runes everywhere now')
    for (let i = 0; i < 12; i++) await say(i % 3, '#go-nuts', ['gofmt or bust', 'benchmarks look good', 'race detector found one', 'lgtm'][i % 4])
    clients[1].send(`PRIVMSG ${me.name} :lunch later?`)

    await page.setViewportSize({ width: 1440, height: 900 })
    await login(page, me.name, me.password)
    await page.waitForTimeout(2500)
    await page.reload()
    await page.waitForTimeout(2500)
    await page.screenshot({ path: `${out}/overview.png` })

    await page.goto(`/chat?net=${libera.id}&buf=%23bouncer`)
    await expect(page.locator('.live.on')).toBeVisible()
    await page.waitForTimeout(1500)
    await page.screenshot({ path: `${out}/chat.png` })

    await page.goto(`/networks/${libera.id}`)
    await page.waitForTimeout(1500)
    await page.screenshot({ path: `${out}/network.png` })
  } finally {
    clients.forEach((c) => c.close())
    for (const u of [me, ...people]) await u.cleanup()
  }
})
