import { test, expect } from '@playwright/test'
import { login, freshUser, addNetwork, uniq, choose } from './helpers.js'
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
  const second = await addNetwork(me.api, { name: 'Second', nick: me.name + '2', channels: [chan] })
  await openChat(page)
  const pick = page.getByLabel('Network', { exact: true })
  await expect(pick).toHaveText(/^\s*Dev\b/)
  await choose(page, pick, 'Second')
  await expect(page.locator('.net-switch')).toContainText(`${me.name}2 · ircd`)
  await expect(page).toHaveURL(/net=/)
  await choose(page, pick, 'Dev')
  await expect(page.locator('.net-switch')).toContainText(`${me.name} · ircd`)

  // a mention on the other network: counted on the picker, and on its option
  await expect.poll(async () => (await me.api.get(`/api/networks/${second.id}`)).channels.length, { timeout: 20000 }).toBeGreaterThan(0)
  irc.send(`PRIVMSG ${chan} :hey ${me.name}2, over here`)
  await expect(pick.locator('.count.mention')).toHaveText('1', { timeout: 20000 })
  await pick.click()
  const opt = page.getByRole('option', { name: /^Second/ })
  await expect(opt.locator('.count.mention')).toHaveText('1')
  await expect(page.getByRole('option', { name: /^Dev/ })).toHaveAttribute('aria-selected', 'true')
  // the keyboard: arrows move, Enter picks, Escape closes
  await page.keyboard.press('Escape')
  await expect(page.getByRole('listbox')).toHaveCount(0)
  await expect(pick).toBeFocused()
  await page.keyboard.press('ArrowDown')
  await expect(page.getByRole('listbox')).toBeVisible()
  await page.keyboard.press('End')
  await expect(pick).toHaveAttribute('aria-activedescendant', /net-pick-opt-1$/)
  await page.keyboard.press('Enter')
  await expect(page.locator('.net-switch')).toContainText(`${me.name}2 · ircd`)
  await expect(pick.locator('.count.mention')).toHaveCount(0) // it's the one you're on now
})

test('the network picker searches and sorts once there are a few networks', async ({ page }) => {
  // three more networks, left switched off, next to the connected Dev
  for (const name of ['Zulu', 'alpha', 'Mike']) await me.api.post('/api/networks', { name, host: 'irc.example.org', port: 6697, tls: true, nick: me.name })
  await openChat(page)
  const pick = page.getByLabel('Network', { exact: true })
  const names = () => page.getByRole('option').locator('.net-opt-name').allTextContents()
  await pick.click()
  const box = page.getByRole('combobox', { name: 'Find a network' })
  await expect(box).toBeFocused()
  expect(await names()).toEqual(['alpha', 'Dev', 'Mike', 'Zulu'])
  // by status: the connected one first
  await page.getByRole('button', { name: 'Status' }).click()
  await expect(page.getByRole('button', { name: 'Status' })).toHaveAttribute('aria-pressed', 'true')
  expect(await names()).toEqual(['Dev', 'alpha', 'Mike', 'Zulu'])
  // typing narrows it (the host matches too), Enter picks
  await box.fill('zul')
  expect(await names()).toEqual(['Zulu'])
  await box.fill('nothing-like-this')
  await expect(page.getByText('No matches')).toBeVisible()
  await box.fill('ZU')
  await box.press('Enter')
  await expect(page.getByRole('listbox')).toHaveCount(0)
  await expect(pick).toContainText('Zulu')
  // the order is remembered; Escape closes and hands focus back
  await page.reload()
  await pick.click()
  await expect(page.getByRole('button', { name: 'Status' })).toHaveAttribute('aria-pressed', 'true')
  expect((await names()).slice(-2)).toEqual(['alpha', 'Mike']) // opening Zulu set it connecting
  await page.keyboard.press('ArrowDown')
  await expect(box).toHaveAttribute('aria-activedescendant', /net-pick-opt-\d$/)
  await page.keyboard.press('Escape')
  await expect(page.getByRole('listbox')).toHaveCount(0)
  await expect(pick).toBeFocused()
})

test('chat with no networks points you at adding one', async ({ page }) => {
  await me.api.del(`/api/networks/${myNet.id}`)
  await page.goto('/chat')
  await expect(page.getByText('No networks yet')).toBeVisible()
  await page.getByRole('link', { name: 'Add network' }).last().click()
  await expect(page.getByRole('form', { name: 'Add a network' })).toBeVisible()
})

test('long names shorten instead of pushing the buffer list into the chat', async ({ page }) => {
  const long = '#' + uniq('a-very-long-channel-name-that-goes-on-and-on-')
  await addNetwork(me.api, { name: 'a-network-with-a-long-name-xyz12', nick: (me.name + 'withaverylongnickname').slice(0, 30), channels: [long] })
  await openChat(page)
  await choose(page, page.getByLabel('Network', { exact: true }), 'a-network-with-a-long-name-xyz12')
  await expect(page.locator('.buf', { hasText: long.slice(1, 20) })).toBeVisible({ timeout: 20000 })
  const fits = await page.evaluate(() => {
    const pane = document.querySelector('.buffers').getBoundingClientRect()
    return [...document.querySelectorAll('.buffers .net-switch, .buffers .buf')].every((e) => e.getBoundingClientRect().right <= pane.right + 0.5)
  })
  expect(fits).toBe(true)
})

test('typing shows both ways between the web chat and an IRC client', async ({ page }) => {
  const input = await openChat(page)
  // the other user types in an IRCv3 client (message-tags)
  const typer = await IRC.connect({ user: them.name, network: 'Dev', password: them.password, caps: ['message-tags'] })
  try {
    await typer.waitFor(new RegExp(` 366 \\S+ ${chan.replace('#', '\\#')} `, 'i'))
    await expect(async () => {
      typer.send(`@+typing=active TAGMSG ${chan}`)
      await expect(page.getByTestId('typing')).toHaveText(`${them.name} is typing…`, { timeout: 2000 })
    }).toPass({ timeout: 20000 })
    // their message ends it
    typer.send(`PRIVMSG ${chan} :there`)
    await expect(log(page).getByText('there', { exact: true })).toBeVisible({ timeout: 10000 })
    await expect(page.getByTestId('typing')).toHaveText('')

    // and this user's typing in the web chat reaches their client
    await input.pressSequentially('hel')
    await typer.waitFor(new RegExp(`^@\\+typing=active :${me.name}!\\S+ TAGMSG ${chan.replace('#', '\\#')}$`, 'i'), 15000)
    await input.fill('')
    await input.press('Backspace')
    await typer.waitFor(new RegExp(`^@\\+typing=done :${me.name}!\\S+ TAGMSG ${chan.replace('#', '\\#')}$`, 'i'), 15000)
  } finally { typer.close() }
})
