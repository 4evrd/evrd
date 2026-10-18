import { test, expect } from '@playwright/test'
import { ADMIN, ADMIN_PASS, login, confirm, freshUser, session, addNetwork, uniq, BASE } from './helpers.js'

test.beforeEach(async ({ page }) => { await login(page, ADMIN, ADMIN_PASS) })

const drawer = (page) => page.locator('#user-drawer')
const row = (page, name) => page.getByTestId('user-row').filter({ hasText: name })

test('create a user: validation, duplicates, then they must choose a password', async ({ page, browser }) => {
  const name = uniq('new')
  await page.getByRole('link', { name: 'Users', exact: true }).click()
  await page.getByRole('button', { name: 'New user' }).click()
  const nd = page.locator('#new-drawer')
  await page.getByRole('button', { name: 'Create user' }).click()
  await expect(nd.getByText(/Use 2–32 lowercase letters/)).toBeVisible()
  await expect(nd.getByText('Use at least 10 characters.')).toBeVisible()

  await nd.getByLabel('Username').fill(ADMIN)
  await nd.getByLabel('Temporary password').fill('temporary-pass')
  await page.getByRole('button', { name: 'Create user' }).click()
  await expect(nd.getByText('That username is taken — pick another.')).toBeVisible()

  await nd.getByLabel('Username').fill(name)
  await page.getByRole('button', { name: 'Create user' }).click()
  await expect(page.getByText(`Created ${name}`)).toBeVisible()
  await expect(row(page, name)).toContainText('User')
  await expect(row(page, name)).toContainText('Offline')

  // the new user signs in and has to pick their own password first
  const ctx = await browser.newContext({ baseURL: BASE })
  const p = await ctx.newPage()
  await p.goto('/')
  await p.getByRole('textbox', { name: 'Username' }).fill(name)
  await p.getByLabel('Password', { exact: true }).fill('temporary-pass')
  await p.getByRole('button', { name: 'Sign in' }).click()
  await expect(p.getByRole('heading', { name: 'Choose your own password' })).toBeVisible()
  await p.locator('#s-cur').fill('temporary-pass')
  await p.locator('#s-new').fill('their-own-password')
  await p.locator('#s-rep').fill('their-own-password')
  await p.getByRole('button', { name: 'Change password' }).click()
  await expect(p.getByRole('heading', { name: 'Overview', exact: true })).toBeVisible()
  await ctx.close()

  await row(page, name).click()
  await drawer(page).getByRole('button', { name: 'Delete' }).click()
  await confirm(page, name)
  await expect(page.getByText(`Deleted ${name}`)).toBeVisible()
  await expect(row(page, name)).toHaveCount(0)
})

test('reset a password: a one-time password the user must change', async ({ page, browser }) => {
  const u = await freshUser('rst')
  try {
    await page.goto(`/admin/users?user=${u.name}`)
    await expect(drawer(page).getByRole('heading', { name: u.name })).toBeVisible()
    await drawer(page).getByRole('button', { name: 'Reset' }).click()
    await confirm(page)
    const temp = await drawer(page).getByLabel('Temporary password').inputValue()
    expect(temp.length).toBeGreaterThanOrEqual(10)
    // the old password stops working
    await expect(session(u.name, u.password)).rejects.toThrow(/401/)
    const ctx = await browser.newContext({ baseURL: BASE })
    const p = await ctx.newPage()
    await p.goto('/')
    await p.getByRole('textbox', { name: 'Username' }).fill(u.name)
    await p.getByLabel('Password', { exact: true }).fill(temp)
    await p.getByRole('button', { name: 'Sign in' }).click()
    await expect(p.getByRole('heading', { name: 'Choose your own password' })).toBeVisible()
    await ctx.close()
  } finally { await u.cleanup() }
})

test('suspend and restore a user', async ({ page }) => {
  const u = await freshUser('sus')
  try {
    await addNetwork(u.api, { name: 'Dev', nick: u.name })
    await page.goto(`/admin/users?user=${u.name}`)
    await expect(drawer(page).locator('.row', { hasText: 'Dev' })).toContainText('Connected')
    await drawer(page).getByRole('button', { name: 'Suspend' }).click()
    await expect(page.locator('dialog.confirm[open]')).toContainText(`Suspend ${u.name}?`)
    await confirm(page)
    await expect(page.getByText(`Suspended ${u.name}`)).toBeVisible()
    await expect(row(page, u.name)).toContainText('Suspended')
    // they're signed out and can't sign back in
    expect((await u.api.ctx.get('/api/me')).status()).toBe(401)
    await expect(session(u.name, u.password)).rejects.toThrow()
    // the overview flags them
    await page.goto('/admin')
    await expect(page.locator('.attn-row', { hasText: `${u.name} is suspended` })).toBeVisible()
    await page.locator('.attn-row', { hasText: `${u.name} is suspended` }).click()
    await expect(drawer(page).getByRole('heading', { name: u.name })).toBeVisible()
    await drawer(page).getByRole('button', { name: 'Restore' }).click()
    await expect(page.getByText(`Restored ${u.name}`)).toBeVisible()
    await expect(row(page, u.name)).not.toContainText('Suspended')
    await session(u.name, u.password).then((s) => s.dispose())
  } finally { await u.cleanup() }
})

test('per-user limits override the server defaults', async ({ page }) => {
  const u = await freshUser('lim')
  try {
    await page.goto(`/admin/users?user=${u.name}`)
    const d = drawer(page)
    await d.getByLabel('Max networks').fill('x')
    await page.getByRole('button', { name: 'Save limits' }).click()
    await expect(d.getByText(/Enter a whole number/)).toBeVisible()
    await d.getByLabel('Max networks').fill('1')
    await d.getByLabel('Max clients').fill('2')
    await d.getByLabel('Backlog lines').fill('500')
    await page.getByRole('button', { name: 'Save limits' }).click()
    await expect(page.getByText(`Saved limits for ${u.name}`)).toBeVisible()
    await expect(row(page, u.name)).toContainText('0 / 1')
    await expect(row(page, u.name)).toContainText('0 / 2')
    expect((await u.api.get('/api/me')).limits).toMatchObject({ maxNetworks: 1, maxClients: 2, backlogLines: 500 })

    // the limit bites: a second network is refused
    await addNetwork(u.api, { name: 'One', nick: u.name })
    await expect(u.api.post('/api/networks', { name: 'Two', host: 'ircd', port: 6667, tls: false, nick: u.name })).rejects.toThrow()

    // empty goes back to the default
    await row(page, u.name).click()
    await expect(d.getByLabel('Max networks')).toHaveValue('1')
    await d.getByLabel('Max networks').fill('')
    await d.getByLabel('Max clients').fill('')
    await d.getByLabel('Backlog lines').fill('')
    await page.getByRole('button', { name: 'Save limits' }).click()
    await expect(row(page, u.name)).toContainText('1 / ∞')
  } finally { await u.cleanup() }
})

test('make someone an admin and back', async ({ page }) => {
  const u = await freshUser('mk')
  try {
    await page.goto(`/admin/users?user=${u.name}`)
    const sw = drawer(page).getByRole('switch', { name: 'Administrator' })
    await sw.click()
    await expect(page.getByText(`${u.name} is now an admin`)).toBeVisible()
    await expect(row(page, u.name)).toContainText('Admin')
    expect((await u.api.get('/api/me')).role).toBe('admin')
    await sw.click()
    await expect(page.getByText(`${u.name} is no longer an admin`)).toBeVisible()
    expect((await u.api.get('/api/me')).role).toBe('user')
  } finally { await u.cleanup() }
})

test('you can’t suspend or delete yourself here', async ({ page }) => {
  await page.goto(`/admin/users?user=${ADMIN}`)
  await expect(drawer(page).getByRole('heading', { name: ADMIN })).toBeVisible()
  await expect(drawer(page).getByRole('button', { name: 'Suspend' })).toHaveCount(0)
  await expect(drawer(page).getByRole('button', { name: 'Delete' })).toHaveCount(0)
})

test('search, filter and deep links on the users list', async ({ page }) => {
  const a = await freshUser('flt', { admin: true })
  const b = await freshUser('flt')
  try {
    await page.goto('/admin/users')
    await page.locator('#page-search').fill(a.name)
    await expect(page.getByTestId('user-row')).toHaveCount(1)
    await page.locator('#page-search').fill('flt')
    await expect(page.getByTestId('user-row')).toHaveCount(2)
    await page.getByRole('button', { name: 'Admins', exact: true }).click()
    await expect(page.getByTestId('user-row')).toHaveCount(1)
    await expect(row(page, a.name)).toBeVisible()
    await page.getByRole('button', { name: 'Suspended', exact: true }).click()
    await expect(page.getByText('No users match')).toBeVisible()
    await page.getByRole('button', { name: 'All', exact: true }).click()

    // keyboard: Enter opens a row
    await row(page, b.name).focus()
    await page.keyboard.press('Enter')
    await expect(drawer(page).getByRole('heading', { name: b.name })).toBeVisible()

    await page.goto(`/admin/users?id=${b.id}`)
    await expect(drawer(page).getByRole('heading', { name: b.name })).toBeVisible()
    await page.goto('/admin/users#new')
    await expect(page.locator('#new-drawer').getByLabel('Username')).toBeVisible()
  } finally { await a.cleanup(); await b.cleanup() }
})

test('bouncer overview: tiles, placement, upstreams and storage', async ({ page }) => {
  const u = await freshUser('ov')
  try {
    await addNetwork(u.api, { name: 'Dev', nick: u.name })
    await page.getByRole('link', { name: 'Overview' }).nth(1).click()
    await expect(page.getByRole('heading', { name: 'Bouncer overview' })).toBeVisible()
    const all = (await (await session(ADMIN, ADMIN_PASS)).get('/api/admin/users')).length
    await expect(page.getByTestId('ov-users')).toHaveText(String(all))
    await expect(page.getByText(/nodes? holding/)).toBeVisible()
    // every placed account hangs off a node; the new one is there, linked
    const node = page.locator('.user-node', { hasText: u.name })
    await expect(node).toContainText('1/1 links')
    await page.getByRole('tab', { name: /Online/ }).click()
    await expect(page.locator('.user-node', { hasText: u.name })).toHaveCount(0)
    await page.getByRole('tab', { name: /All/ }).click()
    await expect(page.locator('.up-row', { hasText: 'ircd' })).toContainText('All up')
    await expect(page.locator('.kv', { hasText: 'Accounts' }).last()).toContainText('PostgreSQL')
    await expect(page.locator('.kv', { hasText: 'Chat history' })).toContainText('ScyllaDB')
    await expect(page.locator('.kv', { hasText: 'Networks per user' })).toContainText('∞')
    await node.click()
    await expect(drawer(page).getByRole('heading', { name: u.name })).toBeVisible()
  } finally { await u.cleanup() }
})
