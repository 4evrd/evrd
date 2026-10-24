import { test, expect } from '@playwright/test'
import { ADMIN, ADMIN_PASS, BASE, login, confirm, session, freshUser, choose, addNetwork } from './helpers.js'
import { IRC } from './irc.js'

// Language packs (docs/translations.md): an admin's for everyone, a user's
// own on top. Uploaded files go in through the hidden file inputs.
const file = (name, data) => ({ name, mimeType: 'application/octet-stream', buffer: Buffer.from(typeof data === 'string' ? data : JSON.stringify(data)) })

// signIn is login for a user whose app isn't in English.
async function signIn(page, u) {
  await page.goto('/')
  await page.getByRole('textbox', { name: 'Username' }).fill(u.name)
  await page.getByLabel('Password', { exact: true }).fill(u.password)
  await page.getByRole('button', { name: 'Sign in' }).click()
  await expect(page.getByRole('textbox', { name: 'Username' })).toHaveCount(0)
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible()
}

test('an admin adds Polish; a user picks it', async ({ page }) => {
  const u = await freshUser('pl')
  const a = await session(ADMIN, ADMIN_PASS)
  try {
    await login(page, ADMIN, ADMIN_PASS)
    await page.goto('/admin/settings')
    const langs = page.getByTestId('languages')
    await expect(langs.getByTestId('lang-it')).toContainText('Built in')
    await langs.getByTestId('lang-upload-file').setInputFiles(file('polski.json', {
      code: 'pl', name: 'Polski',
      messages: { 'account.heading': 'Kontoeinstellungen', 'account.saveProfile': 'Profil speichern', 'account.nope': 'x' },
    }))
    const dlg = page.getByRole('dialog', { name: 'Check the translation' })
    // a language evrd doesn't have: added as a new one
    await expect(dlg.getByTestId('lang-upload-mode-new')).toBeChecked()
    await expect(dlg.getByLabel('Language code')).toHaveValue('pl')
    await expect(dlg.getByLabel('Language name')).toHaveValue('Polski')
    await expect(dlg.getByTestId('pack-report')).toContainText('3 texts translated.')
    await expect(dlg.getByTestId('pack-report')).toContainText('1 key English doesn’t have, so the app won’t use it: account.nope')
    await expect(dlg.getByTestId('pack-report')).toContainText(/texts aren’t translated/)
    await dlg.getByRole('button', { name: 'Save translation' }).click()
    await expect(page.getByText('Polski saved — everyone can use it now')).toBeVisible()
    await expect(langs.getByTestId('lang-pl')).toContainText(/Uploaded/)

    // Anyone can pick it, and its text shows; the rest is English.
    await page.context().clearCookies()
    await login(page, u.name, u.password)
    await page.goto('/account')
    await choose(page, page.getByLabel('Language', { exact: true }), 'Polski')
    await page.getByRole('button', { name: 'Save profile' }).click()
    await expect(page.getByRole('heading', { name: 'Kontoeinstellungen' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Profil speichern' })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Profile' })).toBeVisible()
    await page.reload()
    await expect(page.getByRole('heading', { name: 'Kontoeinstellungen' })).toBeVisible()
  } finally {
    await a.del('/api/admin/languages/pl').catch(() => {})
    await a.dispose()
    await u.cleanup()
  }
})

test('a placeholder that differs from English blocks the upload', async ({ page }) => {
  await login(page, ADMIN, ADMIN_PASS)
  await page.goto('/admin/settings')
  await page.getByTestId('lang-upload-file').setInputFiles(file('x.json', { code: 'qx', name: 'Test', messages: { 'account.ownCurrent': '{nome} ({code})' } }))
  const dlg = page.getByRole('dialog', { name: 'Check the translation' })
  await expect(dlg.getByText(/account\.ownCurrent: the placeholders should be \{code\} \{name\} \{time\}, as in English\./)).toBeVisible()
  await expect(dlg.getByRole('button', { name: 'Save translation' })).toBeDisabled()
  await dlg.getByRole('button', { name: 'Cancel' }).click()
  await expect(dlg).toBeHidden()
})

test('your own translation changes one Italian string', async ({ page, browser }) => {
  const u = await freshUser('own')
  try {
    await login(page, u.name, u.password)
    await page.goto('/account')
    const own = page.getByTestId('own-translation')
    await expect(own.getByTestId('own-status')).toHaveText('You haven’t uploaded one.')
    // a CSV (key, english, translation): the code comes from the file name
    await own.getByTestId('own-upload-file').setInputFiles(file('evrd-it.csv', 'key,english,translation,notes\r\naccount.heading,Account settings,Il mio account,\r\naccount.intro,"Your profile, sign-in…",,\r\n'))
    const dlg = page.getByRole('dialog', { name: 'Check the translation' })
    // a language evrd has: it changes that one, keeping its name
    await expect(dlg.getByTestId('own-upload-mode-change')).toBeChecked()
    await expect(dlg.getByTestId('own-upload-target')).toContainText('Italiano')
    await expect(dlg.getByLabel('Language code')).toHaveCount(0)
    // as a new language it would clash with Italiano
    await dlg.getByTestId('own-upload-mode-new').check()
    await dlg.getByLabel('Language code').fill('it')
    await expect(dlg.getByTestId('own-upload-exists')).toContainText('Italiano already exists')
    await expect(dlg.getByRole('button', { name: 'Save translation' })).toBeDisabled()
    await dlg.getByTestId('own-upload-mode-change').check()
    await dlg.getByRole('button', { name: 'Save translation' }).click()
    await expect(page.getByText('Your translation is saved. Pick Italiano under Language to use it.')).toBeVisible()
    await expect(own.getByTestId('own-status')).toContainText('Italiano (it)')

    await choose(page, page.getByLabel('Language', { exact: true }), 'Italiano')
    await page.getByRole('button', { name: 'Save profile' }).click()
    // the user's string, and the built-in Italian for the rest
    await expect(page.getByRole('heading', { name: 'Il mio account' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Salva il profilo' })).toBeVisible()

    // Someone else's Italian is the built-in one.
    const other = await freshUser('own2')
    try {
      await other.api.patch('/api/me', { language: 'it' })
      const ctx = await browser.newContext({ baseURL: BASE })
      const p2 = await ctx.newPage()
      await signIn(p2, other)
      await p2.goto('/account')
      await expect(p2.getByRole('heading', { name: 'Impostazioni dell’account' })).toBeVisible()
      await ctx.close()
    } finally { await other.cleanup() }

    // Removing it goes back to the built-in Italian.
    await page.getByTestId('own-translation').getByRole('button', { name: 'Rimuovi' }).click()
    await confirm(page)
    await expect(page.getByRole('heading', { name: 'Impostazioni dell’account' })).toBeVisible()
  } finally { await u.cleanup() }
})

test('text from a language pack is never HTML', async ({ page }) => {
  const u = await freshUser('xss')
  try {
    const evil = '<img src=x onerror=alert(1)>'
    await u.api.call('PUT', '/api/me/language-pack', {
      code: 'xx', name: '<b>Evil</b>',
      messages: { 'account.heading': evil, 'account.languageHelp': '<b id="bold">bold</b> help' },
    })
    await u.api.patch('/api/me', { language: 'xx' })
    const dialogs = []
    page.on('dialog', (d) => { dialogs.push(d.message()); d.dismiss() })
    await signIn(page, u)
    await page.goto('/account')
    await expect(page.getByRole('heading', { name: evil })).toBeVisible()
    await expect(page.getByText('<b id="bold">bold</b> help')).toBeVisible() // a Field's help
    await expect(page.getByLabel('Language', { exact: true })).toContainText('<b>Evil</b>')
    await expect(page.locator('img[src="x"]')).toHaveCount(0)
    await expect(page.locator('#bold')).toHaveCount(0)
    expect(dialogs).toEqual([])
  } finally { await u.cleanup() }
})

test('anyone can download a language to edit, in any format', async ({ page }) => {
  const u = await freshUser('dl')
  try {
    await login(page, u.name, u.password)
    await page.goto('/account')
    const box = page.getByTestId('download-language')
    await choose(page, box.getByTestId('dl-lang').getByRole('combobox'), 'Italiano')
    await box.getByRole('button', { name: 'Download' }).click()
    const got = page.waitForEvent('download')
    await page.getByRole('menuitem', { name: /JSON/ }).click()
    const dl = await got
    expect(dl.suggestedFilename()).toBe('evrd-it.json')
    const pack = JSON.parse(await (await import('node:fs/promises')).readFile(await dl.path(), 'utf8'))
    expect(pack.code).toBe('it')
    expect(pack.messages['account.heading']).toBe('Impostazioni dell’account')
    // evrd's IRC messages are in the same file
    expect(pack.messages['irc.notOn']).toBe('Non sei su {channel}.')
  } finally {
    await u.cleanup()
  }
})

// statusSays sends a *status command and waits for a reply containing text.
function statusSays(c, cmd, text) {
  const esc = text.replace(/[#.*+?^${}()|[\]\\]/g, '\\$&')
  const reply = c.next(new RegExp(`^:\\*status!\\S+ PRIVMSG \\S+ :.*${esc}`))
  c.send(`PRIVMSG *status :${cmd}`)
  return reply
}

test('*status answers in your language, your own translation on top', async () => {
  const u = await freshUser('ircit')
  let c
  try {
    await addNetwork(u.api, { name: 'Dev', nick: u.name })
    c = await IRC.connect({ user: u.name, network: 'Dev', password: u.password })
    await c.waitFor(/ 376 /)
    await statusSays(c, 'nope', 'Unknown command "nope" — try help.')

    // the language is read again for each command
    await u.api.patch('/api/me', { language: 'it' })
    await statusSays(c, 'nope', 'Comando sconosciuto "nope": prova help.')
    await statusSays(c, 'help', 'Comandi di evrd')
    await statusSays(c, 'detach #nope', 'Non sei su #nope.')

    // your own translation of one IRC message wins; the rest stays Italian
    await u.api.call('PUT', '/api/me/language-pack', { code: 'it', name: 'Italiano', messages: { 'irc.notOn': 'Qui non ci sei: {channel}' } })
    await statusSays(c, 'detach #nope', 'Qui non ci sei: #nope')
    await statusSays(c, 'nope', 'Comando sconosciuto')
  } finally {
    c?.close()
    await u.cleanup()
  }
})

test('the languages list says how many IRC messages each language has', async ({ page }) => {
  const { total, languages } = await (await fetch(BASE + '/api/irc-messages')).json()
  expect(total).toBeGreaterThan(100)
  expect(languages.it).toBe(total)
  await login(page, ADMIN, ADMIN_PASS)
  await page.goto('/admin/settings')
  await expect(page.getByTestId('lang-irc-it')).toHaveText(`IRC messages (*status): ${total} of ${total} translated`)
  await expect(page.getByTestId('lang-irc-ja')).toHaveText(`IRC messages (*status): ${total} of ${total} translated`)
})
