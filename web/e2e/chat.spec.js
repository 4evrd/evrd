import { test, expect } from '@playwright/test'
import { login, freshUser, addNetwork, uniq } from './helpers.js'
import { IRC } from './irc.js'

// Two accounts on the dev ircd: "me" uses the web app, "them" is someone else
// in the same channel, typing from a native IRC client attached to evrd.
let me, them, chan, myNet, irc
test.beforeEach(async ({ page }) => {
  chan = '#' + uniq('c')
  me = await freshUser('me')
  them = await freshUser('them')
  myNet = await addNetwork(me.api, { name: 'Dev', nick: me.name, channels: [chan] })
  await addNetwork(them.api, { name: 'Dev', nick: them.name, channels: [chan] })
  irc = await IRC.connect({ user: them.name, network: 'Dev', password: them.password })
  await irc.waitFor(new RegExp(` 366 \\S+ ${chan.replace('#', '\\#')} `, 'i'))
  await login(page, me.name, me.password)
})
test.afterEach(async () => {
  irc?.close()
  await me.cleanup()
  await them.cleanup()
})

const log = (page) => page.locator('.log')
async function openChat(page, buf = chan) {
  await page.goto(`/chat?net=${myNet.id}&buf=${encodeURIComponent(buf)}`)
  await expect(page.locator('.live.on')).toBeVisible({ timeout: 10000 })
  await expect(page.locator('#buf-title')).toHaveText(buf)
  return page.getByRole('textbox', { name: 'Message' })
}
// eventually reloads the page until the check passes (lists refresh on load).
const eventually = (page, check) => expect(async () => { await page.reload(); await check() }).toPass({ timeout: 20000 })

test('messages go both ways, live', async ({ page }) => {
  const input = await openChat(page)
  await input.fill('hello from the web')
  await input.press('Enter')
  await expect(log(page).getByText('hello from the web')).toBeVisible()
  await irc.waitFor(new RegExp(`PRIVMSG ${chan} :hello from the web`, 'i'))

  irc.send(`PRIVMSG ${chan} :hello from irssi`)
  const line = log(page).locator('.msg', { hasText: 'hello from irssi' })
  await expect(line).toBeVisible({ timeout: 10000 })
  await expect(line.locator('.nick')).toHaveText(them.name)
})

test('members and topic', async ({ page }) => {
  const input = await openChat(page)
  const members = page.locator('aside.members')
  await expect(members.getByText(them.name, { exact: true })).toBeVisible({ timeout: 10000 })
  // the first to join is the channel operator
  await expect(members.getByText('Operators')).toBeVisible()
  await expect(page.locator('.log-head .topic')).toHaveText('No topic set')

  await input.fill('/topic set by e2e')
  await input.press('Enter')
  await irc.waitFor(/ TOPIC \S+ :set by e2e/)
  await expect(page.locator('.log-head .topic')).toHaveText('set by e2e', { timeout: 15000 })
})

test('unread lines and mentions are counted until you read them', async ({ page }) => {
  irc.send(`PRIVMSG ${chan} :just chatting`)
  irc.send(`PRIVMSG ${chan} :hey ${me.name}, you there?`)

  // overview: the channel carries a mention, the top bar links to it
  await eventually(page, async () => {
    await expect(page.getByRole('link', { name: 'Mentions — 1 unread' })).toBeVisible({ timeout: 2000 })
  })
  await expect(page.locator('.chan-node', { hasText: chan }).getByLabel('1 mentions')).toBeVisible()
  await expect(page.getByRole('tab', { name: /Mentions 1/ })).toBeVisible()
  await page.getByRole('tab', { name: /Unread/ }).click()
  await expect(page.locator('.chan-node', { hasText: chan })).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'Your bouncer' }).getByRole('link', { name: /Chat\s*1/ })).toBeVisible()

  // the mentions link opens the buffer, marks where you left off and
  // highlights the line that names you
  await page.getByRole('link', { name: 'Mentions — 1 unread' }).click()
  await expect(page.locator('#buf-title')).toHaveText(chan)
  await expect(page.locator('#backlog-mark')).toHaveText(/Missed while away · 2 lines/i)
  await expect(log(page).locator('.msg.highlight')).toContainText('you there?')
  await expect(log(page).locator('.msg.highlight', { hasText: 'just chatting' })).toHaveCount(0)

  // opening it read it
  await eventually(page, async () => {
    await expect(page.getByRole('link', { name: 'Mentions — none' })).toBeVisible({ timeout: 2000 })
  })
  const read = await me.api.get(`/api/networks/${myNet.id}/buffers`)
  expect(read.find((b) => b.name.toLowerCase() === chan.toLowerCase())).toMatchObject({ unread: 0, mentions: 0 })
})

test('mark all as read', async ({ page }) => {
  await openChat(page, '*status')
  irc.send(`PRIVMSG ${chan} :one`)
  irc.send(`PRIVMSG ${chan} :two`)
  irc.send(`PRIVMSG ${chan} :three`)
  const b = page.locator('.buf', { hasText: chan.slice(1) })
  await eventually(page, async () => { await expect(b.getByLabel('3 unread')).toBeVisible({ timeout: 2000 }) })
  await page.getByRole('button', { name: 'Mark all as read' }).click()
  await expect(page.getByText('Marked Dev as read')).toBeVisible()
  await expect(b.locator('.count')).toHaveCount(0)
})

test('private messages open their own buffer', async ({ page }) => {
  await openChat(page)
  irc.send(`PRIVMSG ${me.name} :psst`)
  const q = page.getByRole('navigation', { name: 'Buffers' }).locator('.buf', { hasText: them.name })
  await expect(q).toBeVisible({ timeout: 10000 })
  await expect(q.locator('.count.mention')).toHaveText('1')
  await q.click()
  await expect(page.locator('#buf-title')).toHaveText(them.name)
  await expect(page.locator('.log-head .topic')).toHaveText('Private conversation on Dev')
  await expect(log(page).getByText('psst')).toBeVisible()
  const input = page.getByRole('textbox', { name: 'Message' })
  await input.fill('hi back')
  await input.press('Enter')
  await irc.waitFor(new RegExp(`PRIVMSG ${them.name} :hi back`, 'i'))

  // /msg opens a buffer too, /close shuts it
  await input.fill(`/msg ${them.name} again`)
  await input.press('Enter')
  await irc.waitFor(/:again$/)
  await input.fill('/close')
  await input.press('Enter')
  await expect(page.getByRole('navigation', { name: 'Buffers' }).locator('.buf', { hasText: them.name })).toHaveCount(0)
})

test('chat commands: /join, /me, /nick, /part and events', async ({ page }) => {
  const input = await openChat(page)
  const other = '#' + uniq('j')
  await input.fill(`/join ${other}`)
  await input.press('Enter')
  await expect(page.locator('#buf-title')).toHaveText(other)
  await expect(log(page).getByText(`${me.name} joined`)).toBeVisible({ timeout: 10000 })

  await input.fill('/me waves')
  await input.press('Enter')
  await expect(log(page)).toContainText(`${me.name} waves`)

  await input.fill(`/nick ${me.name}n`)
  await input.press('Enter')
  await expect(log(page).getByText(`${me.name} is now known as ${me.name}n`)).toBeVisible({ timeout: 10000 })

  await input.fill('/help')
  await input.press('Enter')
  await expect(log(page).getByText(/\/join #channel \[key\]/)).toBeVisible()

  await input.fill('/bogus')
  await input.press('Enter')
  await expect(log(page).getByText(/\/bogus isn’t supported/)).toBeVisible()

  await input.fill(`/part ${other} bye`)
  await input.press('Enter')
  await expect(page.locator('.buf', { hasText: other.slice(1) })).toHaveCount(0, { timeout: 10000 })
  // /join and /part are saved to the network
  const n = (await me.api.get('/api/networks')).find((x) => x.id === myNet.id)
  expect(n.channels.map((c) => c.toLowerCase())).not.toContain(other.toLowerCase())
})

test('*status works from the web chat', async ({ page }) => {
  const input = await openChat(page)
  await input.fill('/msg *status help')
  await input.press('Enter')
  await expect(page.locator('#buf-title')).toHaveText('*status')
  await expect(log(page).getByText(/evrd commands/)).toBeVisible({ timeout: 5000 })
  await input.fill('networks')
  await input.press('Enter')
  await expect(log(page).getByText(/\* Dev\s+connected/)).toBeVisible({ timeout: 5000 })
})

test('switching networks', async ({ page }) => {
  await addNetwork(me.api, { name: 'Second', nick: me.name + '2', channels: [chan] })
  await openChat(page)
  await page.getByLabel('Network', { exact: true }).selectOption({ label: 'Second' })
  await expect(page.locator('.net-switch')).toContainText(`${me.name}2 · ircd`)
  await expect(page).toHaveURL(/net=/)
  await page.getByLabel('Network', { exact: true }).selectOption({ label: 'Dev' })
  await expect(page.locator('.net-switch')).toContainText(`${me.name} · ircd`)
})

test('chat with no networks points you at adding one', async ({ page }) => {
  await me.api.del(`/api/networks/${myNet.id}`)
  await page.goto('/chat')
  await expect(page.getByText('No networks yet')).toBeVisible()
  await page.getByRole('link', { name: 'Add network' }).last().click()
  await expect(page.getByRole('form', { name: 'Add a network' })).toBeVisible()
})
