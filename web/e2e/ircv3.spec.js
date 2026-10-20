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
