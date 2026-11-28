import { test, expect } from '@playwright/test'
import { execSync } from 'node:child_process'
import net from 'node:net'
import { IRC } from './irc.js'
import { session, uniq, SOLO_URL, SOLO_IRC_PORT, SOLO_EVRCTL } from './helpers.js'

// bouncedcc: DCC chats and file transfers relayed through evrd. Two users of
// evrd-solo (deploy/evrd.solo.conf sets dcc_public 127.0.0.1 and its ports,
// published by docker-compose.dev.yml), each with bouncedcc on, on the dev
// ircd. Their raw IRC clients only ever connect to evrd: the one offering
// asks for a passive DCC (it can't listen), the other's evrd turns that into
// an ordinary offer for its client, and the two evrd sides connect to each
// other. Neither client sees the other's address.

test.describe.configure({ timeout: 120_000 })

// Where the test reaches evrd-solo's DCC ports (the address in the offers).
const DCC_HOST = process.env.E2E_DCC_HOST || '127.0.0.1'
const LOCALHOST = 2130706433 // 127.0.0.1, as DCC writes it

// A brand-new account is placed on the node at its next heartbeat; until
// then evrd answers 503 "moving" — retry like the web app does.
async function retry(f, timeout = 30000) {
  const end = Date.now() + timeout
  for (;;) {
    try { return await f() } catch (e) {
      if (Date.now() > end || !/503/.test(String(e))) throw e
      await new Promise((r) => setTimeout(r, 1000))
    }
  }
}

// soloUser makes an account on evrd-solo with a network on the dev ircd,
// bouncedcc on, and attaches a raw IRC client to it.
async function soloUser(prefix) {
  const name = uniq(prefix)
  const password = `${name}-secret-pw`
  execSync(`${SOLO_EVRCTL} user add ${name} ${password}`, { stdio: 'ignore' })
  const api = await session(name, password, SOLO_URL)
  const nick = name.slice(0, 15)
  const n = await retry(() => api.post('/api/networks', { name: 'Dev', host: 'ircd', port: 6667, tls: false, nick }))
  await api.patch(`/api/networks/${n.id}`, { modules: { bouncedcc: { enabled: true } } })
  await retry(() => api.post(`/api/networks/${n.id}/connect`))
  await expect.poll(async () => (await api.get(`/api/networks/${n.id}`)).status, { timeout: 30000 }).toBe('connected')
  // 127.0.0.1, not localhost: the DCC connections must come from where the
  // client attached from, or evrd turns them away
  const irc = await IRC.connect({ user: name, network: 'Dev', password, host: DCC_HOST, port: SOLO_IRC_PORT })
  await irc.waitFor(new RegExp(` 001 ${nick} `), 15000)
  return { name, nick, api, irc, net: n }
}

// offerTo waits for a DCC offer from someone, and returns its parts.
async function offerTo(c, from, type, name) {
  const re = new RegExp(`^(?:@\\S+ )?:${from}!\\S+ PRIVMSG \\S+ :\\x01DCC ${type} ${name} (\\S+) (\\d+)(?: (\\d+))?(?: (\\S+))?\\x01$`, 'i')
  const m = (await c.waitFor(re, 20000)).match(re)
  return { ip: m[1], port: +m[2], rest: m.slice(3).filter(Boolean) }
}

function dial(port) {
  return new Promise((resolve, reject) => {
    const s = net.connect(port, DCC_HOST, () => resolve(s))
    s.on('error', reject)
  })
}

// readUntil collects what a socket receives until it ends (or has n bytes).
function readUntil(s, n = Infinity) {
  return new Promise((resolve, reject) => {
    const chunks = []
    let len = 0
    const done = () => resolve(Buffer.concat(chunks))
    s.on('data', (d) => { chunks.push(d); len += d.length; if (len >= n) { s.off('end', done); done() } })
    s.on('end', done)
    s.on('error', reject)
    setTimeout(() => reject(new Error(`only ${len} bytes`)), 20000)
  })
}

let alice, bob
const sockets = []

test.beforeEach(async () => {
  alice = await soloUser('dcca')
  bob = await soloUser('dccb')
})
test.afterEach(async () => {
  for (const s of sockets.splice(0)) s.destroy()
  for (const u of [alice, bob]) {
    if (!u) continue
    u.irc.close()
    await u.api.del(`/api/networks/${u.net.id}`).catch(() => {})
    await u.api.dispose()
  }
})

test('a DCC SEND goes through evrd', async () => {
  const data = Buffer.from(`hello through evrd ${uniq('f')}\n`.repeat(200))
  // bob's client can't listen: a passive offer (port 0, a token)
  bob.irc.send(`PRIVMSG ${alice.nick} :\x01DCC SEND hello.txt ${LOCALHOST} 0 ${data.length} 77\x01`)

  // alice's client gets an ordinary offer, pointing at evrd
  const o = await offerTo(alice.irc, bob.nick, 'SEND', 'hello.txt')
  expect(o.ip).toBe(String(LOCALHOST))
  expect(o.port).toBeGreaterThan(0)
  expect(o.rest).toEqual([String(data.length)]) // no token: evrd listens for her
  const a = await dial(o.port)
  sockets.push(a)
  const received = readUntil(a, data.length)

  // bob's client gets the answer, pointing at evrd too, with its token
  const answer = await offerTo(bob.irc, alice.nick, 'SEND', 'hello.txt')
  expect(answer.ip).toBe(String(LOCALHOST))
  expect(answer.port).toBeGreaterThan(0)
  expect(answer.rest).toEqual([String(data.length), '77'])
  const b = await dial(answer.port)
  sockets.push(b)
  b.end(data)

  expect((await received).equals(data)).toBe(true)

  // and it's in both connection logs
  for (const u of [alice, bob]) {
    await expect.poll(async () => JSON.stringify(await u.api.get(`/api/networks/${u.net.id}/log`)), { timeout: 15000 })
      .toContain('bouncedcc: relaying')
  }
})

test('a DCC CHAT goes through evrd, both ways', async () => {
  alice.irc.send(`PRIVMSG ${bob.nick} :\x01DCC CHAT chat ${LOCALHOST} 0 88\x01`)

  const o = await offerTo(bob.irc, alice.nick, 'CHAT', 'chat')
  expect(o.ip).toBe(String(LOCALHOST))
  expect(o.rest).toEqual([])
  const b = await dial(o.port)
  sockets.push(b)

  const answer = await offerTo(alice.irc, bob.nick, 'CHAT', 'chat')
  expect(answer.rest).toEqual(['88'])
  const a = await dial(answer.port)
  sockets.push(a)

  const atB = readUntil(b, 'hello bob\n'.length)
  a.write('hello bob\n')
  expect((await atB).toString()).toBe('hello bob\n')
  const atA = readUntil(a, 'hi alice\n'.length)
  b.write('hi alice\n')
  expect((await atA).toString()).toBe('hi alice\n')

  // one side hangs up: the other's connection ends too
  const ended = new Promise((r) => b.once('close', r))
  a.end()
  await ended
})
