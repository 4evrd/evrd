import { test, expect } from '@playwright/test'
import { login, freshUser, addNetwork, uniq } from './helpers.js'
import { IRC } from './irc.js'

// ZNC's *status commands from a real IRC client, and playback buffers per
// network and channel: "me" attaches to evrd like irssi would; "them" is
// someone else on the dev ircd, typing from their own evrd network.

let me, them, chan, netA, netB, talk
const clients = []
const esc = (s) => s.replace(/[#.*+?^${}()|[\]\\]/g, '\\$&')

test.beforeEach(async () => {
  chan = '#' + uniq('st')
  me = await freshUser('me')
  them = await freshUser('them')
  netA = await addNetwork(me.api, { name: 'A', nick: me.name, channels: [chan] })
  netB = await addNetwork(me.api, { name: 'B', nick: me.name + 'b' })
  await addNetwork(them.api, { name: 'Dev', nick: them.name, channels: [chan] })
  talk = await IRC.connect({ user: them.name, network: 'Dev', password: them.password })
  clients.push(talk)
  await talk.waitFor(new RegExp(` 366 \\S+ ${esc(chan)} `, 'i'))
  // me is in the channel too before anyone talks
  await expect.poll(async () => (await me.api.get(`/api/networks/${netA.id}`)).channels.length, { timeout: 20000 }).toBeGreaterThan(0)
})
test.afterEach(async () => {
  while (clients.length) clients.pop().close()
  await me.cleanup()
  await them.cleanup()
})

async function attach(network, user = me) {
  const c = await IRC.connect({ user: user.name, network, password: user.password })
  clients.push(c)
  await c.waitFor(/ 376 /)
  return c
}

// cmd sends a *status command and waits for a reply matching re.
function cmd(c, text, re, timeout) {
  const reply = c.next(new RegExp(`^:\\*status!\\S+ PRIVMSG \\S+ :.*${typeof re === 'string' ? esc(re) : re.source}`), timeout)
  c.send(`PRIVMSG *status :${text}`)
  return reply
}

// replayed attaches, and returns the lines that came before *status
// answered uptime (playback comes first), then detaches.
async function replayed(network) {
  const c = await attach(network)
  await cmd(c, 'uptime', 'evrd has been running')
  const lines = [...c.lines]
  c.close()
  await c.closed()
  await new Promise((r) => setTimeout(r, 500)) // evrd notes it left
  return lines.join('\n')
}

test('auto-clear is per network: what a client saw isn’t replayed; another network keeps its own', async () => {
  const a = await attach('A')
  await cmd(a, `autoclear ${chan} on`, `${chan} auto-clears`)
  talk.send(`PRIVMSG ${chan} :seen live`)
  await a.waitFor(/:seen live$/)
  a.close()
  await a.closed()
  await new Promise((r) => setTimeout(r, 500))

  // a private message on B, where no client was
  talk.send(`PRIVMSG ${me.name}b :while you were on A`)
  await new Promise((r) => setTimeout(r, 1000))

  expect(await replayed('A')).not.toContain('seen live')
  expect(await replayed('B')).toContain('while you were on A')
  expect(await replayed('B')).not.toContain('while you were on A') // cleared by then

  // a line while away is replayed, once
  talk.send(`PRIVMSG ${chan} :while you were away`)
  await new Promise((r) => setTimeout(r, 1000))
  expect(await replayed('A')).toContain('while you were away')
  expect(await replayed('A')).not.toContain('while you were away')
})

test('nothing is replayed that another attached client is seeing', async () => {
  const stay = await attach('A')
  await cmd(stay, `autoclear ${chan} on`, 'auto-clears')
  talk.send(`PRIVMSG ${chan} :the other client has it`)
  await stay.waitFor(/:the other client has it$/)
  expect(await replayed('A')).not.toContain('the other client has it')
})

test('buffers: setbuffer, clearbuffer, playbuffer and playback', async () => {
  const a = await attach('A')
  for (let i = 1; i <= 5; i++) talk.send(`PRIVMSG ${chan} :line ${i}`)
  await a.waitFor(/:line 5$/)
  await cmd(a, 'buffers', new RegExp(`${esc(chan)}\\s+50 lines - keeps`))
  await cmd(a, `setbuffer ${chan} 2`, `${chan} plays back 2 lines.`)
  await cmd(a, 'setbuffer * 7', 'Buffers on A play back 7 lines')
  await cmd(a, `showchan ${chan}`, 'buffer: plays back 2 lines - keeps its buffer')
  a.close()
  await a.closed()
  await new Promise((r) => setTimeout(r, 500))

  let seen = await replayed('A')
  expect(seen).toContain('line 4')
  expect(seen).toContain('line 5')
  expect(seen).not.toContain('line 3')

  const c = await attach('A')
  await cmd(c, `playbuffer ${chan}`, 'Replayed 2 line(s)')
  await cmd(c, `clearbuffer ${chan.slice(0, 4)}*`, 'Cleared 1 buffer on A')
  await cmd(c, `playbuffer ${chan}`, `The buffer for ${chan} is empty.`)
  // history still has it
  await cmd(c, `playback ${chan} 10`, 'Replayed')
  expect(c.lines.some((l) => / line 1$/.test(l))).toBe(true) // (with its time in front)
  await cmd(c, `setbuffer ${chan} -`, `${chan} plays back 7 lines.`)
  await cmd(c, 'clearallchannelbuffers', 'channel buffer')
  await cmd(c, 'clearallquerybuffers', /query buffer/)
  await cmd(c, 'clearallbuffers', /Cleared \d+ buffers? on A/)
  c.close()
  await c.closed()
  await new Promise((r) => setTimeout(r, 500))
  seen = await replayed('A')
  expect(seen).not.toContain('line 5')
})

test('channels: detach, attach, disable, enable, nicks, topics, showchan', async () => {
  const a = await attach('A')
  a.send(`TOPIC ${chan} :status e2e topic`) // (me is the channel op)
  await talk.waitFor(/:status e2e topic$/)
  await cmd(a, `listnicks ${chan}`, them.name)
  await cmd(a, 'topics', 'status e2e topic')
  await cmd(a, `showchan ${chan}`, new RegExp(`${esc(chan)} on A - joined, 2 nicks`))
  await cmd(a, 'channels', chan)

  // detach hides it, attach brings it back with what was missed
  const hidden = a.next(new RegExp(`PART ${esc(chan)} :Detached`, 'i'))
  a.send(`PRIVMSG *status :detach ${chan}`)
  await hidden
  talk.send(`PRIVMSG ${chan} :said while detached`)
  await new Promise((r) => setTimeout(r, 800))
  expect(a.lines.some((l) => l.endsWith(':said while detached'))).toBe(false)
  const back = a.next(/said while detached$/) // (played back with its time)
  a.send(`PRIVMSG *status :attach ${chan}`) // JOIN and playback follow
  await back
  await cmd(a, `attach ${chan}`, `${chan} isn't detached.`)

  // disable parts it (them sees it) but keeps it saved; enable joins again
  const parted = talk.next(new RegExp(`^:${esc(me.name)}!\\S+ PART ${esc(chan)}`, 'i'))
  await cmd(a, `disablechan ${chan}`, `Disabled ${chan}: left it, kept it saved.`)
  await parted
  const n = await me.api.get(`/api/networks/${netA.id}`)
  expect(n.channels.map((c) => c.toLowerCase())).toContain(chan.toLowerCase())
  await cmd(a, `showchan ${chan}`, 'saved, not joined - disabled')
  const joined = talk.next(new RegExp(`^:${esc(me.name)}!\\S+ JOIN :?${esc(chan)}`, 'i'))
  await cmd(a, `enablechan ${chan}`, `Enabled ${chan}: joining it.`)
  await joined
})

test('jump: to the next server, to another network and back', async () => {
  const a = await attach('A')
  await cmd(a, 'listservers', '1. ircd 6667')
  await cmd(a, 'jump', 'Reconnecting A to ircd:6667')
  await expect.poll(async () => (await me.api.get(`/api/networks/${netA.id}`)).status, { timeout: 20000 }).toBe('connected')
  await cmd(a, 'jump B', 'Jumped from A to B.')
  await cmd(a, 'networks', '* B')
  const rejoined = a.next(new RegExp(` JOIN ${esc(chan)}$`, 'i')) // its channels come back
  await cmd(a, 'jumpnetwork A', 'Jumped from B to A.')
  await rejoined
  await cmd(a, 'jump nowhere', 'neither one of your networks nor a server of A')
})

test('clients, uptime, traffic, bind hosts, modules, and what evrd does elsewhere', async () => {
  const a = await attach('A')
  // (its name comes in its answer to evrd's CTCP VERSION)
  await expect(async () => { await cmd(a, 'listclients', 'e2e-client', 1500) }).toPass({ timeout: 10000 })
  await cmd(a, 'uptime', 'evrd has been running for')
  await cmd(a, 'traffic', /Traffic|health page/)
  await cmd(a, 'showbindhost', "This network's bind host: (not set)")
  await cmd(a, 'listmods', 'loadmod <module>')
  await cmd(a, 'listavailmods', 'loadmod <module>')
  await cmd(a, 'rehash', "evrd applies settings as they're saved")
  await cmd(a, 'saveconfig', "evrd saves every change as it's made")
  await cmd(a, 'reloadmod q', 'Modules are built into evrd')
  await cmd(a, 'help', /\| CLEARALLQUERYBUFFERS +\| +\| Clear every query buffer/)
  // commands of the same name as their own go to the network
  const list = a.next(/ 323 /) // the network's LIST answer
  a.send('LIST')
  await list
  // ZNC's raw command form
  const r = a.next(/:\*status!\S+ PRIVMSG \S+ :.*evrd has been running/)
  a.send('ZNC Uptime')
  await r
})

// Every *status command ZNC 1.9.1 has, by ZNC's name (the case it writes
// them in, too), as a ZNC user would type it: none is unknown here. The
// ones that change things get no arguments (their usage) or the other
// network, B.
const ZNC_COMMANDS = [
  'AddMOTD x', 'AddNetwork', 'AddPort', 'AddServer', 'AddTrustedServerFingerprint', 'Attach',
  'Broadcast x', 'ClearAllBuffers', 'ClearAllChannelBuffers', 'ClearAllQueryBuffers', 'ClearBindHost',
  'ClearBuffer', 'ClearMOTD', 'ClearUserBindHost', 'ClearVHost', 'Disconnect B', 'Connect B', 'DelNetwork',
  'DelPort', 'DelServer', 'DelTrustedServerFingerprint', 'Detach', 'DisableChan', 'EnableChan', 'Help',
  'Jump B', 'JumpNetwork', 'ListAllUserNetworks', 'ListAvailableModules', 'ListAvailMods', 'ListChans',
  'ListClients', 'ListMods', 'ListModules', 'ListNetworks', 'ListNicks', 'ListPorts', 'ListServers',
  'ListTrustedServerFingerprints', 'ListUsers', 'LoadMod', 'LoadModule', 'MOTD', 'MoveChan', 'MoveNetwork',
  'PlayBuffer', 'Rehash', 'ReloadMod', 'ReloadModule', 'RemServer', 'Restart', 'SaveConfig', 'SetBindHost',
  'SetBuffer', 'SetMOTD x', 'SetUserBindHost', 'SetVHost', 'ShowBindHost', 'ShowMOTD', 'Shutdown',
  'SwapChans', 'Topics', 'Traffic', 'UnloadMod', 'UnloadModule', 'UpdateMod', 'UpdateModule', 'Uptime', 'Version',
]

test('every ZNC *status command has an answer', async () => {
  const a = await attach('A')
  for (const [i, c] of ZNC_COMMANDS.entries()) {
    // a marker after it: everything before the marker's answer is its own
    const from = a.lines.length
    a.send(`PRIVMSG *status :${c}`)
    a.send(`PRIVMSG *status :zzmark${i}`)
    await a.waitFor(new RegExp(`Unknown command "zzmark${i}"`))
    const said = a.lines.slice(from).filter((l) => /^:\*status!/.test(l) && !l.includes(`zzmark${i}`))
    expect(said.length, c).toBeGreaterThan(0)
    expect(said.join('\n'), c).not.toMatch(/Unknown command/)
  }
  // the ones evrd has under its own name do the same thing
  await cmd(a, 'SetVHost', 'Usage: setbindhost')
  await cmd(a, 'RemServer', 'Usage: delserver')
  await cmd(a, 'MOTD', '/motd shows the network')
  await cmd(a, 'SwapChans #a #b', 'evrd keeps no channel order')
  await cmd(a, 'ClearBuffer #nowhere', 'No buffer to clear on B.') // Jump B moved it
})

test('help filters, channels show their state, a private message plays back to you, fallback servers keep the link', async () => {
  const a = await attach('A')
  // help with a filter: only those commands
  const from = a.lines.length
  await cmd(a, 'help buffer', /\| SETBUFFER /)
  expect(a.lines.slice(from).join('\n')).not.toMatch(/\| NETWORKS /)
  await cmd(a, 'help zzz', 'No command matches zzz')
  // channels: one a line, with its state
  await cmd(a, 'channels', new RegExp(`${esc(chan)}\\s+joined, 2 nicks`))
  await cmd(a, `disablechan ${chan}`, 'Disabled')
  await cmd(a, 'channels', new RegExp(`${esc(chan)}\\s+saved, not joined, disabled`))
  await cmd(a, `enablechan ${chan}`, 'Enabled')
  // a private message from them replays as sent to me, not to them
  talk.send(`PRIVMSG ${me.name} :psst`)
  await a.waitFor(/:psst$/)
  await expect(async () => { // once it's saved
    const pm = a.next(new RegExp(`^:${esc(them.name)}!\\S+ PRIVMSG ${esc(me.name)} :.*psst`), 1500)
    a.send(`PRIVMSG *status :playback ${them.name} 1`)
    await pm
  }).toPass({ timeout: 15000 })
  // a server to fall back to (and removing it) doesn't reconnect: no KICK
  await cmd(a, 'addserver ircd 6668', "It's used when the link drops.")
  await cmd(a, 'delserver ircd 6668', 'Removed ircd:6668 from A.')
  await new Promise((r) => setTimeout(r, 1500))
  expect(a.lines.some((l) => / KICK /.test(l))).toBe(false)
})

test('admin listings are for admins only', async () => {
  const a = await attach('A')
  await cmd(a, 'listusers', 'Admins only.')
  await cmd(a, 'listallusernetworks', 'Admins only.')

  const boss = await freshUser('boss', { admin: true })
  try {
    await addNetwork(boss.api, { name: 'Dev', nick: boss.name })
    const b = await attach('Dev', boss)
    await cmd(b, 'listusers', me.name)
    await cmd(b, 'listallusernetworks', new RegExp(`${esc(me.name)}\\s+B`))
  } finally { await boss.cleanup() }
})

test('the web chat runs the buffer commands too', async ({ page }) => {
  await login(page, me.name, me.password)
  await page.goto(`/chat?net=${netA.id}&buf=${encodeURIComponent('*status')}`)
  await expect(page.locator('.live.on')).toBeVisible({ timeout: 10000 })
  const input = page.getByRole('textbox', { name: 'Message' })
  // help comes as a Markdown table, drawn as one
  await input.fill('help')
  await input.press('Enter')
  const help = page.getByTestId('status-table').last()
  await expect(help.getByRole('row').filter({ hasText: 'CLEARBUFFER' })).toContainText('<channel|nick>[,…]')
  await expect(help.getByRole('columnheader', { name: 'Description' })).toHaveCount(1)
  await input.fill(`setbuffer ${chan} 3`)
  await input.press('Enter')
  await expect(page.locator('.log').getByText(`${chan} plays back 3 lines.`)).toBeVisible({ timeout: 5000 })
  // from any buffer: /znc …; a *status command on its own isn't one
  await page.goto(`/chat?net=${netA.id}&buf=${encodeURIComponent(chan)}`)
  await expect(page.locator('.live.on')).toBeVisible({ timeout: 10000 })
  await input.fill('/znc uptime')
  await input.press('Enter')
  await expect(page.locator('#buf-title')).toHaveText('*status')
  await expect(page.locator('.log').getByText(/evrd has been running/)).toBeVisible({ timeout: 5000 })
  await input.fill('/clearbuffer #x')
  await input.press('Enter')
  await expect(page.getByText(/\/clearbuffer isn’t supported here/)).toBeVisible()
  await input.fill(`playbuffer ${chan}`)
  await input.press('Enter')
  await expect(page.locator('.log').getByText(/already shows each buffer's history/)).toBeVisible({ timeout: 5000 })
})
