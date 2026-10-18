import { test, expect } from '@playwright/test'
import { login, confirm, freshUser, session, BASE } from './helpers.js'

let u
test.beforeEach(async ({ page }) => {
  u = await freshUser('acct')
  await login(page, u.name, u.password)
})
test.afterEach(async () => { await u.cleanup() })

test('profile: email, nicks and auto-away are saved', async ({ page }) => {
  await page.goto('/account')
  await expect(page.getByTestId('acct-username')).toHaveValue(u.name)
  await page.getByLabel('Email').fill('not-an-email')
  await page.getByLabel('Default nickname').fill('9bad')
  await page.getByRole('button', { name: 'Save profile' }).click()
  await expect(page.getByText('That doesn’t look like an email address.')).toBeVisible()
  await expect(page.getByText(/Nicks start with a letter/)).toBeVisible()

  await page.getByLabel('Email').fill(`${u.name}@example.org`)
  await page.getByLabel('Default nickname').fill(u.name + 'x')
  await page.getByLabel('Alternate nickname').fill(u.name + '_')
  await page.getByLabel('Real name').fill('E2E Person')
  await page.getByRole('switch', { name: /Mark me away/ }).click()
  await page.getByRole('button', { name: 'Save profile' }).click()
  await expect(page.getByText('Profile saved')).toBeVisible()

  await page.reload()
  await expect(page.getByLabel('Email')).toHaveValue(`${u.name}@example.org`)
  await expect(page.getByLabel('Default nickname')).toHaveValue(u.name + 'x')
  await expect(page.getByLabel('Alternate nickname')).toHaveValue(u.name + '_')
  await expect(page.getByLabel('Real name')).toHaveValue('E2E Person')
  await expect(page.getByRole('switch', { name: /Mark me away/ })).toHaveAttribute('aria-checked', 'true')
  expect(await u.api.get('/api/me')).toMatchObject({ defaultNick: u.name + 'x', defaultAltNick: u.name + '_', autoAway: true })

  // the new default nick pre-fills the add-network form
  await page.goto('/networks#add')
  await expect(page.getByRole('form', { name: 'Add a network' }).getByLabel('Nickname')).toHaveValue(u.name + 'x')
})

test('tabs: keyboard and deep links', async ({ page }) => {
  await page.goto('/account#sessions')
  await expect(page.getByRole('tab', { name: 'Sessions' })).toHaveAttribute('aria-selected', 'true')
  await page.getByRole('tab', { name: 'Sessions' }).press('ArrowRight')
  await expect(page.getByRole('tab', { name: 'Delete account' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByRole('tab', { name: 'Delete account' })).toBeFocused()
  await expect(page).toHaveURL(/#danger$/)
  await page.keyboard.press('ArrowRight')
  await expect(page.getByRole('tab', { name: 'Profile' })).toHaveAttribute('aria-selected', 'true')
  await page.keyboard.press('ArrowLeft')
  await expect(page.getByRole('tab', { name: 'Delete account' })).toHaveAttribute('aria-selected', 'true')
  await expect(page.getByRole('tab')).toHaveCount(4)
  // the user pill leads here
  await page.goto('/')
  await page.getByRole('link', { name: `Account: ${u.name}` }).click()
  await expect(page.getByRole('heading', { name: 'Account settings' })).toBeVisible()
})

test('change password: validation, then other sessions are signed out', async ({ page }) => {
  const other = await session(u.name, u.password)
  await page.goto('/account#security')
  const save = page.getByRole('button', { name: 'Change password' })
  await save.click()
  await expect(page.locator('#pw-summary')).toContainText('Fix 2 things before saving')

  await page.locator('#s-cur').fill('wrong-password')
  await page.locator('#s-new').fill('short')
  await page.locator('#s-rep').fill('different')
  await save.click()
  await expect(page.getByText('Use at least 10 characters.', { exact: true })).toBeVisible()
  await expect(page.getByText('The passwords don’t match.', { exact: true })).toBeVisible()

  await page.locator('#s-new').fill('a-brand-new-password')
  await page.locator('#s-rep').fill('a-brand-new-password')
  await save.click()
  await expect(page.getByText('That isn’t your current password.', { exact: true })).toBeVisible()

  await page.locator('#s-cur').fill(u.password)
  await save.click()
  await expect(page.getByText('Password changed. Other web sessions were signed out.')).toBeVisible()
  // this tab stays signed in; the other session doesn't
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Account settings' })).toBeVisible()
  expect((await other.ctx.get('/api/me')).status()).toBe(401)
  await other.dispose()
  u.password = 'a-brand-new-password'
})

test('sessions: see where you are signed in and sign the others out', async ({ page, browser }) => {
  const ctx2 = await browser.newContext({ baseURL: BASE, userAgent: 'Mozilla/5.0 (Windows NT 10.0) Firefox/130.0' })
  const p2 = await ctx2.newPage()
  await login(p2, u.name, u.password)

  await page.goto('/account#sessions')
  const rows = page.locator('#sessions .sess-row')
  await expect(rows.filter({ hasText: 'Firefox on Windows' })).toBeVisible()
  await expect(rows.filter({ hasText: 'This device' })).toHaveCount(1)
  await page.getByRole('button', { name: 'Sign out other sessions' }).click()
  await confirm(page)
  await expect(page.getByText('Signed out other sessions')).toBeVisible()
  await expect(rows).toHaveCount(1)
  await expect(page.getByRole('button', { name: 'Sign out other sessions' })).toBeDisabled()

  await p2.reload()
  await expect(p2.getByRole('button', { name: 'Sign in' })).toBeVisible()

  // a single session can be signed out from its row
  await login(p2, u.name, u.password)
  await page.reload()
  await page.getByRole('button', { name: 'Sign out Firefox on Windows' }).click()
  await expect(rows).toHaveCount(1)
  await p2.reload()
  await expect(p2.getByRole('button', { name: 'Sign in' })).toBeVisible()
  await ctx2.close()
})

test('delete account', async ({ page }) => {
  await page.goto('/account#danger')
  await page.getByRole('button', { name: 'Delete my account' }).click()
  const dlg = page.locator('dialog.confirm[open]')
  await expect(dlg).toContainText('Delete your account?')
  // the button stays off until the username is typed
  await expect(dlg.locator('#dlg-ok')).toBeDisabled()
  await dlg.getByRole('textbox').fill('nope')
  await expect(dlg.locator('#dlg-ok')).toBeDisabled()
  await confirm(page, u.name)
  await expect(page.getByRole('button', { name: 'Sign in' })).toBeVisible()
  const res = await (await session('e2eadmin', 'e2eadminpass')).get('/api/admin/users')
  expect(res.some((x) => x.username === u.name)).toBe(false)
})

test('an admin is told who keeps admin rights', async ({ page }) => {
  // e2eadmin always exists, so this admin is never the only one
  const a = await freshUser('adm', { admin: true })
  try {
    await page.getByRole('button', { name: 'Sign out' }).click()
    await login(page, a.name, a.password)
    await page.goto('/account#danger')
    await expect(page.getByText(/Admin rights stay with the other admins?[,:]/)).toBeVisible()
    await expect(page.getByRole('button', { name: 'Delete my account' })).toBeEnabled()
  } finally { await a.cleanup() }
})
