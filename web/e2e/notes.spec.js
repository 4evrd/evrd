import { test, expect } from '@playwright/test'
import { login, freshUser, addNetwork } from './helpers.js'
import { IRC } from './irc.js'

// Notes (ZNC's notes module): kept in Account → Notes and at /msg *notes,
// the same ones both ways, and shown to an IRC client as it attaches.

let me
const clients = []
test.beforeEach(async ({ page }) => {
  me = await freshUser('nt')
  await addNetwork(me.api, { name: 'Dev', nick: me.name })
  await login(page, me.name, me.password)
})
test.afterEach(async () => {
  while (clients.length) clients.pop().close()
  await me.cleanup()
})

async function attach() {
  const c = await IRC.connect({ user: me.name, network: 'Dev', password: me.password })
  clients.push(c)
  await c.waitFor(/ 376 /)
  return c
}

// notes sends a line to *notes and waits for its reply matching re.
function notes(c, line, re) {
  const reply = c.next(new RegExp(`^:\\*notes!\\S+ PRIVMSG \\S+ :${re}`))
  c.send(`PRIVMSG *notes :${line}`)
  return reply
}

test('notes from the web app reach IRC clients, and *notes edits the same ones', async ({ page }) => {
  await page.goto('/account#notes')
  await expect(page.getByRole('tab', { name: 'Notes' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByTestId('notes-none')).toBeVisible()
  const form = page.getByRole('form', { name: 'Add or change a note' })
  await form.getByLabel('Key').fill('two words')
  await form.getByLabel('Note', { exact: true }).fill('x')
  await form.getByRole('button', { name: 'Save note' }).click()
  await expect(form.getByText('A key is one word')).toBeVisible()
  await form.getByLabel('Key').fill('wifi')
  await form.getByLabel('Note', { exact: true }).fill('hunter2 at home')
  await form.getByRole('button', { name: 'Save note' }).click()
  await expect(page.getByText('Note wifi saved')).toBeVisible()
  await expect(page.getByTestId('note')).toHaveCount(1)
  await expect(page.getByTestId('note')).toContainText('hunter2 at home')

  // shown as the client attaches, from *notes
  const c = await attach()
  await c.waitFor(/^:\*notes!\S+ NOTICE \S+ :Your notes \(1\) - \/msg \*notes help to edit them:$/)
  await c.waitFor(/^:\*notes!\S+ NOTICE \S+ :wifi: hunter2 at home$/)

  // *notes: add, the shorthand, list, get, delete
  await notes(c, 'add door 1234', 'Note door saved\\.')
  await notes(c, 'add door 5678', 'There\'s a note door already')
  await notes(c, '+todo buy milk', 'Note todo saved\\.')
  await notes(c, 'get todo', 'buy milk$')
  await notes(c, '-todo', 'Note todo deleted\\.')
  await notes(c, 'list', '.*\\| door +\\| 1234')
  // *status's notes command is the same
  const st = c.next(/^:\*status!\S+ PRIVMSG \S+ :.*\| wifi +\| hunter2 at home/)
  c.send('PRIVMSG *status :notes list')
  await st

  // the web app has them; editing one there changes it for IRC
  await page.reload()
  await expect(page.getByTestId('note')).toHaveCount(2)
  await page.getByRole('button', { name: 'Edit door' }).click()
  await expect(form.getByLabel('Key')).toHaveValue('door')
  await form.getByLabel('Note', { exact: true }).fill('4321')
  await form.getByRole('button', { name: 'Save note' }).click()
  await expect(page.getByText('Note door saved')).toBeVisible()
  await notes(c, 'get door', '4321$')
  await page.getByRole('button', { name: 'Delete wifi' }).click()
  await expect(page.getByText('Note wifi deleted')).toBeVisible()
  await expect(page.getByTestId('note')).toHaveCount(1)
  await notes(c, 'get wifi', 'No note wifi\\.')

  // not shown on attach with the switch off
  await page.getByRole('switch', { name: 'Show my notes to IRC clients as they connect' }).click()
  await expect.poll(async () => (await me.api.get('/api/me')).settings.showNotesOnLogin).toBe(false)
  const d = await attach()
  const up = d.next(/^:\*status!\S+ PRIVMSG \S+ :evrd has been running/)
  d.send('PRIVMSG *status :uptime')
  await up
  expect(d.lines.some((l) => / :Your notes \(/.test(l))).toBe(false)
})
