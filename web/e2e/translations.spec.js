import { test, expect } from '@playwright/test'
import { ADMIN, ADMIN_PASS, BASE, login, confirm, session, freshUser, choose } from './helpers.js'

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

test('an admin adds German; a user picks it', async ({ page }) => {
  const u = await freshUser('de')
  const a = await session(ADMIN, ADMIN_PASS)
  try {
    await login(page, ADMIN, ADMIN_PASS)
    await page.goto('/admin/settings')
    const langs = page.getByTestId('languages')
    await expect(langs.getByTestId('lang-it')).toContainText('Built in')
    await langs.getByTestId('lang-upload-file').setInputFiles(file('deutsch.json', {
      code: 'de', name: 'Deutsch',
      messages: { 'account.heading': 'Kontoeinstellungen', 'account.saveProfile': 'Profil speichern', 'account.nope': 'x' },
    }))
    const dlg = page.getByRole('dialog', { name: 'Check the translation' })
    await expect(dlg.getByLabel('Language code')).toHaveValue('de')
    await expect(dlg.getByLabel('Language name')).toHaveValue('Deutsch')
    await expect(dlg.getByTestId('pack-report')).toContainText('3 texts translated.')
    await expect(dlg.getByTestId('pack-report')).toContainText('1 key English doesn’t have, so the app won’t use it: account.nope')
    await expect(dlg.getByTestId('pack-report')).toContainText(/texts aren’t translated/)
    await dlg.getByRole('button', { name: 'Save translation' }).click()
    await expect(page.getByText('Deutsch saved — everyone can use it now')).toBeVisible()
    await expect(langs.getByTestId('lang-de')).toContainText(/Uploaded/)

    // Anyone can pick it, and its text shows; the rest is English.
    await page.context().clearCookies()
    await login(page, u.name, u.password)
    await page.goto('/account')
    await choose(page, page.getByLabel('Language', { exact: true }), 'Deutsch')
    await page.getByRole('button', { name: 'Save profile' }).click()
    await expect(page.getByRole('heading', { name: 'Kontoeinstellungen' })).toBeVisible()
    await expect(page.getByRole('button', { name: 'Profil speichern' })).toBeVisible()
    await expect(page.getByRole('tab', { name: 'Profile' })).toBeVisible()
    await page.reload()
    await expect(page.getByRole('heading', { name: 'Kontoeinstellungen' })).toBeVisible()
  } finally {
    await a.del('/api/admin/languages/de').catch(() => {})
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
    await expect(dlg.getByLabel('Language code')).toHaveValue('it')
    await expect(dlg.getByLabel('Language name')).toHaveValue('Italiano')
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
