import { test, expect } from '@playwright/test'
import { login, freshUser, addNetwork, uniq, confirm, choose } from './helpers.js'
import { IRC } from './irc.js'

// The network page's Playback section: the same settings as *status
// setbuffer / autoclear / clearbuffer, saved as they change.

let me, them, chan, net, talk
test.beforeEach(async () => {
  chan = '#' + uniq('pb')
  me = await freshUser('me')
  them = await freshUser('them')
  net = await addNetwork(me.api, { name: 'Dev', nick: me.name, channels: [chan] })
  await addNetwork(them.api, { name: 'Dev', nick: them.name, channels: [chan] })
  talk = await IRC.connect({ user: them.name, network: 'Dev', password: them.password })
  await talk.waitFor(new RegExp(` 366 \\S+ ${chan.replace('#', '\\#')} `, 'i'))
  await expect.poll(async () => (await me.api.get(`/api/networks/${net.id}`)).channels.length, { timeout: 20000 }).toBeGreaterThan(0)
  // (joins count too: they're played back) start from a clear buffer
  await new Promise((r) => setTimeout(r, 1100))
  await me.api.post(`/api/networks/${net.id}/playback/clear`, { buffers: [chan] })
  await new Promise((r) => setTimeout(r, 1100))
  for (let i = 1; i <= 3; i++) talk.send(`PRIVMSG ${chan} :line ${i}`)
  await expect.poll(async () => (await me.api.get(`/api/networks/${net.id}/playback`)).buffers.find((b) => b.name.toLowerCase() === chan)?.waiting, { timeout: 10000 }).toBe(3)
})
test.afterEach(async () => {
  talk?.close()
  await me.cleanup()
  await them.cleanup()
})

const playback = () => me.api.get(`/api/networks/${net.id}/playback`)
const mine = async () => (await playback()).buffers.find((b) => b.name.toLowerCase() === chan)

test('per-buffer lines, auto-clear and clearing from the network page', async ({ page }) => {
  await login(page, me.name, me.password)
  await page.goto(`/networks/${net.id}#playback`)
  const box = page.getByTestId('playback')
  await expect(box.getByRole('heading', { name: 'Playback' })).toBeVisible()
  const row = box.getByTestId('pb-row').filter({ hasText: chan })
  await expect(row.getByTestId('pb-waiting')).toHaveText('3')

  // the network's lines
  const netLines = box.getByLabel('Lines per buffer')
  await expect(netLines).toHaveAttribute('placeholder', '50')
  await netLines.fill('20')
  await netLines.press('Enter')
  await expect(page.getByText('Saved playback lines for Dev.')).toBeVisible()
  expect((await playback()).lines).toBe(20)

  // this buffer's lines: 2 plays 2
  const lines = row.getByLabel(`Lines for ${chan}`)
  await expect(lines).toHaveAttribute('placeholder', '20')
  await lines.fill('2')
  await lines.press('Enter')
  await expect(row.getByTestId('pb-waiting')).toHaveText('2')
  expect((await mine()).lines).toBe(2)
  await lines.fill('many')
  await lines.press('Enter')
  await expect(page.getByText(/Lines is a whole number from 0 to 500/)).toBeVisible()
  await expect(lines).toHaveValue('2')

  // auto-clear: on, then back to the account's
  const mode = row.getByRole('combobox')
  await expect(mode).toHaveText(/Account’s: last lines/)
  await choose(page, mode, 'Only what’s new')
  await expect.poll(async () => (await mine()).autoClear).toBe(true)
  await choose(page, mode, 'Account’s: last lines')
  await expect.poll(async () => (await mine()).autoClear).toBe(null)

  // clear this buffer: nothing waiting, history kept
  await row.getByRole('button', { name: `Clear ${chan}` }).click()
  await expect(row.getByTestId('pb-waiting')).toHaveText('0')
  await expect(row).toContainText('cleared')
  await expect(row.getByRole('button', { name: `Clear ${chan}` })).toBeDisabled()
  expect((await me.api.get(`/api/networks/${net.id}/messages?target=${encodeURIComponent(chan)}`)).length).toBeGreaterThanOrEqual(3)

  // something new waits; clear all asks first
  talk.send(`PRIVMSG ${chan} :line 4`)
  await expect.poll(async () => (await mine()).waiting, { timeout: 10000 }).toBe(1)
  await page.reload()
  await expect(row.getByTestId('pb-waiting')).toHaveText('1')
  await box.getByRole('button', { name: 'Clear all' }).click()
  await expect(page.locator('dialog.confirm[open]')).toContainText(`Clear every buffer on Dev?`)
  await confirm(page)
  await expect(page.getByText('Cleared every buffer on Dev.')).toBeVisible()
  await expect(row.getByTestId('pb-waiting')).toHaveText('0')

  // an IRC client attaching gets none of it
  const c = await IRC.connect({ user: me.name, network: 'Dev', password: me.password })
  try {
    await c.waitFor(/ 376 /)
    const done = c.next(/evrd has been running/)
    c.send('PRIVMSG *status :uptime')
    await done
    expect(c.lines.some((l) => /line [1-4]$/.test(l))).toBe(false)
  } finally { c.close() }
})

test('the section is on the page’s contents, and only for existing networks', async ({ page }) => {
  await login(page, me.name, me.password)
  await page.goto(`/networks/${net.id}`)
  await page.getByRole('navigation', { name: 'Sections' }).getByRole('link', { name: 'Playback' }).click()
  await expect(page).toHaveURL(/#playback$/)
  await page.goto('/networks/new')
  await expect(page.getByTestId('playback')).toHaveCount(0)
})
