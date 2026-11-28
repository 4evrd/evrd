import { test, expect } from '@playwright/test'
import { freshUser, addNetwork, uniq } from './helpers.js'
import { IRC } from './irc.js'

// The autoattach module from a real IRC client: "me" detaches a channel and
// switches autoattach on with *status; "them" talks there from their own
// evrd network. A line matching the rule brings the channel back, with what
// was said, the way *status attach does.

let me, them, chan, net
const clients = []
// played-back lines carry their time in front (clients without server-time)
const ts = '(\\[[\\d:]+\\] )?'
const esc = (s) => s.replace(/[#.*+?^${}()|[\]\\]/g, '\\$&')

test.beforeEach(async () => {
  chan = '#' + uniq('aa')
  me = await freshUser('me')
  them = await freshUser('them')
  net = await addNetwork(me.api, { name: 'A', nick: me.name, channels: [chan] })
  await addNetwork(them.api, { name: 'Dev', nick: them.name, channels: [chan] })
  await expect.poll(async () => (await me.api.get(`/api/networks/${net.id}`)).channels.length, { timeout: 20000 }).toBeGreaterThan(0)
})
test.afterEach(async () => {
  while (clients.length) clients.pop().close()
  await me.cleanup()
  await them.cleanup()
})

async function attach(user, network) {
  const c = await IRC.connect({ user: user.name, network, password: user.password })
  clients.push(c)
  await c.waitFor(/ 376 /)
  return c
}

// cmd sends a *status command and waits for a reply matching text.
function cmd(c, line, text) {
  const reply = c.next(new RegExp(`^:\\*status!\\S+ PRIVMSG \\S+ :.*${esc(text)}`))
  c.send(`PRIVMSG *status :${line}`)
  return reply
}

test('a matching line in a detached channel attaches it, with what was said', async () => {
  const talk = await attach(them, 'Dev')
  await talk.waitFor(new RegExp(` 366 \\S+ ${esc(chan)} `, 'i'))
  const a = await attach(me, 'A')
  await a.waitFor(new RegExp(` 366 \\S+ ${esc(chan)} `, 'i'))

  await cmd(a, 'loadmod autoattach', 'autoattach is on for A')
  await cmd(a, `modset autoattach rules ${chan} *wake*`, "Set autoattach's")
  const parted = a.next(new RegExp(`PART ${esc(chan)} :Detached`, 'i'))
  a.send(`PRIVMSG *status :detach ${chan}`)
  await parted

  // a line that doesn't match: still detached, nothing shown
  talk.send(`PRIVMSG ${chan} :just chatter`)
  await expect.poll(async () => (await me.api.get(`/api/networks/${net.id}`)).channelSettings[chan.toLowerCase()]?.detached).toBe(true)
  await new Promise((r) => setTimeout(r, 1000))
  expect(a.lines.some((l) => l.endsWith(':just chatter'))).toBe(false)

  // one that does: the JOIN, then the line (in the playback), once
  const from = a.lines.length
  const joined = a.next(new RegExp(`^:${esc(me.name)}!\\S+ JOIN :?${esc(chan)}$`, 'i'))
  const said = a.next(new RegExp(`:${ts}please wake up$`))
  talk.send(`PRIVMSG ${chan} :please wake up`)
  await joined
  await said
  await expect.poll(async () => (await me.api.get(`/api/networks/${net.id}`)).channelSettings[chan.toLowerCase()]?.detached ?? false).toBe(false)
  await cmd(a, `attach ${chan}`, `${chan} isn't detached.`)
  expect(a.lines.slice(from).filter((l) => new RegExp(`:${ts}please wake up$`).test(l))).toHaveLength(1)

  // and it's live again
  talk.send(`PRIVMSG ${chan} :back with us`)
  await a.waitFor(/:back with us$/)
})

test('a ! rule keeps a channel detached', async () => {
  const talk = await attach(them, 'Dev')
  await talk.waitFor(new RegExp(` 366 \\S+ ${esc(chan)} `, 'i'))
  await me.api.patch(`/api/networks/${net.id}`, {
    modules: { autoattach: { enabled: true, settings: { rules: `${chan}\n!${chan} *spam*` } } },
  })
  const a = await attach(me, 'A')
  await a.waitFor(new RegExp(` 366 \\S+ ${esc(chan)} `, 'i'))
  const parted = a.next(new RegExp(`PART ${esc(chan)} :Detached`, 'i'))
  a.send(`DETACH ${chan}`) // ZNC's client command
  await parted
  const from = a.lines.length

  talk.send(`PRIVMSG ${chan} :buy spam now`)
  await new Promise((r) => setTimeout(r, 1500))
  expect(a.lines.slice(from).some((l) => / JOIN /.test(l) || /buy spam now$/.test(l))).toBe(false)
  expect((await me.api.get(`/api/networks/${net.id}`)).channelSettings[chan.toLowerCase()]?.detached).toBe(true)

  const joined = a.next(new RegExp(` JOIN :?${esc(chan)}$`, 'i'))
  talk.send(`PRIVMSG ${chan} :anything else`)
  await joined
  await a.waitFor(new RegExp(`:${ts}anything else$`))
})
