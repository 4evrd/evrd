import { test, expect } from '@playwright/test'
import { freshUser, uniq } from './helpers.js'
import { IRC } from './irc.js'

// Coming back after the link drops, on a network whose services are a
// server of their own (the dev stack's InspIRCd linked to Anope): logging
// in takes a round trip over the link, so a JOIN sent right behind the
// IDENTIFY gets there first. evrd must still end up in a registered-only
// (+R) channel, with the ops ChanServ gives the account, and in an
// oper-only (+O) channel when perform opers it up. Each is checked from a
// client attached to evrd, after an oper KILLs evrd's connection.

const INSP = { host: 'inspircd', port: 6667, tls: false }
const OPER = 'OPER evrd e2e-oper-pw' // deploy/inspircd/inspircd.conf

// each signs in a few networks, waits on services and reconnects
test.setTimeout(120000)

let u
const clients = []
let nets = []
test.beforeEach(async () => { u = null; nets = []; u = await freshUser('rj') })
test.afterEach(async ({}, info) => {
  // on failure, what each network's connection log and attached client saw
  if (info.status !== info.expectedStatus) {
    for (const n of nets) {
      const log = await u.api.get(`/api/networks/${n.id}/log`).catch(() => [])
      const body = (log || []).map((e) => `${e.at} ${e.from}: ${e.text}`).join('\n')
      console.log(`--- connection log of ${n.name}\n${body}`)
      await info.attach(`log ${n.name}`, { body })
    }
    for (const c of clients) {
      console.log(`--- client on ${c.network} (last 40 lines)\n${c.lines.slice(-40).join('\n')}`)
      await info.attach(`client ${c.network}`, { body: c.lines.join('\n') })
    }
  }
  while (clients.length) clients.pop().close()
  await u?.cleanup()
})

// network adds a network on InspIRCd and waits for it to connect.
async function network(body) {
  const n = await u.api.post('/api/networks', { ...INSP, ...body })
  nets.push(n)
  await u.api.post(`/api/networks/${n.id}/connect`)
  await expect.poll(async () => (await u.api.get(`/api/networks/${n.id}`)).status, { timeout: 30000 }).toBe('connected')
  return n
}
async function attach(name) {
  const c = await IRC.connect({ user: u.name, network: name, password: u.password })
  c.network = name
  clients.push(c)
  await c.waitFor(/ 376 /, 15000)
  return c
}
const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
const joinOf = (nick, chan) => new RegExp(`^:${esc(nick)}!\\S+ JOIN :?${esc(chan)}( |$)`, 'i')
// Back in: a client that kept the channel open while the link was down
// (the account's quiet rejoin, the default) is told "Back on …" in it; one
// that was taken out gets the JOIN again. A refusal takes it out for good.
const backIn = (nick, chan) => new RegExp(`(^:${esc(nick)}!\\S+ JOIN :?${esc(chan)}( |$)| NOTICE ${esc(chan)} :Back on | (47[1-7]|520) \\S+ ${esc(chan)} )`, 'i')

// names asks c for chan's NAMES and returns them.
async function names(c, chan) {
  const seen = c.lines.length
  c.send(`NAMES ${chan}`)
  await c.next(new RegExp(` 366 \\S+ ${esc(chan)} `, 'i'))
  return c.lines.slice(seen).filter((l) => / 353 /.test(l)).map((l) => l.replace(/^.* :/, '')).join(' ').split(' ')
}

// oper is an opered client on its own network: it registers channels and
// KILLs. Anope only links a few seconds after InspIRCd starts, so wait for
// NickServ.
async function oper() {
  const nick = uniq('op')
  await network({ name: 'Op', nick })
  const c = await attach('Op')
  c.send(OPER)
  await c.waitFor(/ 381 /)
  await expect(async () => {
    const seen = c.lines.length
    c.send('PRIVMSG NickServ :HELP')
    await c.next(/^:NickServ!/i, 3000)
    expect(c.lines.length).toBeGreaterThan(seen)
  }).toPass({ timeout: 90000 })
  return { nick, c }
}

// account registers a NickServ account from a network of its own, removed
// again so the nick is free (opered: Anope's registration delay doesn't
// apply).
async function account() {
  const acct = uniq('acct')
  const pw = `${acct}-pw`
  const reg = await network({ name: 'Reg', nick: acct })
  const c = await attach('Reg')
  c.send(OPER)
  await c.waitFor(/ 381 /)
  c.send(`PRIVMSG NickServ :REGISTER ${pw} ${acct}@evrd.test`)
  await c.waitFor(/NickServ.*registered/i, 15000)
  c.close()
  await u.api.del(`/api/networks/${reg.id}`)
  return { acct, pw }
}

// killed KILLs nick and waits for evrd to be back.
async function killed(op, nick, id) {
  op.c.send(`KILL ${nick} :e2e: the link drops`)
  await expect.poll(async () => (await u.api.get(`/api/networks/${id}`)).status, { timeout: 15000 }).not.toBe('connected')
  await expect.poll(async () => (await u.api.get(`/api/networks/${id}`)).status, { timeout: 30000 }).toBe('connected')
}

const logText = async (id) => ((await u.api.get(`/api/networks/${id}/log`)) || []).map((e) => e.text).join('\n')

test('NickServ over a services link: back in a +R channel, with ChanServ auto-op, after a reconnect', async () => {
  const op = await oper()
  const { acct, pw } = await account()
  // the oper's own account founds the channel and gives acct auto-op
  op.c.send(`PRIVMSG NickServ :REGISTER ${op.nick}-pw ${op.nick}@evrd.test`)
  await op.c.waitFor(/NickServ.*registered/i, 15000)
  const chan = '#' + uniq('reg')
  op.c.send(`JOIN ${chan}`)
  await op.c.waitFor(joinOf(op.nick, chan))
  op.c.send(`PRIVMSG ChanServ :REGISTER ${chan}`)
  await op.c.waitFor(/ChanServ.*registered/i, 15000)
  op.c.send(`PRIVMSG ChanServ :ACCESS ${chan} ADD ${acct} 5`)
  await op.c.waitFor(/ChanServ.*added to .*access list/i, 15000)
  op.c.send(`MODE ${chan} +R`)
  await op.c.waitFor(new RegExp(`MODE ${esc(chan)} :?\\+R`, 'i'))

  // IDENTIFY, not SASL: SASL logs in before registration ends
  const n = await network({ name: 'NS', nick: acct, authMethod: 'nickserv', identifyArgs: 'account', saslUser: acct, password: pw, channels: [chan] })
  await expect.poll(async () => (await u.api.get(`/api/networks/${n.id}`)).auth, { timeout: 30000 }).toBe('identified')
  const c = await attach('NS')
  await c.waitFor(joinOf(acct, chan), 15000)

  await expect.poll(() => names(c, chan), { timeout: 15000 }).toContain('@' + acct)

  const back = c.next(backIn(acct, chan), 45000)
  await killed(op, acct, n.id)
  expect(await back).not.toMatch(/ (47[1-7]|520) /)
  // opped by ChanServ: as the attached client and the server see it
  await expect.poll(() => names(c, chan), { timeout: 15000 }).toContain('@' + acct)
  expect(await names(op.c, chan)).toContain('@' + acct)
  expect(await logText(n.id)).not.toContain(pw)
})

test('perform OPERs up before the joins: back in an oper-only channel after a reconnect', async () => {
  const op = await oper()
  const chan = '#' + uniq('opers')
  op.c.send(`JOIN ${chan}`)
  await op.c.waitFor(joinOf(op.nick, chan))
  op.c.send(`MODE ${chan} +O`)
  await op.c.waitFor(new RegExp(`MODE ${esc(chan)} :?\\+O`, 'i'))

  const nick = uniq('o')
  const n = await network({
    name: 'Oper', nick, channels: [chan],
    modules: { perform: { enabled: true, settings: { commands: OPER } } },
  })
  const c = await attach('Oper')
  await c.waitFor(joinOf(nick, chan), 15000)

  const back = c.next(backIn(nick, chan), 45000)
  await killed(op, nick, n.id)
  expect(await back).not.toMatch(/ (47[1-7]|520) /)
  expect(await names(op.c, chan)).toContain(nick)
  const log = await logText(n.id)
  expect(log).not.toContain('e2e-oper-pw')
  expect(log).not.toMatch(/ 520 |only.*operators/i)
})

test('services that never answer: the channels are joined after the wait, once', async () => {
  const op = await oper()
  const chan = '#' + uniq('plain')
  const nick = uniq('s')
  // "services" here is the oper's client, which says nothing
  const n = await network({ name: 'Silent', nick, authMethod: 'nickserv', servicesNick: op.nick, password: 'never-answered', authTimeout: 3, channels: [chan] })
  await expect.poll(async () => (await u.api.get(`/api/networks/${n.id}`)).auth, { timeout: 20000 }).toBe('unconfirmed')
  const c = await attach('Silent')
  await c.waitFor(joinOf(nick, chan), 15000)
  const log = await logText(n.id)
  expect(log).toContain("Services didn't answer the login within")
  // one JOIN went out, after the wait
  await expect.poll(() => op.c.lines.filter((l) => /PRIVMSG \S+ :IDENTIFY/.test(l)).length).toBe(1)
})
