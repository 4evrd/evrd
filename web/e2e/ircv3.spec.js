import { test, expect } from '@playwright/test'
import { freshUser, addNetwork, uniq } from './helpers.js'
import { IRC } from './irc.js'

// A modern IRC client (Goguma, gamja, Senpai…) on evrd: it negotiates
// chathistory, gets no automatic backlog, and fetches history itself, with
// msgids that match the live lines.
let me, them, chan, irc, peer
test.beforeEach(async () => {
  chan = '#' + uniq('h')
  me = await freshUser('me')
  them = await freshUser('them')
  await addNetwork(me.api, { name: 'Dev', nick: me.name, channels: [chan] })
  await addNetwork(them.api, { name: 'Dev', nick: them.name, channels: [chan] })
  peer = await IRC.connect({ user: them.name, network: 'Dev', password: them.password })
  await peer.waitFor(new RegExp(` 366 \\S+ ${chan.replace('#', '\\#')} `, 'i'))
})
test.afterEach(async () => {
  irc?.close()
  peer?.close()
  await me.cleanup()
  await them.cleanup()
})

// fetch sends a CHATHISTORY command and returns its batch's lines.
async function fetch(c, cmd) {
  c.send(`CHATHISTORY ${cmd}`)
  const ref = (await c.next(/ BATCH \+\S+ chathistory /)).match(/BATCH \+(\S+)/)[1]
  await c.waitFor(new RegExp(`^:\\S+ BATCH -${ref}$`))
  return c.lines.filter((l) => l.includes(`batch=${ref};`))
}
const said = (lines) => lines.map((l) => l.replace(/^.* :/, ''))

test('CHATHISTORY fetches what was said, with the live msgids', async () => {
  irc = await IRC.connect({ user: me.name, network: 'Dev', password: me.password, caps: ['draft/chathistory', 'batch', 'server-time', 'message-tags'] })
  await irc.waitFor(/ CAP \S+ ACK :draft\/chathistory/)
  await irc.waitFor(/ 005 \S+ CHATHISTORY=1000 MSGREFTYPES=msgid,timestamp /)
  await irc.waitFor(new RegExp(` 366 \\S+ ${chan.replace('#', '\\#')} `, 'i'))

  peer.send(`PRIVMSG ${chan} :first`)
  peer.send(`PRIVMSG ${chan} :second`)
  const live = await irc.waitFor(/PRIVMSG \S+ :second$/)
  expect(live).toMatch(/^@time=\S+Z;msgid=[0-9a-f]+ /)
  const msgid = live.match(/msgid=([0-9a-f]+)/)[1]

  // ScyllaDB writes land a moment later: ask until they're there
  let lines = []
  await expect(async () => {
    lines = await fetch(irc, `LATEST ${chan} * 10`)
    expect(said(lines)).toEqual(['first', 'second'])
  }).toPass({ timeout: 10000 })
  expect(lines[1]).toContain(`msgid=${msgid}`)

  // older than the second: just the first
  expect(said(await fetch(irc, `BEFORE ${chan} msgid=${msgid} 10`))).toEqual(['first'])

  irc.send('CHATHISTORY NOPE x * 1')
  await irc.waitFor(/FAIL CHATHISTORY INVALID_PARAMS NOPE :Unknown command/)
})

test('typing indicators go through evrd to clients that asked for message-tags', async () => {
  irc = await IRC.connect({ user: me.name, network: 'Dev', password: me.password, caps: ['message-tags'] })
  await irc.waitFor(new RegExp(` 366 \\S+ ${chan.replace('#', '\\#')} `, 'i'))
  const plain = await IRC.connect({ user: me.name, network: 'Dev', password: me.password })
  try {
    await plain.waitFor(new RegExp(` 366 \\S+ ${chan.replace('#', '\\#')} `, 'i'))
    // the other user's client types: evrd passes it to the network, which
    // passes it back to this user's evrd, which passes it on
    const typer = await IRC.connect({ user: them.name, network: 'Dev', password: them.password, caps: ['message-tags'] })
    try {
      await typer.waitFor(new RegExp(` 366 \\S+ ${chan.replace('#', '\\#')} `, 'i'))
      await expect(async () => {
        typer.send(`@+typing=active TAGMSG ${chan}`)
        await irc.next(new RegExp(`^@\\+typing=active :${them.name}!\\S+ TAGMSG ${chan.replace('#', '\\#')}$`, 'i'), 2000)
      }).toPass({ timeout: 20000 })
      typer.send(`PRIVMSG ${chan} :done typing`)
      await plain.waitFor(/PRIVMSG \S+ :done typing$/)
      // no tags and no TAGMSG for a client that didn't ask (005 may name TAGMSG in TARGMAX)
      expect(plain.lines.some((l) => l.startsWith('@') || /^:\S+ TAGMSG /.test(l))).toBe(false)
    } finally { typer.close() }
  } finally { plain.close() }
})

test('backlog comes as a history batch with real events, *buffextras for plain clients', async () => {
  // while this user's IRC clients are away: a line, then the other user leaves
  peer.send(`PRIVMSG ${chan} :while you were out`)
  peer.send(`PART ${chan} :off for lunch`)
  const left = new RegExp(`PART ${chan.replace('#', '\\#')}`, 'i')

  await expect(async () => {
    irc?.close()
    irc = await IRC.connect({ user: me.name, network: 'Dev', password: me.password, caps: ['batch', 'server-time', 'message-tags', 'draft/event-playback'] })
    const ref = (await irc.waitFor(new RegExp(` BATCH \\+(\\S+) chathistory ${chan.replace('#', '\\#')}$`, 'i'), 5000)).match(/BATCH \+(\S+)/)[1]
    await irc.waitFor(new RegExp(`^:\\S+ BATCH -${ref}$`), 5000)
    const inBatch = irc.lines.filter((l) => l.includes(`batch=${ref}`))
    expect(inBatch.some((l) => /PRIVMSG \S+ :while you were out$/.test(l))).toBe(true)
    expect(inBatch.some((l) => left.test(l) && l.includes(`:${them.name}!`))).toBe(true)
    expect(irc.lines.some((l) => l.includes('*buffextras'))).toBe(false)
  }).toPass({ timeout: 20000 })

  // a client without the caps: the same events, as *buffextras lines
  const plain = await IRC.connect({ user: me.name, network: 'Dev', password: me.password })
  try {
    // (no server-time: evrd puts the time in front)
    await plain.waitFor(/PRIVMSG \S+ :\[[\d:]+\] while you were out$/)
    await plain.waitFor(/^:\*buffextras!\S+ PRIVMSG \S+ :\[[\d:]+\] them\S* left \(off for lunch\)$/)
    expect(plain.lines.some((l) => l.includes('BATCH'))).toBe(false)
  } finally { plain.close() }
})
