import { test, expect } from '@playwright/test'
import { execSync } from 'node:child_process'
import { IRC } from './irc.js'
import { freshUser, addNetwork, session, uniq, SOLO_URL, SOLO_IRC_PORT, COMPOSE, SOLO_EVRCTL } from './helpers.js'

// The connection holder: restarting evrd (evrd-solo, standalone, with its
// IRC connections held by the links service) keeps its users on IRC — the
// network sees no QUIT and no JOIN, the nick is kept, and what was said
// while evrd was down is in the history afterwards.
//
// A watcher on the clustered nodes sits in the same channel of the dev ircd
// and reports what the network saw.

test.describe.configure({ timeout: 180_000 })

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

test('restarting evrd keeps its users on IRC', async () => {
  const name = uniq('hold')
  const password = `${name}-secret-pw`
  execSync(`${SOLO_EVRCTL} user add ${name} ${password}`, { stdio: 'ignore' })
  const solo = await session(name, password, SOLO_URL)

  const chan = '#' + uniq('held')
  const watcher = await freshUser('watch')
  let irc
  try {
    const wnet = await addNetwork(watcher.api, { name: 'Dev', nick: watcher.name, channels: [chan] })
    irc = await IRC.connect({ user: watcher.name, network: 'Dev', password: watcher.password })
    await irc.waitFor(/ 001 /, 15000)

    // The solo user joins the same channel through evrd-solo (and the holder).
    const nick = name.slice(0, 15)
    const net = await retry(() => solo.post('/api/networks', { name: 'Dev', host: 'ircd', port: 6667, tls: false, nick, channels: [chan] }))
    await retry(() => solo.post(`/api/networks/${net.id}/connect`))
    await expect.poll(async () => (await solo.get(`/api/networks/${net.id}`)).status, { timeout: 30000 }).toBe('connected')
    await irc.waitFor(new RegExp(`^:${nick}!\\S+ JOIN :?${chan}`), 20000)
    void wnet

    // evrd-solo goes down; the channel goes on without it.
    const mark = irc.lines.length
    execSync(`${COMPOSE} stop -t 20 evrd-solo`, { stdio: 'ignore' })
    const said = `while evrd was down ${uniq('m')}`
    irc.send(`PRIVMSG ${chan} :${said}`)
    await new Promise((r) => setTimeout(r, 1500))
    execSync(`${COMPOSE} start evrd-solo`, { stdio: 'ignore' })

    // Back: the same link, connected, same nick — without reconnecting.
    await expect.poll(async () => {
      try { return (await solo.get(`/api/networks/${net.id}`)).status } catch { return 'down' }
    }, { timeout: 120000, intervals: [1000] }).toBe('connected')
    expect((await solo.get(`/api/networks/${net.id}`)).currentNick).toBe(nick)

    // The network never saw it leave or come back.
    await new Promise((r) => setTimeout(r, 2000))
    const seen = irc.lines.slice(mark).filter((l) => l.startsWith(`:${nick}!`))
    expect(seen.filter((l) => / (QUIT|JOIN|PART) /.test(l)), 'the watcher saw the user quit or rejoin').toEqual([])

    // What was said meanwhile reached evrd's history.
    await expect.poll(async () => {
      const msgs = await solo.get(`/api/networks/${net.id}/messages?target=${encodeURIComponent(chan)}`)
      return (msgs || []).some((m) => m.text === said)
    }, { timeout: 20000 }).toBe(true)

    await solo.del(`/api/networks/${net.id}`)
  } finally {
    irc?.close()
    await solo.dispose()
    await watcher.cleanup()
  }
})

// The holder keeps the IRC clients too (issue #70): a client attached to
// evrd-solo stays connected while evrd-solo restarts. It sees no disconnect
// and no second welcome, keeps its nick, gets what the channel said in the
// gap, and what it said in the gap reaches the channel.
test('restarting evrd keeps its IRC clients connected', async () => {
  const name = uniq('keep')
  const password = `${name}-secret-pw`
  execSync(`${SOLO_EVRCTL} user add ${name} ${password}`, { stdio: 'ignore' })
  const solo = await session(name, password, SOLO_URL)

  const chan = '#' + uniq('kept')
  const watcher = await freshUser('watch')
  let irc, client
  try {
    await addNetwork(watcher.api, { name: 'Dev', nick: watcher.name, channels: [chan] })
    irc = await IRC.connect({ user: watcher.name, network: 'Dev', password: watcher.password })
    await irc.waitFor(/ 001 /, 15000)

    const nick = name.slice(0, 15)
    const net = await retry(() => solo.post('/api/networks', { name: 'Dev', host: 'ircd', port: 6667, tls: false, nick, channels: [chan] }))
    await retry(() => solo.post(`/api/networks/${net.id}/connect`))
    await expect.poll(async () => (await solo.get(`/api/networks/${net.id}`)).status, { timeout: 30000 }).toBe('connected')
    await irc.waitFor(new RegExp(`^:${nick}!\\S+ JOIN :?${chan}`), 20000)

    // An IRC client on evrd-solo, through the holder's listener.
    client = await IRC.connect({ user: name, network: 'Dev', password, port: SOLO_IRC_PORT, caps: ['server-time'] })
    let closed = false
    client.sock.on('close', () => { closed = true })
    await client.waitFor(new RegExp(` 001 ${nick} `), 15000)
    await client.waitFor(new RegExp(` JOIN :?${chan}`), 15000)
    client.send(`PRIVMSG ${chan} :before the restart`)
    await irc.waitFor(new RegExp(`^:${nick}!\\S+ PRIVMSG ${chan} :before the restart`), 10000)

    // evrd-solo goes down; both sides carry on talking.
    const mark = client.lines.length
    const wmark = irc.lines.length
    execSync(`${COMPOSE} stop -t 20 evrd-solo`, { stdio: 'ignore' })
    const said = `said while evrd was down ${uniq('m')}`
    irc.send(`PRIVMSG ${chan} :${said}`)
    client.send('PING :still-there')
    await client.next(/^:evrd PONG evrd :still-there$/, 10000) // the holder answers
    const mine = `sent while evrd was down ${uniq('m')}`
    client.send(`PRIVMSG ${chan} :${mine}`)
    await new Promise((r) => setTimeout(r, 1500))
    execSync(`${COMPOSE} start evrd-solo`, { stdio: 'ignore' })

    // The channel's line from the gap arrives, with its time; ours goes out.
    const got = await client.waitFor(new RegExp(`PRIVMSG ${chan} :${said}$`), 120000)
    expect(got).toMatch(/^@time=/)
    await irc.waitFor(new RegExp(`^:${nick}!\\S+ PRIVMSG ${chan} :${mine}$`), 30000)

    // and evrd still answers it, as the same client
    client.send('PRIVMSG *status :version')
    await client.waitFor(/:\*status!\S+ PRIVMSG \S+ :evrd /, 15000)

    const after = client.lines.slice(mark)
    expect(closed, 'the client was disconnected').toBe(false)
    expect(after.filter((l) => / (001|376) /.test(l) || /^ERROR /.test(l)), 'a second welcome or an ERROR').toEqual([])
    expect(after.filter((l) => new RegExp(`^(@\\S+ )?:${nick}!\\S+ JOIN `).test(l)), 'rejoined').toEqual([])
    const seen = irc.lines.slice(wmark).filter((l) => l.startsWith(`:${nick}!`))
    expect(seen.filter((l) => / (QUIT|JOIN|PART) /.test(l)), 'the network saw it leave or come back').toEqual([])

    await solo.del(`/api/networks/${net.id}`)
  } finally {
    client?.sock.destroy()
    irc?.close()
    await solo.dispose()
    await watcher.cleanup()
  }
})

// The admin overview names the holder evrd-solo uses, and that it keeps the
// IRC clients.
test('the admin overview reports the connection holder', async () => {
  const name = uniq('hadm')
  const password = `${name}-secret-pw`
  execSync(`${SOLO_EVRCTL} user add -admin ${name} ${password}`, { stdio: 'ignore' })
  const solo = await session(name, password, SOLO_URL)
  try {
    const ov = await retry(() => solo.get('/api/admin/overview'))
    expect(ov.holder).toMatchObject({ configured: true, reachable: true, protocol: 2, holdsClients: true })
    // the holder's binary hash. In an image it's the evrd-links beside evrd,
    // so it has evrd's release; the dev stack runs both with go run, so
    // there's nothing to compare it with. Never an older build.
    expect(ov.holder.hash).toMatch(/^[0-9a-f]{12}$/)
    expect(ov.holder.older).toBe(false)
    expect(['', ov.version]).toContain(ov.holder.version)
  } finally {
    await solo.dispose()
  }
})
