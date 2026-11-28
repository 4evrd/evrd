import { test, expect } from '@playwright/test'
import { freshUser, addNetwork } from './helpers.js'
import { IRC } from './irc.js'

// The autoreply module: "me" has it on and no client attached; "them" sends
// me private messages from a real IRC client (through their own evrd
// network) and gets my reply as a notice — once per interval, and not
// while I'm attached.

let me, them, net, talk
const clients = []
const esc = (s) => s.replace(/[#.*+?^${}()|[\]\\]/g, '\\$&')

test.beforeEach(async () => {
  me = await freshUser('me')
  them = await freshUser('them')
  net = await addNetwork(me.api, { name: 'A', nick: me.name })
  await addNetwork(them.api, { name: 'Dev', nick: them.name })
  talk = await IRC.connect({ user: them.name, network: 'Dev', password: them.password })
  clients.push(talk)
  await talk.waitFor(/ 376 /)
})
test.afterEach(async () => {
  while (clients.length) clients.pop().close()
  await me.cleanup()
  await them.cleanup()
})

const modules = (settings) => me.api.patch(`/api/networks/${net.id}`, { modules: { autoreply: { enabled: true, settings } } })
// replies are the notices from me that talk has had
const replies = () => talk.lines.filter((l) => new RegExp(`^:${esc(me.name)}!\\S+ NOTICE ${esc(them.name)} :`, 'i').test(l))

test('private messages get the reply while nobody is attached, once per interval', async () => {
  await modules({ reply: '%nick% is out, try later %sender%', interval: '5' })
  const reply = talk.next(new RegExp(`NOTICE ${esc(them.name)} :${esc(me.name)} is out, try later ${esc(them.name)}$`, 'i'))
  talk.send(`PRIVMSG ${me.name} :are you there?`)
  await reply

  // again within the five minutes: no second reply
  talk.send(`PRIVMSG ${me.name} :hello??`)
  await new Promise((r) => setTimeout(r, 1500))
  expect(replies()).toHaveLength(1)

  // the connection log says so
  const log = ((await me.api.get(`/api/networks/${net.id}/log`)) || []).map((e) => e.text).join('\n')
  expect(log).toContain(`autoreply: told ${them.name} you're away`)
})

test('no reply while a client is attached; the default reply once it leaves', async () => {
  await modules({ interval: '0' }) // answer every time
  const c = await IRC.connect({ user: me.name, network: 'A', password: me.password })
  clients.push(c)
  await c.waitFor(/ 376 /)

  const got = c.next(/:while you're here$/)
  talk.send(`PRIVMSG ${me.name} :while you're here`)
  await got
  await new Promise((r) => setTimeout(r, 1500))
  expect(replies()).toHaveLength(0)

  c.close()
  await c.closed()
  await new Promise((r) => setTimeout(r, 1000)) // evrd notes it left
  const reply = talk.next(new RegExp(`NOTICE ${esc(them.name)} :I am currently away, try again later$`, 'i'))
  talk.send(`PRIVMSG ${me.name} :and now?`)
  await reply
  // not a CTCP (evrd answers the PING itself: that notice is a CTCP reply)
  talk.send(`PRIVMSG ${me.name} :\x01PING 123\x01`)
  await new Promise((r) => setTimeout(r, 1500))
  expect(replies().filter((l) => !l.includes('\x01'))).toHaveLength(1)
})
