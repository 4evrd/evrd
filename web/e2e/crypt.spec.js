import { createHash, randomBytes } from 'node:crypto'
import { test, expect } from '@playwright/test'
import { login, freshUser, addNetwork, uniq } from './helpers.js'
import { IRC } from './irc.js'

// The crypt module (FiSH): "me" attaches to evrd like irssi would, with
// crypt on; "them" is someone else on the dev ircd, typing from their own
// evrd network without it, so they see (and send) the encrypted text as it
// is on the network, the way a FiSH client would.
//
// "+OK 9vjCO08wIY41GHMAo.pCIjl/" is "Hello, world!" with the key "testkey"
// in ECB, as weechat's fish.py makes it (internal/modules/fish_test.go).

const HELLO = '+OK 9vjCO08wIY41GHMAo.pCIjl/'
let me, them, chan, net, talk
const clients = []
const esc = (s) => s.replace(/[#.*+?^${}()|[\]\\/]/g, '\\$&')

test.beforeEach(async () => {
  chan = '#' + uniq('fish')
  me = await freshUser('me')
  them = await freshUser('them')
  net = await addNetwork(me.api, { name: 'A', nick: me.name, channels: [chan] })
  await addNetwork(them.api, { name: 'Dev', nick: them.name, channels: [chan] })
  talk = await IRC.connect({ user: them.name, network: 'Dev', password: them.password })
  clients.push(talk)
  await talk.waitFor(new RegExp(` 366 \\S+ ${esc(chan)} `, 'i'))
  await expect.poll(async () => (await me.api.get(`/api/networks/${net.id}`)).channels.length, { timeout: 20000 }).toBeGreaterThan(0)
})
test.afterEach(async () => {
  while (clients.length) clients.pop().close()
  await me.cleanup()
  await them.cleanup()
})

async function attach() {
  const c = await IRC.connect({ user: me.name, network: 'A', password: me.password })
  clients.push(c)
  await c.waitFor(/ 376 /)
  return c
}

// crypt sends a *crypt command and waits for its answer (a NOTICE from *crypt).
function crypt(c, text, re) {
  const reply = c.next(new RegExp(`^:\\*crypt!\\S+ NOTICE \\S+ :.*${typeof re === 'string' ? esc(re) : re.source}`))
  c.send(`PRIVMSG *crypt :${text}`)
  return reply
}

async function cryptOn(c) {
  const on = c.next(/^:\*status!\S+ PRIVMSG \S+ :crypt is on for A\./)
  c.send('PRIVMSG *status :loadmod crypt')
  await on
}

const keys = async () => (await me.api.get(`/api/networks/${net.id}`)).modules?.crypt?.settings?.keys || ''

test('a key set in the web app: what me sends goes out encrypted, what arrives encrypted is read decrypted', async ({ page }) => {
  await login(page, me.name, me.password)
  await page.goto(`/networks/${net.id}`)
  const mod = page.getByTestId('module-crypt')
  await expect(mod).toContainText('ZNC crypt')
  await mod.getByRole('switch').click()
  await mod.getByLabel('Keys').fill(`${chan} testkey`)
  await page.getByRole('button', { name: 'Save changes' }).last().click()
  await expect(page.getByText('Saved A')).toBeVisible()
  await expect.poll(keys).toBe(`${chan} testkey`)

  const a = await attach()
  const b = await attach() // me's other client

  // on the network it's FiSH; me's other client sees what was written
  const seen = b.next(new RegExp(`PRIVMSG ${esc(chan)} :Hello, world!$`))
  a.send(`PRIVMSG ${chan} :Hello, world!`)
  await talk.waitFor(new RegExp(`^:${esc(me.name)}!\\S+ PRIVMSG ${esc(chan)} :${esc(HELLO)}$`, 'i'))
  await seen
  expect(talk.lines.some((l) => l.endsWith(':Hello, world!'))).toBe(false)

  // an action, and a long line split so each piece fits
  a.send(`PRIVMSG ${chan} :\x01ACTION waves\x01`)
  await talk.waitFor(new RegExp(`PRIVMSG ${esc(chan)} :\\x01ACTION \\+OK \\S+\\x01$`))
  const before = talk.lines.length
  a.send(`PRIVMSG ${chan} :${'all work and no play makes jack a dull boy '.repeat(20)}`)
  await expect.poll(() => talk.lines.slice(before).filter((l) => l.includes(` PRIVMSG ${chan} :+OK `)).length, { timeout: 15000 }).toBeGreaterThan(1)

  // them, with a FiSH client, writes back: me reads it decrypted, marked
  talk.send(`PRIVMSG ${chan} :${HELLO}`)
  await a.waitFor(new RegExp(`^:${esc(them.name)}!\\S+ PRIVMSG ${esc(chan)} :🔒 Hello, world!$`, 'i'))
  // text that doesn't decrypt comes through as it is
  talk.send(`PRIVMSG ${chan} :+OK not!fish`)
  await a.waitFor(/:\+OK not!fish$/)

  // the topic too: set encrypted, read decrypted
  const topic = a.next(new RegExp(`TOPIC ${esc(chan)} :Hello, world!$`))
  a.send(`TOPIC ${chan} :Hello, world!`) // (me is the channel op)
  await talk.waitFor(new RegExp(`TOPIC ${esc(chan)} :${esc(HELLO)}$`))
  await topic
})

test('*crypt from an IRC client: setkey, listkeys, delkey', async () => {
  const a = await attach()
  await cryptOn(a)
  await crypt(a, `setkey ${them.name} cbc:a private key`, `Key for ${them.name} set (CBC)`)
  await expect.poll(keys).toBe(`${them.name} cbc:a private key`)
  await crypt(a, 'listkeys', `${them.name}: CBC a private key`)

  a.send(`PRIVMSG ${them.name} :just between us`)
  await talk.waitFor(new RegExp(`^:${esc(me.name)}!\\S+ PRIVMSG ${esc(them.name)} :\\+OK \\*[A-Za-z0-9+/=]+$`, 'i'))
  expect(talk.lines.some((l) => l.includes('just between us'))).toBe(false)

  await crypt(a, `delkey ${them.name}`, `Key for ${them.name} deleted`)
  await expect.poll(keys).toBe('')
  a.send(`PRIVMSG ${them.name} :in the clear now`)
  await talk.waitFor(/:in the clear now$/)
  await crypt(a, 'listkeys', 'No keys yet')
  await crypt(a, 'nonsense', 'Unknown command nonsense')
  expect(talk.lines.some((l) => l.includes('*crypt'))).toBe(false) // never went to the network
})

// DH1080 as FiSH does it (internal/modules/dh1080.go): them plays the FiSH
// client, with its own key pair.
const P = BigInt('0xFBE1022E23D213E8ACFA9AE8B9DFADA3EA6B7AC7A7B7E95AB5EB2DF858921FEADE95E6AC7BE7DE6ADBAB8A783E' +
  '7AF7A7FA6A2B7BEB1E72EAE2B72F9FA2BFB2A2EFBEFAC868BADB3E828FA8BADFADA3E4CC1BE7E8AFE85E9698A7' +
  '83EB68FA07A77AB6AD7BEB618ACF9CA2897EB28A6189EFA07AB99A8A7FA9AE299EFA7BA66DEAFEFBEFBF0B7D8B')
function modpow(b, e, m) {
  let r = 1n
  b %= m
  for (; e > 0n; e >>= 1n, b = (b * b) % m) if (e & 1n) r = (r * b) % m
  return r
}
const toBytes = (n) => { const h = n.toString(16); return Buffer.from(h.length % 2 ? '0' + h : h, 'hex') }
const dhEncode = (buf) => buf.toString('base64').replace(/=+$/, '') + (buf.length % 3 === 0 ? 'A' : '')
const dhDecode = (s) => BigInt('0x' + Buffer.from(s.length % 4 === 1 ? s.slice(0, -1) : s, 'base64').toString('hex'))
function dhPair() {
  for (;;) {
    const priv = BigInt('0x' + randomBytes(135).toString('hex'))
    const pub = dhEncode(toBytes(modpow(2n, priv, P)))
    // (FiSH drops "A"s at the end before the last character: keep clear of that)
    if (pub[pub.length - 2] !== 'A') return { priv, pub }
  }
}
const dhKey = (priv, theirs) => dhEncode(createHash('sha256').update(toBytes(modpow(dhDecode(theirs), priv, P))).digest())

test('DH1080: keyx with someone, and answering someone who starts one', async () => {
  const a = await attach()
  await cryptOn(a)

  // me starts it; them answers as a FiSH client would
  const init = talk.next(new RegExp(`^:${esc(me.name)}!\\S+ NOTICE ${esc(them.name)} :DH1080_INIT (\\S+) CBC$`, 'i'))
  await crypt(a, `keyx ${them.name}`, `Asked ${them.name} for a key exchange`)
  const ours = (await init).match(/DH1080_INIT (\S+) CBC$/)[1]
  const p1 = dhPair()
  const done = a.next(new RegExp(`^:\\*crypt!\\S+ NOTICE \\S+ :Key exchange with ${esc(them.name)} done \\(CBC\\)`))
  talk.send(`NOTICE ${me.name} :DH1080_FINISH ${p1.pub} CBC`)
  await done
  await expect.poll(keys).toBe(`${them.name} cbc:${dhKey(p1.priv, ours)}`)

  // them starts one (an old client: no CBC); evrd answers and takes the key
  const p2 = dhPair()
  const finish = talk.next(new RegExp(`^:${esc(me.name)}!\\S+ NOTICE ${esc(them.name)} :DH1080_FINISH (\\S+)$`, 'i'))
  const told = a.next(new RegExp(`^:\\*crypt!\\S+ NOTICE \\S+ :${esc(them.name)} asked for a key exchange`))
  talk.send(`NOTICE ${me.name} :DH1080_INIT ${p2.pub}`)
  const answer = (await finish).match(/DH1080_FINISH (\S+)$/)[1]
  await told
  await expect.poll(keys).toBe(`${them.name} ${dhKey(p2.priv, answer)}`)
  // the exchange itself never reached me's client
  expect(a.lines.some((l) => l.includes('DH1080'))).toBe(false)

  // and now what me sends them is encrypted with it
  a.send(`PRIVMSG ${them.name} :after the exchange`)
  await talk.waitFor(new RegExp(`PRIVMSG ${esc(them.name)} :\\+OK [./0-9a-zA-Z]+$`, 'i'))
})
